package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"os"
	"testing"

	mediaopus "github.com/bluenviron/mediacommon/v2/pkg/codecs/opus"
	"github.com/stretchr/testify/require"
)

// Checking a sound before it is kept (design/3.2.0.md §7.8). The fixtures are
// real packets from the browsers' own encoders, made in B0 by the probe page
// (build/wcprobe.html) in headless Chrome and Firefox at the contract's
// settings: 40 kbit/s CBR, 20 ms frames, mono.

type soundFixtureFile struct {
	UA          string              `json:"ua"`
	Supported   bool                `json:"supported"`
	BitrateMode string              `json:"bitrateMode"`
	Fixtures    map[string][]string `json:"fixtures"`
}

// soundFixtures is one browser's six signals, as packets.
func soundFixtures(t testing.TB, browser string) map[string][][]byte {
	t.Helper()

	raw, err := os.ReadFile("testdata/sound-" + browser + "-40k.json")
	require.NoError(t, err)

	var file soundFixtureFile
	require.NoError(t, json.Unmarshal(raw, &file))
	require.True(t, file.Supported)
	require.Equal(t, "constant", file.BitrateMode)

	fixtures := map[string][][]byte{}
	for name, encoded := range file.Fixtures {
		for _, packet := range encoded {
			decoded, err := base64.StdEncoding.DecodeString(packet)
			require.NoError(t, err)
			fixtures[name] = append(fixtures[name], decoded)
		}
	}

	return fixtures
}

func TestHonestSoundsFromBothBrowsersPass(t *testing.T) {
	for _, browser := range []string{"chromium", "firefox"} {
		fixtures := soundFixtures(t, browser)

		for name, ms := range map[string]int64{"tone1s": 1020, "noise2s": 2020, "blip": 260, "full6s": 6020} {
			packets := fixtures[name]

			// Through the upload's own layout, as the panel sends it.
			parsed, ok := parseSoundPackets(encodeSoundPackets(packets))
			require.True(t, ok, "%s %s", browser, name)

			checked, code, err := checkSoundPackets(parsed)
			require.NoError(t, err)
			require.Empty(t, code, "%s %s", browser, name)
			require.Equal(t, ms, checked.ms, "%s %s: count × 20", browser, name)
			require.InDelta(t, -21, checked.level, 2, "%s %s", browser, name)
		}

		require.Len(t, fixtures["full6s"], maxSoundPackets, "a full 6.00 s trim is 301 packets, and passes")

		_, code, _ := checkSoundPackets(fixtures["loud"])
		require.Equal(t, soundCodeTooLoud, code, "%s: a 1 s tone at -4.6 LUFS", browser)

		_, code, _ = checkSoundPackets(fixtures["quiet"])
		require.Equal(t, soundCodeSilent, code, "%s: one at -69.7", browser)
	}
}

// Each row of §7.8's table, one by one.
func TestEveryRowOfTheUploadTableIsRefused(t *testing.T) {
	tone := soundFixtures(t, "chromium")["tone1s"]

	// The packets section's shape.
	require.Len(t, tone, 51)
	_, ok := parseSoundPackets(encodeSoundPackets(tone[:12]))
	require.False(t, ok, "twelve packets is under a quarter of a second")
	_, ok = parseSoundPackets(encodeSoundPackets(tone[:13]))
	require.True(t, ok, "thirteen is the shortest trim")

	long := [][]byte{}
	for len(long) < maxSoundPackets+1 {
		long = append(long, tone...)
	}
	_, ok = parseSoundPackets(encodeSoundPackets(long[:maxSoundPackets]))
	require.True(t, ok, "301 is a full 6.00 s")
	_, ok = parseSoundPackets(encodeSoundPackets(long[:maxSoundPackets+1]))
	require.False(t, ok, "302 is over")

	// 301 packets of 128 bytes fit in 39 132; anything past 40 000 is
	// refused before a byte of it is read as a packet.
	big := make([]byte, maxSoundSectionBytes+1)
	binary.BigEndian.PutUint16(big, 100)
	_, ok = parseSoundPackets(big)
	require.False(t, ok, "over 40 000 bytes")

	withPacket := func(index int, packet []byte) [][]byte {
		changed := append([][]byte{}, tone...)
		changed[index] = packet

		return changed
	}
	_, ok = parseSoundPackets(encodeSoundPackets(withPacket(5, []byte{})))
	require.False(t, ok, "an empty packet")
	_, ok = parseSoundPackets(encodeSoundPackets(withPacket(5, make([]byte, 129))))
	require.False(t, ok, "a packet of 129 bytes")

	section := encodeSoundPackets(tone)
	_, ok = parseSoundPackets(append(section, 0))
	require.False(t, ok, "a byte after the last packet")
	_, ok = parseSoundPackets(section[:len(section)-1])
	require.False(t, ok, "a packet cut short")
	lying := append([]byte{}, section...)
	binary.BigEndian.PutUint16(lying, 52)
	_, ok = parseSoundPackets(lying)
	require.False(t, ok, "a count that is not the packets'")

	// The TOC byte: one frame, mono, 20 ms. Two 10 ms frames (code 1), four
	// of 5 ms (code 3) and a stereo 20 ms frame all last 960 samples by
	// PacketDuration2, which alone would pass them, and are refused.
	first := tone[5][0]
	for about, packet := range map[string][]byte{
		"code 1, two 10 ms frames": append([]byte{30<<3 | 1}, make([]byte, 99)...),
		"code 3, four 5 ms frames": append([]byte{29<<3 | 3, 4}, make([]byte, 98)...),
		"stereo":                   append([]byte{first | 4}, tone[5][1:]...),
	} {
		require.EqualValues(t, 960, mediaopus.PacketDuration2(packet), "%s: the duration alone would pass it", about)
		require.False(t, soundTOCOK(packet), about)

		_, code, _ := checkSoundPackets(withPacket(5, packet))
		require.Equal(t, soundCodeBadSound, code, about)
	}
	for about, toc := range map[string]byte{
		"code 1, two 20 ms frames":   first | 1,
		"a 10 ms configuration":      30 << 3,
		"a 60 ms SILK configuration": 3 << 3,
	} {
		packet := append([]byte{toc}, tone[5][1:]...)
		require.False(t, soundTOCOK(packet), about)

		_, code, _ := checkSoundPackets(withPacket(5, packet))
		require.Equal(t, soundCodeBadSound, code, about)
	}
	for _, config := range []byte{1, 5, 9, 13, 15, 19, 23, 27, 31} {
		require.True(t, soundTOCOK([]byte{config << 3}), "configuration %d is 20 ms", config)
	}

	// Every packet must decode into 960 samples. pion decodes anything of
	// the right shape (two hundred thousand random packets that pass the TOC
	// rule all did), so a stand-in says otherwise here.
	previous := newSoundDecoder
	t.Cleanup(func() { newSoundDecoder = previous })
	for about, answer := range map[string]fixedDecoder{
		"an error":        {err: errors.New("invalid frame")},
		"480 samples":     {samples: 480},
		"a 40 ms frame's": {samples: 1920},
	} {
		newSoundDecoder = func() (soundDecoder, error) { return answer, nil }
		_, code, err := checkSoundPackets(tone)
		require.NoError(t, err, about)
		require.Equal(t, soundCodeBadSound, code, about)
	}
}

// fixedDecoder answers every packet the same way.
type fixedDecoder struct {
	samples int
	err     error
}

func (d fixedDecoder) DecodeToFloat32(_ []byte, _ []float32) (int, error) {
	return d.samples, d.err
}

// The ceiling on synthetic sound: a steady tone at -20, as the panel makes
// it, passes; one 10 dB louder fails as a whole; a short blast hidden in a
// quiet sound fails by its window; silence is silent.
func TestTheCeilingHoldsWholeAndWindow(t *testing.T) {
	tone := func(seconds, frequency, amplitude float64) []float32 {
		pcm := make([]float32, int(seconds*48000))
		for i := range pcm {
			pcm[i] = float32(amplitude * math.Sin(2*math.Pi*frequency*float64(i)/48000))
		}

		return pcm
	}

	at := func(pcm []float32, lufs float64) []float32 {
		whole, _ := soundLevels(pcm)
		gain := math.Pow(10, (lufs-whole)/20)
		for i := range pcm {
			pcm[i] *= float32(gain)
		}

		return pcm
	}

	level, code := soundCeiling(at(tone(2, 997, 0.1), -20))
	require.Empty(t, code)
	require.InDelta(t, -20, level, 0.1)

	_, code = soundCeiling(at(tone(2, 997, 0.1), -10))
	require.Equal(t, soundCodeTooLoud, code, "-10 as a whole")

	quiet := at(tone(6, 997, 0.1), -40)
	blast := at(tone(0.3, 997, 0.1), -2)
	copy(quiet[48000:], blast)
	whole, loudest := soundLevels(quiet)
	require.Less(t, whole, soundCeilingWhole, "quiet enough as a whole")
	require.Greater(t, loudest, soundCeilingWindow, "but not in the window that holds the blast")
	_, code = soundCeiling(quiet)
	require.Equal(t, soundCodeTooLoud, code)

	_, code = soundCeiling(make([]float32, 48000))
	require.Equal(t, soundCodeSilent, code)

	short := at(tone(0.25, 997, 0.1), -20)
	_, loudest = soundLevels(short)
	require.InDelta(t, -20+10*math.Log10(0.25/0.4), loudest, 0.2, "a sound under 400 ms is one window padded with silence")
}

// A 30 Hz tone levelled to -20 LUFS, as the panel's BS.1770 would level it,
// passes; plain RMS would have read it 8.3 dB louder and refused it.
func TestBassIsNotTooLoud(t *testing.T) {
	pcm := make([]float32, 3*48000)
	for i := range pcm {
		pcm[i] = float32(math.Sin(2 * math.Pi * 30 * float64(i) / 48000))
	}

	whole, _ := soundLevels(pcm)
	gain := math.Pow(10, (-20-whole)/20)
	sumSquares := 0.0
	for i := range pcm {
		pcm[i] *= float32(gain)
		sumSquares += float64(pcm[i]) * float64(pcm[i])
	}

	level, code := soundCeiling(pcm)
	require.Empty(t, code)
	require.InDelta(t, -20, level, 0.1)

	plain := -0.691 + 10*math.Log10(sumSquares/float64(len(pcm)))
	require.Greater(t, plain, soundCeilingWhole, "an unweighted ceiling would refuse it: %.1f", plain)
}

// panickingDecoder stands in for a decoder with a bug.
type panickingDecoder struct{ after int }

func (d *panickingDecoder) DecodeToFloat32(_ []byte, out []float32) (int, error) {
	d.after--
	if d.after < 0 {
		panic("index out of range")
	}

	return soundFrameSamples, nil
}

func TestADecoderPanicIsBadSoundNotACrash(t *testing.T) {
	previous := newSoundDecoder
	newSoundDecoder = func() (soundDecoder, error) { return &panickingDecoder{after: 3}, nil }
	t.Cleanup(func() { newSoundDecoder = previous })

	_, code, err := checkSoundPackets(soundFixtures(t, "chromium")["tone1s"])
	require.Equal(t, soundCodeBadSound, code)
	require.ErrorIs(t, err, errDecoderPanic, "caught, and said so, never up the stack")

	samples, err := decodeChecked(&panickingDecoder{}, []byte{0x78}, make([]float32, 960))
	require.Zero(t, samples)
	require.True(t, errors.Is(err, errDecoderPanic))
}

// FuzzDecodeChecked feeds pion's decoder raw packets, in sequence through one
// decoder as an upload's are, each with a two-byte length before it. A panic
// inside the decoder is caught by decodeChecked, but fails the target: it is
// a bug to report upstream. Its seeds, the browsers' packets and a few
// malformed ones, run in every go test; B0 ran it with -fuzz for eleven
// minutes before adoption.
func FuzzDecodeChecked(f *testing.F) {
	for _, browser := range []string{"chromium", "firefox"} {
		fixtures := soundFixtures(f, browser)
		for _, name := range []string{"blip", "quiet"} {
			f.Add(encodeSoundPackets(fixtures[name])[2:])
		}
	}
	f.Add([]byte{})
	f.Add([]byte{0, 1, 0x78})
	f.Add([]byte{0, 3, 0x7B, 0xFF, 0xFF})
	f.Add([]byte{0, 2, 0xFC, 0x00})
	f.Add([]byte{0xFF, 0xFF, 0x78, 0x01})

	f.Fuzz(func(t *testing.T, stream []byte) {
		decoder, err := newSoundDecoder()
		require.NoError(t, err)

		out := make([]float32, 5760)
		for len(stream) >= 2 {
			length := int(binary.BigEndian.Uint16(stream))
			stream = stream[2:]
			if length > len(stream) {
				length = len(stream)
			}

			_, err := decodeChecked(decoder, stream[:length], out)
			if errors.Is(err, errDecoderPanic) {
				t.Fatalf("the decoder panicked on %x", stream[:length])
			}
			stream = stream[length:]
		}
	})
}
