package main

import (
	"encoding/binary"
	"errors"
	"math"

	"github.com/pion/opus"
)

// Checking a sound before it is kept (design/3.2.0.md §7.2, §7.8).
//
// VillaNET's server cannot encode Opus: gumble's codec needs cgo, and the
// server is built without it. So the browser makes the Opus, with WebCodecs at
// a constant 40 kbit/s in 20 ms mono frames, levelled to -20 LUFS by its own
// BS.1770 (loudness.ts), and the server checks every byte of what it is sent
// before it keeps any of it:
//
//   - the packets section's shape: how many packets, how long each;
//   - each packet's TOC byte: one mono frame of 20 ms, exactly what WebCodecs
//     makes, and nothing a listener's decoder would have to work harder to
//     take apart;
//   - that every packet decodes, with github.com/pion/opus, a pure-Go decoder,
//     into 960 samples: real Opus and nothing else;
//   - a loudness ceiling on what it decoded, so a hand-made upload cannot be
//     ear-splitting while an honest one never comes near it.
//
// Every call into the decoder goes through decodeChecked, which turns a panic
// into a refusal. A panic in a handler would end the plugin's process, and
// Mattermost deactivates a plugin that dies three times in an hour, for
// everybody: one bad packet must cost its sender a refusal, never that.

const (
	// The longest sound: 6 s. The encoder's delay adds a packet, so a full
	// 6.00 s trim is 301 packets of 20 ms (W2), and the packet cap is one
	// more than the length alone would say.
	maxSoundMs      = 6000
	soundFrameMs    = 20
	maxSoundPackets = maxSoundMs/soundFrameMs + 1
	minSoundPackets = 13 // 0.25 s, the shortest trim, makes 13

	// Each packet at most 128 bytes: (128 + 32) × 50 × 8 = 64 kbit/s, under
	// the 72 kbit/s an old Murmur configuration allows. 40 kbit/s CBR makes
	// 100.
	maxSoundPacketBytes = 128

	// The packets section, decoded from the upload's base64: 301 × 130 is
	// 39 130.
	maxSoundSectionBytes = 40000

	// Samples per packet at 48 kHz, mono, 20 ms.
	soundFrameSamples = 960

	// The ceiling (§7.8). K-weighted and ungated: the panel levels to -20
	// LUFS gated, and an ungated level is never above the gated one, so an
	// honest sound reads -20 or under as a whole, 6 dB below -14. Gating lets
	// one 400 ms burst in an honest 6 s sound sit about 11.3 dB above its
	// integrated -20, so about -8.7; -5 leaves 3.7 dB. A short blast cannot
	// hide in a quiet sound. And a sound with no 400 ms window above -50 is
	// silence: an honest one has a block within 10 dB of -20.
	soundCeilingWhole  = -14.0
	soundCeilingWindow = -5.0
	soundSilentWindow  = -50.0

	// BS.1770's blocks, and the step between the windows the ceiling looks at.
	soundWindowSamples = 48000 * 400 / 1000
	soundWindowStep    = 48000 * 100 / 1000
)

// The codes an upload is refused with (§7.14 words them).
const (
	soundCodeBadSound = "bad_sound"
	soundCodeTooLoud  = "too_loud"
	soundCodeSilent   = "silent"
)

var errDecoderPanic = errors.New("the Opus decoder panicked")

// soundDecoder is what the check needs of a decoder. pion's, unless a test
// hands in a stand-in.
type soundDecoder interface {
	DecodeToFloat32(in []byte, out []float32) (int, error)
}

// newSoundDecoder makes one decoder per sound, fed its packets in order, as a
// listener's would be. A variable so that a test can make one that panics.
var newSoundDecoder = func() (soundDecoder, error) {
	decoder, err := opus.NewDecoderWithOutput(48000, 1)
	if err != nil {
		return nil, err
	}

	return &decoder, nil
}

// decodeChecked decodes one packet, and is the only way anything here calls
// a decoder: a panic comes back as errDecoderPanic, never up the stack.
func decodeChecked(decoder soundDecoder, packet []byte, out []float32) (samples int, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			samples, err = 0, errDecoderPanic
		}
	}()

	return decoder.DecodeToFloat32(packet, out)
}

// parseSoundPackets reads a packets section: a big-endian u16 count, then
// that many packets, each a big-endian u16 length and its bytes, and nothing
// after them. Every length and count is held to its bound before anything is
// sliced.
func parseSoundPackets(section []byte) ([][]byte, bool) {
	if len(section) < 2 || len(section) > maxSoundSectionBytes {
		return nil, false
	}

	count := int(binary.BigEndian.Uint16(section))
	if count < minSoundPackets || count > maxSoundPackets {
		return nil, false
	}

	packets := make([][]byte, 0, count)
	at := 2
	for range count {
		if at+2 > len(section) {
			return nil, false
		}

		length := int(binary.BigEndian.Uint16(section[at:]))
		at += 2
		if length < 1 || length > maxSoundPacketBytes || at+length > len(section) {
			return nil, false
		}

		packets = append(packets, section[at:at+length])
		at += length
	}

	if at != len(section) {
		return nil, false
	}

	return packets, true
}

// encodeSoundPackets is parseSoundPackets the other way round.
func encodeSoundPackets(packets [][]byte) []byte {
	size := 2
	for _, packet := range packets {
		size += 2 + len(packet)
	}

	section := make([]byte, 0, size)
	section = binary.BigEndian.AppendUint16(section, uint16(len(packets)))
	for _, packet := range packets {
		section = binary.BigEndian.AppendUint16(section, uint16(len(packet)))
		section = append(section, packet...)
	}

	return section
}

// twentyMsConfigs are the Opus configurations whose frames are 20 ms: SILK
// narrow, medium and wide band, Hybrid super-wide and full band, and CELT in
// all four bands (RFC 6716 §3.1).
var twentyMsConfigs = map[byte]bool{1: true, 5: true, 9: true, 13: true, 15: true, 19: true, 23: true, 27: true, 31: true}

// soundTOCOK is the TOC byte's rule: code 0 (one frame), mono, and a 20 ms
// configuration. A duration of 960 samples alone would also let through two
// 10 ms frames, four of 5 ms, eight of 2.5 ms, and stereo, none of which an
// honest panel makes.
func soundTOCOK(packet []byte) bool {
	if len(packet) == 0 {
		return false
	}

	toc := packet[0]

	return toc&3 == 0 && toc&4 == 0 && twentyMsConfigs[toc>>3]
}

// soundCheck is what checking a sound found: how long it is, and its whole
// level, for the admin card.
type soundCheck struct {
	ms    int64
	level float64
}

// checkSoundPackets is every check of §7.8 on the audio: each packet's TOC,
// each packet decoded into 960 samples, then the ceiling. It answers "" or
// the code to refuse with, and errDecoderPanic when the decoder panicked,
// which the caller logs.
func checkSoundPackets(packets [][]byte) (soundCheck, string, error) {
	if len(packets) < minSoundPackets || len(packets) > maxSoundPackets {
		return soundCheck{}, soundCodeBadSound, nil
	}

	for _, packet := range packets {
		if len(packet) < 1 || len(packet) > maxSoundPacketBytes || !soundTOCOK(packet) {
			return soundCheck{}, soundCodeBadSound, nil
		}
	}

	decoder, err := newSoundDecoder()
	if err != nil {
		return soundCheck{}, soundCodeBadSound, nil
	}

	// Room for the longest packet Opus allows, 120 ms, whatever the TOC
	// said: a decoder that believed something else must not write past the
	// end.
	out := make([]float32, 5760)
	pcm := make([]float32, 0, len(packets)*soundFrameSamples)

	for _, packet := range packets {
		samples, err := decodeChecked(decoder, packet, out)
		if errors.Is(err, errDecoderPanic) {
			return soundCheck{}, soundCodeBadSound, err
		}
		if err != nil || samples != soundFrameSamples {
			return soundCheck{}, soundCodeBadSound, nil
		}

		pcm = append(pcm, out[:samples]...)
	}

	level, code := soundCeiling(pcm)
	if code != "" {
		return soundCheck{}, code, nil
	}

	return soundCheck{ms: int64(len(packets) * soundFrameMs), level: level}, "", nil
}

// soundCeiling holds decoded audio to the ceiling (§7.8): silent with no
// 400 ms window above -50, too loud above -14 as a whole or -5 in any window.
// It answers the whole-sound level, to a tenth, and "" or the code.
func soundCeiling(pcm []float32) (float64, string) {
	whole, loudest := soundLevels(pcm)

	switch {
	case loudest <= soundSilentWindow:
		return 0, soundCodeSilent
	case whole > soundCeilingWhole || loudest > soundCeilingWindow:
		return 0, soundCodeTooLoud
	}

	return math.Round(whole*10) / 10, ""
}

// biquad is one second-order filter section, in direct form I.
type biquad struct {
	b0, b1, b2, a1, a2 float64
	x1, x2, y1, y2     float64
}

func (f *biquad) step(x float64) float64 {
	y := f.b0*x + f.b1*f.x1 + f.b2*f.x2 - f.a1*f.y1 - f.a2*f.y2
	f.x2, f.x1 = f.x1, x
	f.y2, f.y1 = f.y1, y

	return y
}

// kWeighted is the signal through BS.1770-4's K-weighting at 48 kHz: its high
// shelf, then its high-pass, with the standard's own coefficients. Only the
// filters: no gating and no blocks, which are the browser's (loudness.ts).
// Without them, plain RMS would read bass far louder than the browser
// measured it — 5.6 dB at 40 Hz, 8.3 at 30 and 13.3 at 20 — so an honestly
// levelled bass-heavy sound could fail the ceiling.
func kWeighted(pcm []float32) []float64 {
	shelf := biquad{b0: 1.53512485958697, b1: -2.69169618940638, b2: 1.19839281085285, a1: -1.69065929318241, a2: 0.73248077421585}
	highPass := biquad{b0: 1.0, b1: -2.0, b2: 1.0, a1: -1.99004745483398, a2: 0.99007225036621}

	weighted := make([]float64, len(pcm))
	for i, sample := range pcm {
		weighted[i] = highPass.step(shelf.step(float64(sample)))
	}

	return weighted
}

// loudnessOf is BS.1770's loudness of a mean square: -0.691 + 10 log10(z),
// and -inf for silence.
func loudnessOf(meanSquare float64) float64 {
	if meanSquare <= 0 {
		return math.Inf(-1)
	}

	return -0.691 + 10*math.Log10(meanSquare)
}

// soundLevels is a sound's K-weighted, ungated level as a whole, and that of
// its loudest 400 ms window, the windows stepping by 100 ms. A sound shorter
// than a window is measured as one window padded with silence.
func soundLevels(pcm []float32) (float64, float64) {
	if len(pcm) == 0 {
		return math.Inf(-1), math.Inf(-1)
	}

	weighted := kWeighted(pcm)

	// Running sums of squares, so each window is two lookups.
	sums := make([]float64, len(weighted)+1)
	for i, value := range weighted {
		sums[i+1] = sums[i] + value*value
	}

	whole := loudnessOf(sums[len(weighted)] / float64(len(weighted)))

	if len(weighted) < soundWindowSamples {
		return whole, loudnessOf(sums[len(weighted)] / float64(soundWindowSamples))
	}

	loudest := math.Inf(-1)
	for start := 0; start+soundWindowSamples <= len(weighted); start += soundWindowStep {
		level := loudnessOf((sums[start+soundWindowSamples] - sums[start]) / float64(soundWindowSamples))
		loudest = math.Max(loudest, level)
	}

	return whole, loudest
}
