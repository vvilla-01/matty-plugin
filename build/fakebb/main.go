// fakebb is a stand-in for Broadcast Box, for developing VillaNET and its
// clients (the web panel, Matty) on one machine with nothing else running.
//
// It answers the three things the plugin asks of Broadcast Box:
//
//	GET  /api/status         the streams that are live, and who is watching
//	GET  /api/whip/profile   the stream a bearer token belongs to (and POST, to save its title)
//	POST /api/whep           a real WebRTC playback session
//
// and the playback is real: every viewer is sent the same looping clip (a
// test card and a tone, made once with ffmpeg; see the Makefile's
// fakebb-media), so a player can be watched connecting, playing, being muted
// and losing its stream. Streams are switched on and off while it runs:
//
//	curl -X POST 'localhost:7788/fake/live?key=alice&on=false'
//	curl -X POST 'localhost:7788/fake/live?key=alice&silent=true'
//	curl -X POST 'localhost:7788/fake/live?key=alice&paused=true'
//
// The first takes the stream off the air; the second leaves it listed but
// stops its media (a broadcast that died without saying so); the third is a
// streamer who paused: listed as paused, sending nothing, and expected back on
// the same connection. In each case, like Broadcast Box, the viewers stay
// connected.
//
// Each stream's picture is in one codec, as on a real Broadcast Box, where it
// is whatever the streamer's encoder sends: H.264 (-h264), what OBS sends and
// the only video the plugin's clip recorder records, so a stream in H.264 is
// one that clips can be cut from; or VP8 (-video), which a real Broadcast Box
// never carries but an emulator can always play. -codecs says which streams
// start in H.264 (victor's, by default; every other stream is VP8), and a
// stream's codec is changed while it runs, its viewers staying connected:
//
//	curl -X POST 'localhost:7788/fake/codec?key=alice&codec=h264'
//
// Viewers are answered with Broadcast Box's codecs as far as this repository
// records them (registerBroadcastBoxCodecs). A viewer whose offer lacks the
// stream's codec is answered with a session and its sound but no picture,
// which is what Broadcast Box's source was read to do and has not been tried
// against it; the stand-in logs each such session ("no picture for viewer").
//
// Beside it run a stand-in for Steam and a stand-in UnifiedPush push server
// (which the app's stand-in distributor hands out endpoints at and collects
// from), on the same port, and a stand-in Mumble server, on Mumble's own port
// (build/fakebb/standin says what each answers and how it is steered):
//
//	curl -X POST 'localhost:7788/fake/steam?who=alice&game=Hades'
//	curl -X POST 'localhost:7788/fake/push?status=410'
//	curl -X POST 'localhost:7788/fake/voice?name=bob&muted=true'
//
// The plugin is pointed at the Mumble one by its own setting, and calls the
// push server once localhost:7788 is one of its listed push servers. Steam's hosts
// are not a setting, so only a plugin built with the villanet_standin tag
// (make standin-linux) asks this Steam; a release never does.
//
// Never part of a release: nothing under build/ is in the plugin bundle.
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/pion/webrtc/v4/pkg/media/oggreader"

	"github.com/vvilla-01/matty-plugin/build/fakebb/standin"
)

type stream struct {
	Key    string
	MOTD   string
	Live   bool
	Paused bool
	// Silent is a stream that is still listed but has stopped sending: what a
	// player sees when a broadcast dies without anybody saying so.
	Silent  bool
	Started time.Time
	// Codec is the codec its picture is sent in: codecVP8 or codecH264.
	Codec string
}

type session struct {
	id    string
	key   string
	pc    *webrtc.PeerConnection
	video *videoTrack
}

type box struct {
	mu       sync.Mutex
	streams  map[string]*stream
	sessions map[string]*session
	video    string
	h264     string
	audio    string
	api      *webrtc.API
	// inH264 is the streams that are in H.264 when they are first heard of.
	inH264 map[string]bool
	// open opens a codec's clip from its start; openVideo unless a test
	// says otherwise.
	open func(codec string) (frameSource, error)
}

func main() {
	addr := flag.String("addr", "127.0.0.1:7788", "where to listen")
	video := flag.String("video", "", "an IVF file with VP8 video, looped to every viewer of a stream in VP8")
	h264 := flag.String("h264", "", "a raw H.264 file (Annex B, 30 frames a second), looped to every viewer of a stream in H.264")
	inH264 := flag.String("codecs", "victor=h264", "streams whose picture is H.264 rather than VP8: key=h264, comma separated")
	audio := flag.String("audio", "", "an Ogg file with Opus audio, looped to every viewer")
	live := flag.String("streams", "victor=Building Matty live,alice=Ranked grind,chillstep=Lo-fi beats all night", "streams that start live: key=title, comma separated")
	steam := flag.String("steam", "victor=Factorio,alice=Hades,bob,carol", "Steam accounts there are from the start: name, or name=game for somebody in a game, comma separated")
	mumble := flag.String("mumble", "127.0.0.1:64738", "where the stand-in Mumble server listens; empty for none")
	voice := flag.String("voice", "bob,carol=muted,Guest", "who is in voice from the start: name, name=muted or name=deafened, comma separated")
	flag.Parse()

	api, err := newBoxAPI()
	if err != nil {
		log.Fatal(err)
	}
	b := &box{
		streams:  map[string]*stream{},
		sessions: map[string]*session{},
		video:    *video,
		h264:     *h264,
		audio:    *audio,
		api:      api,
		inH264:   map[string]bool{},
	}
	for _, entry := range list(*inH264) {
		key, codec, _ := strings.Cut(entry, "=")
		if codec != codecH264 {
			log.Fatalf("-codecs %q: a stream is given as key=h264", entry)
		}
		b.inH264[key] = true
	}
	for _, entry := range strings.Split(*live, ",") {
		key, title, _ := strings.Cut(strings.TrimSpace(entry), "=")
		if key != "" {
			b.streams[key] = b.newStream(key, title)
			b.streams[key].Live = true
			b.streams[key].Started = time.Now().Add(-37 * time.Minute)
		}
	}

	mux := b.routes(list(*steam))
	log.Printf("stand-in Steam on http://%s (accounts: %s)", *addr, *steam)
	log.Printf("stand-in push server on http://%s/push/", *addr)

	if *mumble != "" {
		server, err := standin.NewMumble(list(*voice)...)
		if err != nil {
			log.Fatal(err)
		}
		server.Routes(mux)

		// Not fatal: a real Mumble server may already have the port, and the
		// rest is still worth running.
		if listener, listenErr := net.Listen("tcp", *mumble); listenErr != nil {
			log.Printf("no stand-in Mumble: %v", listenErr)
		} else {
			log.Printf("stand-in Mumble on %s (in voice: %s)", *mumble, *voice)
			go func() { log.Printf("stand-in Mumble stopped: %v", server.Serve(listener)) }()
		}
	}

	log.Printf("fake Broadcast Box on http://%s (VP8 %q, H.264 %q for %s, audio %q)", *addr, *video, *h264, *inH264, *audio)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

// routes is everything served on the one port: Broadcast Box, the steering
// of its streams, the stand-in Steam with these accounts and the stand-in push
// server.
func (b *box) routes(steamAccounts []string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", b.status)
	mux.HandleFunc("/api/whip/profile", b.profile)
	mux.HandleFunc("/api/whep", b.whep)
	mux.HandleFunc("/fake/live", b.setLive)
	mux.HandleFunc("/fake/codec", b.setCodec)

	standin.NewSteam(steamAccounts...).Routes(mux)
	standin.NewPush().Routes(mux)

	return mux
}

// newBoxAPI is what every viewer's session is made from: Broadcast Box's
// codecs, and VP8.
func newBoxAPI() (*webrtc.API, error) {
	engine := &webrtc.MediaEngine{}
	if err := registerBroadcastBoxCodecs(engine); err != nil {
		return nil, err
	}
	settings := webrtc.SettingEngine{}
	// So a browser or an emulator on this same machine can reach it.
	settings.SetIncludeLoopbackCandidate(true)

	return webrtc.NewAPI(webrtc.WithMediaEngine(engine), webrtc.WithSettingEngine(settings)), nil
}

// list is a comma-separated flag's entries, without the empty ones.
func list(flag string) []string {
	var entries []string
	for _, entry := range strings.Split(flag, ",") {
		if entry = strings.TrimSpace(entry); entry != "" {
			entries = append(entries, entry)
		}
	}

	return entries
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func (b *box) status(w http.ResponseWriter, _ *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := []map[string]any{}
	keys := make([]string, 0, len(b.streams))
	for key := range b.streams {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		s := b.streams[key]
		if !s.Live {
			continue
		}
		watching := []map[string]any{}
		for _, v := range b.sessions {
			if v.key == key {
				watching = append(watching, map[string]any{"id": v.id, "videoBitrate": 1500000})
			}
		}
		out = append(out, map[string]any{
			"streamKey":   s.Key,
			"isPublic":    true,
			"motd":        s.MOTD,
			"streamStart": s.Started.UTC().Format(time.RFC3339),
			"audioTracks": []map[string]any{{"rid": "", "packetsReceived": 1000}},
			"videoTracks": []map[string]any{{"rid": "", "bitrate": 1500000, "packetsReceived": 1000, "lastKeyframe": time.Now().UTC().Format(time.RFC3339)}},
			"sessions":    watching,
			"paused":      s.Paused,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// keyOf finds the stream a bearer token is for: the key in base64, which is
// how the plugin sends a token to the profile endpoints, or the key itself.
// Any token is given a stream of its own name, so any token unlocks, as long
// as it is the same one.
//
// The base64 is taken off whenever what is under it could be a stream's name,
// whether or not there is such a stream yet. It used to be taken off only for
// a stream already known, so somebody linking with a new name was given a
// stream called after the base64 instead ("Ym9i" for "bob"), or, where that
// ended in "=", a name the plugin rightly refuses.
func (b *box) keyOf(r *http.Request) string {
	return keyUnder(strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")))
}

func keyUnder(token string) string {
	if raw, err := base64.StdEncoding.DecodeString(token); err == nil && streamName(string(raw)) {
		return string(raw)
	}

	return token
}

// streamName is whether text could be a stream's name, as Broadcast Box makes
// them: letters, digits, dashes, dots and underscores.
func streamName(text string) bool {
	if text == "" || len(text) > 64 {
		return false
	}
	for _, r := range text {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' && r != '.' {
			return false
		}
	}

	return true
}

func (b *box) profile(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()

	key := b.keyOf(r)
	if key == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no token"})
		return
	}
	s, known := b.streams[key]
	if !known {
		s = b.newStream(key, "")
		b.streams[key] = s
	}
	if r.Method == http.MethodPost {
		var body struct {
			MOTD     string `json:"motd"`
			IsPublic bool   `json:"isPublic"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body)
		s.MOTD = body.MOTD
	}
	writeJSON(w, http.StatusOK, map[string]any{"motd": s.MOTD, "isPublic": true, "isActive": s.Live, "streamKey": s.Key})
}

func (b *box) setLive(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()

	key := r.URL.Query().Get("key")
	s, known := b.streams[key]
	if !known {
		s = b.newStream(key, r.URL.Query().Get("motd"))
		b.streams[key] = s
	}
	wasLive := s.Live
	s.Live = r.URL.Query().Get("on") != "false"
	s.Silent = r.URL.Query().Get("silent") == "true"
	s.Paused = r.URL.Query().Get("paused") == "true"
	if s.Live && !wasLive {
		s.Started = time.Now()
	}
	if motd := r.URL.Query().Get("motd"); motd != "" {
		s.MOTD = motd
	}
	writeJSON(w, http.StatusOK, map[string]any{"streamKey": key, "live": s.Live, "silent": s.Silent, "paused": s.Paused})
}

// newStream is a stream first heard of, in the codec -codecs gives it.
func (b *box) newStream(key, title string) *stream {
	codec := codecVP8
	if b.inH264[key] {
		codec = codecH264
	}

	return &stream{Key: key, MOTD: title, Codec: codec}
}

// setCodec changes the codec a stream's picture is sent in, as a streamer who
// changes encoder does; its viewers stay connected and carry on in the new
// one, or, where they did not offer it, with no picture. Only the codec
// changes: whether the stream is live, paused or silent stays as it was.
func (b *box) setCodec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	key, codec := r.URL.Query().Get("key"), r.URL.Query().Get("codec")
	if key == "" || (codec != codecVP8 && codec != codecH264) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "say which stream (key=) and which codec (codec=vp8 or codec=h264)"})
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	s, known := b.streams[key]
	if !known {
		s = b.newStream(key, "")
		b.streams[key] = s
	}
	s.Codec = codec
	log.Printf("stream %q is now in %s", key, codec)
	writeJSON(w, http.StatusOK, map[string]any{"streamKey": key, "codec": s.Codec, "live": s.Live})
}

// codecOfStream is the codec a stream's picture is in right now.
func (b *box) codecOfStream(key string) string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if s, known := b.streams[key]; known {
		return s.Codec
	}

	return codecVP8
}

func (b *box) isLive(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	s, known := b.streams[key]
	return known && s.Live
}

// sending is whether a stream's media should be going out right now.
func (b *box) sending(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	s, known := b.streams[key]
	return known && s.Live && !s.Silent && !s.Paused
}

func (b *box) whep(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	key := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if !b.isLive(key) {
		http.Error(w, "no such stream", http.StatusNotFound)
		return
	}
	offer, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "unreadable offer", http.StatusBadRequest)
		return
	}

	pc, err := b.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Named after the stream key, as Broadcast Box does: the plugin has to take that out.
	audioTrack, _ := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus}, "audio", key)
	videoTrack := newVideoTrack(key)
	videoTrack.offered = offerVideoCodecs(string(offer))
	for _, track := range []webrtc.TrackLocal{audioTrack, videoTrack} {
		sender, addErr := pc.AddTrack(track)
		if addErr != nil {
			_ = pc.Close()
			http.Error(w, addErr.Error(), http.StatusInternalServerError)
			return
		}
		go func() {
			buf := make([]byte, 1500)
			for {
				if _, _, readErr := sender.Read(buf); readErr != nil {
					return
				}
			}
		}()
	}

	if err = pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: string(offer)}); err != nil {
		_ = pc.Close()
		http.Error(w, "bad offer: "+err.Error(), http.StatusBadRequest)
		return
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		_ = pc.Close()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err = pc.SetLocalDescription(answer); err != nil {
		_ = pc.Close()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	<-gathered

	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	id := fmt.Sprintf("%s-%s-%s-%s-%s", hex.EncodeToString(raw[0:4]), hex.EncodeToString(raw[4:6]), hex.EncodeToString(raw[6:8]), hex.EncodeToString(raw[8:10]), hex.EncodeToString(raw[10:16]))
	s := &session{id: id, key: key, pc: pc, video: videoTrack}
	b.mu.Lock()
	b.sessions[id] = s
	b.mu.Unlock()

	done := make(chan struct{})
	var once sync.Once
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("viewer %s of %q: %s", id[:8], key, state)
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed || state == webrtc.PeerConnectionStateDisconnected {
			once.Do(func() {
				close(done)
				b.mu.Lock()
				delete(b.sessions, id)
				b.mu.Unlock()
				_ = pc.Close()
			})
		}
	})
	go b.playVideo(key, id[:8], videoTrack, done)
	go b.playAudio(key, audioTrack, done)

	w.Header().Set("Content-Type", "application/sdp")
	w.Header().Set("Location", "/api/whep/"+id)
	w.WriteHeader(http.StatusCreated)
	_, _ = io.WriteString(w, pc.LocalDescription().SDP)
}

// playVideo sends the clip's frames at their own pace, round and round, for
// as long as the stream is live and the viewer is there, in the codec the
// stream is in. When that changes, the other codec's clip starts from its
// beginning, which is a keyframe. A clip that cannot be opened or read is
// said once, and tried again once a second until it can, or the codec
// changes.
func (b *box) playVideo(key, viewer string, track *videoTrack, done <-chan struct{}) {
	open := b.open
	if open == nil {
		open = b.openVideo
	}
	var (
		source   frameSource
		codec    string
		failed   string
		failedAt time.Time
		told     string
	)
	defer func() {
		if source != nil {
			source.close()
		}
	}()
	fail := func(err error) {
		if failed != codec {
			log.Printf("video: %v", err)
		}
		failed, failedAt = codec, time.Now()
	}
	ticker := time.NewTicker(time.Second / 30)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
		}
		want := b.codecOfStream(key)
		if want != told && track.isBound() {
			told = want
			if _, offered := track.payloadType(want); !offered {
				log.Printf("no picture for viewer %s of %q: the stream is in %s and the offer had %s", viewer, key, want, track.offered)
			}
		}
		if source == nil || want != codec {
			if source != nil {
				source.close()
				source = nil
			}
			if want == codec && failed == codec && time.Since(failedAt) < time.Second {
				continue
			}
			codec = want
			opened, err := open(codec)
			if err != nil {
				fail(err)
				continue
			}
			source = opened
			ticker.Reset(source.frame())
		}
		if !b.sending(key) {
			continue
		}
		data, readErr := source.next()
		if readErr != nil {
			source.close()
			source = nil
			if !errors.Is(readErr, io.EOF) {
				fail(readErr)
			}
			continue
		}
		failed = ""
		_ = track.write(codec, data, source.frame())
	}
}

// openVideo opens the clip for a codec from its start.
func (b *box) openVideo(codec string) (frameSource, error) {
	switch codec {
	case codecH264:
		if b.h264 == "" {
			return nil, errors.New("no H.264 clip (-h264) for a stream in H.264")
		}
		return openH264(b.h264)
	default:
		if b.video == "" {
			return nil, errors.New("no VP8 clip (-video) for a stream in VP8")
		}
		return openIVF(b.video)
	}
}

func (b *box) playAudio(key string, track *webrtc.TrackLocalStaticSample, done <-chan struct{}) {
	if b.audio == "" {
		return
	}
	const page = 20 * time.Millisecond
	for {
		file, err := os.Open(b.audio)
		if err != nil {
			log.Printf("audio: %v", err)
			return
		}
		ogg, _, err := oggreader.NewWith(file)
		if err != nil {
			log.Printf("audio: %v", err)
			_ = file.Close()
			return
		}
		var last uint64
		ticker := time.NewTicker(page)
		for {
			select {
			case <-done:
				ticker.Stop()
				_ = file.Close()
				return
			case <-ticker.C:
			}
			if !b.sending(key) {
				continue
			}
			data, length, readErr := nextSound(ogg, &last, page)
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				log.Printf("audio: %v", readErr)
				break
			}
			_ = track.WriteSample(media.Sample{Data: data, Duration: length})
		}
		ticker.Stop()
		_ = file.Close()
	}
}

// nextSound is the next page of sound in an Ogg Opus file and how long it
// lasts (by its granule position after the last one; fallback when that says
// nothing sensible). Opus's two header pages are passed over: oggreader reads
// the first (OpusHead) itself, and the second (OpusTags, the encoder's name)
// used to be sent as if it were a packet of sound, once a loop, which no
// decoder can read: a clip recorded from the stand-in had a broken packet
// every 12 seconds, and the app would not play it.
func nextSound(ogg *oggreader.OggReader, last *uint64, fallback time.Duration) ([]byte, time.Duration, error) {
	for {
		data, header, err := ogg.ParseNextPage()
		if err != nil {
			return nil, 0, err
		}
		if opusHeaderPage(data) {
			continue
		}
		samples := header.GranulePosition - *last
		*last = header.GranulePosition
		length := time.Duration(samples) * time.Second / 48000
		if length <= 0 || length > time.Second {
			length = fallback
		}

		return data, length, nil
	}
}

// opusHeaderPage is whether an Ogg page holds one of Opus's two headers
// rather than sound.
func opusHeaderPage(data []byte) bool {
	return bytes.HasPrefix(data, []byte("OpusHead")) || bytes.HasPrefix(data, []byte("OpusTags"))
}
