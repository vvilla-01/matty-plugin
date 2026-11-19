package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// viewerOffer is a viewer's offer as both clients make one (the web panel's
// whep.ts and Matty's WhepPlayer): audio, then video, each to receive only.
// Cut down to the lines the relay reads; Broadcast Box is a fake in every
// test that sends it.
const viewerOffer = "v=0\r\n" +
	"o=- 4611731400430051336 2 IN IP4 127.0.0.1\r\n" +
	"s=-\r\n" +
	"t=0 0\r\n" +
	"a=group:BUNDLE 0 1\r\n" +
	"m=audio 9 UDP/TLS/RTP/SAVPF 111\r\n" +
	"c=IN IP4 0.0.0.0\r\n" +
	"a=mid:0\r\n" +
	"a=recvonly\r\n" +
	"a=rtpmap:111 opus/48000/2\r\n" +
	"m=video 9 UDP/TLS/RTP/SAVPF 96\r\n" +
	"c=IN IP4 0.0.0.0\r\n" +
	"a=mid:1\r\n" +
	"a=recvonly\r\n" +
	"a=rtpmap:96 H264/90000\r\n"

// The offers the clients really send, as their WebRTC stacks write them
// (written out by hand from what each one makes, the ICE and DTLS values
// made up): Matty's, made by libwebrtc for WhepPlayer.kt, and the web panel's
// (whep.ts) as Chrome and as Firefox word it. Each asks for audio, then
// video, each to receive only, and the panel's carries the stereo=1 it adds.
const (
	mattyOffer = "v=0\r\n" +
		"o=- 8429112634092752811 2 IN IP4 127.0.0.1\r\n" +
		"s=-\r\n" +
		"t=0 0\r\n" +
		"a=group:BUNDLE 0 1\r\n" +
		"a=extmap-allow-mixed\r\n" +
		"a=msid-semantic: WMS\r\n" +
		"m=audio 9 UDP/TLS/RTP/SAVPF 111 63 9 102 0 8 13 110 126\r\n" +
		"c=IN IP4 0.0.0.0\r\n" +
		"a=rtcp:9 IN IP4 0.0.0.0\r\n" +
		"a=ice-ufrag:Qx3b\r\n" +
		"a=ice-pwd:0cX1vB7m2xYqL4sR9tE6uP8w\r\n" +
		"a=ice-options:trickle renomination\r\n" +
		"a=fingerprint:sha-256 4A:1F:8C:22:9B:E0:7D:51:3C:AA:16:F4:08:6E:B2:95:D3:47:0A:C9:5E:21:7F:88:B6:03:E4:9D:52:1A:C7:60\r\n" +
		"a=setup:actpass\r\n" +
		"a=mid:0\r\n" +
		"a=extmap:1 urn:ietf:params:rtp-hdrext:ssrc-audio-level\r\n" +
		"a=extmap:2 http://www.webrtc.org/experiments/rtp-hdrext/abs-send-time\r\n" +
		"a=extmap:3 http://www.ietf.org/id/draft-holmer-rmcat-transport-wide-cc-extensions-01\r\n" +
		"a=extmap:4 urn:ietf:params:rtp-hdrext:sdes:mid\r\n" +
		"a=recvonly\r\n" +
		"a=rtcp-mux\r\n" +
		"a=rtpmap:111 opus/48000/2\r\n" +
		"a=rtcp-fb:111 transport-cc\r\n" +
		"a=fmtp:111 minptime=10;useinbandfec=1;stereo=1\r\n" +
		"a=rtpmap:63 red/48000/2\r\n" +
		"a=fmtp:63 111/111\r\n" +
		"a=rtpmap:9 G722/8000\r\n" +
		"a=rtpmap:102 ILBC/8000\r\n" +
		"a=rtpmap:0 PCMU/8000\r\n" +
		"a=rtpmap:8 PCMA/8000\r\n" +
		"a=rtpmap:13 CN/8000\r\n" +
		"a=rtpmap:110 telephone-event/48000\r\n" +
		"a=rtpmap:126 telephone-event/8000\r\n" +
		"m=video 9 UDP/TLS/RTP/SAVPF 96 97 98 99 35 36 100 101 127\r\n" +
		"c=IN IP4 0.0.0.0\r\n" +
		"a=rtcp:9 IN IP4 0.0.0.0\r\n" +
		"a=ice-ufrag:Qx3b\r\n" +
		"a=ice-pwd:0cX1vB7m2xYqL4sR9tE6uP8w\r\n" +
		"a=ice-options:trickle renomination\r\n" +
		"a=fingerprint:sha-256 4A:1F:8C:22:9B:E0:7D:51:3C:AA:16:F4:08:6E:B2:95:D3:47:0A:C9:5E:21:7F:88:B6:03:E4:9D:52:1A:C7:60\r\n" +
		"a=setup:actpass\r\n" +
		"a=mid:1\r\n" +
		"a=extmap:14 urn:ietf:params:rtp-hdrext:toffset\r\n" +
		"a=extmap:2 http://www.webrtc.org/experiments/rtp-hdrext/abs-send-time\r\n" +
		"a=extmap:13 urn:3gpp:video-orientation\r\n" +
		"a=extmap:3 http://www.ietf.org/id/draft-holmer-rmcat-transport-wide-cc-extensions-01\r\n" +
		"a=extmap:4 urn:ietf:params:rtp-hdrext:sdes:mid\r\n" +
		"a=recvonly\r\n" +
		"a=rtcp-mux\r\n" +
		"a=rtcp-rsize\r\n" +
		"a=rtpmap:96 VP8/90000\r\n" +
		"a=rtcp-fb:96 goog-remb\r\n" +
		"a=rtcp-fb:96 transport-cc\r\n" +
		"a=rtcp-fb:96 ccm fir\r\n" +
		"a=rtcp-fb:96 nack\r\n" +
		"a=rtcp-fb:96 nack pli\r\n" +
		"a=rtpmap:97 rtx/90000\r\n" +
		"a=fmtp:97 apt=96\r\n" +
		"a=rtpmap:98 VP9/90000\r\n" +
		"a=fmtp:98 profile-id=0\r\n" +
		"a=rtpmap:99 rtx/90000\r\n" +
		"a=fmtp:99 apt=98\r\n" +
		"a=rtpmap:35 AV1/90000\r\n" +
		"a=rtpmap:36 rtx/90000\r\n" +
		"a=fmtp:36 apt=35\r\n" +
		"a=rtpmap:100 H264/90000\r\n" +
		"a=fmtp:100 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f\r\n" +
		"a=rtpmap:101 rtx/90000\r\n" +
		"a=fmtp:101 apt=100\r\n" +
		"a=rtpmap:127 ulpfec/90000\r\n"

	chromeOffer = "v=0\r\n" +
		"o=- 2928406181207394812 2 IN IP4 127.0.0.1\r\n" +
		"s=-\r\n" +
		"t=0 0\r\n" +
		"a=group:BUNDLE 0 1\r\n" +
		"a=extmap-allow-mixed\r\n" +
		"a=msid-semantic: WMS\r\n" +
		"m=audio 9 UDP/TLS/RTP/SAVPF 111 63 9 0 8 13 110 126\r\n" +
		"c=IN IP4 0.0.0.0\r\n" +
		"a=rtcp:9 IN IP4 0.0.0.0\r\n" +
		"a=ice-ufrag:pT9e\r\n" +
		"a=ice-pwd:Hn2kW5vQz8rY1mC4xJ7aL0sD\r\n" +
		"a=ice-options:trickle\r\n" +
		"a=fingerprint:sha-256 0B:93:C4:7E:1A:58:F2:6D:A0:3B:E9:44:7C:12:85:DF:6A:B1:0E:39:C7:52:8F:24:E6:9A:31:7D:B8:05:4C:F0\r\n" +
		"a=setup:actpass\r\n" +
		"a=mid:0\r\n" +
		"a=extmap:1 urn:ietf:params:rtp-hdrext:ssrc-audio-level\r\n" +
		"a=extmap:2 http://www.webrtc.org/experiments/rtp-hdrext/abs-send-time\r\n" +
		"a=extmap:3 http://www.ietf.org/id/draft-holmer-rmcat-transport-wide-cc-extensions-01\r\n" +
		"a=extmap:4 urn:ietf:params:rtp-hdrext:sdes:mid\r\n" +
		"a=recvonly\r\n" +
		"a=rtcp-mux\r\n" +
		"a=rtcp-rsize\r\n" +
		"a=rtpmap:111 opus/48000/2\r\n" +
		"a=rtcp-fb:111 transport-cc\r\n" +
		"a=fmtp:111 minptime=10;useinbandfec=1;stereo=1\r\n" +
		"a=rtpmap:63 red/48000/2\r\n" +
		"a=fmtp:63 111/111\r\n" +
		"a=rtpmap:9 G722/8000\r\n" +
		"a=rtpmap:0 PCMU/8000\r\n" +
		"a=rtpmap:8 PCMA/8000\r\n" +
		"a=rtpmap:13 CN/8000\r\n" +
		"a=rtpmap:110 telephone-event/48000\r\n" +
		"a=rtpmap:126 telephone-event/8000\r\n" +
		"m=video 9 UDP/TLS/RTP/SAVPF 96 97 103 104 107 108 109 114 115 116 117 118 39 40 45 46 98 99 100 101 119 120 49 50 123 124 125\r\n" +
		"c=IN IP4 0.0.0.0\r\n" +
		"a=rtcp:9 IN IP4 0.0.0.0\r\n" +
		"a=ice-ufrag:pT9e\r\n" +
		"a=ice-pwd:Hn2kW5vQz8rY1mC4xJ7aL0sD\r\n" +
		"a=ice-options:trickle\r\n" +
		"a=fingerprint:sha-256 0B:93:C4:7E:1A:58:F2:6D:A0:3B:E9:44:7C:12:85:DF:6A:B1:0E:39:C7:52:8F:24:E6:9A:31:7D:B8:05:4C:F0\r\n" +
		"a=setup:actpass\r\n" +
		"a=mid:1\r\n" +
		"a=extmap:14 urn:ietf:params:rtp-hdrext:toffset\r\n" +
		"a=extmap:2 http://www.webrtc.org/experiments/rtp-hdrext/abs-send-time\r\n" +
		"a=extmap:13 urn:3gpp:video-orientation\r\n" +
		"a=extmap:3 http://www.ietf.org/id/draft-holmer-rmcat-transport-wide-cc-extensions-01\r\n" +
		"a=extmap:5 http://www.webrtc.org/experiments/rtp-hdrext/playout-delay\r\n" +
		"a=extmap:6 http://www.webrtc.org/experiments/rtp-hdrext/video-content-type\r\n" +
		"a=extmap:7 http://www.webrtc.org/experiments/rtp-hdrext/video-timing\r\n" +
		"a=extmap:8 http://www.webrtc.org/experiments/rtp-hdrext/color-space\r\n" +
		"a=extmap:4 urn:ietf:params:rtp-hdrext:sdes:mid\r\n" +
		"a=extmap:10 urn:ietf:params:rtp-hdrext:sdes:rtp-stream-id\r\n" +
		"a=extmap:11 urn:ietf:params:rtp-hdrext:sdes:repaired-rtp-stream-id\r\n" +
		"a=recvonly\r\n" +
		"a=rtcp-mux\r\n" +
		"a=rtcp-rsize\r\n" +
		"a=rtpmap:96 VP8/90000\r\n" +
		"a=rtcp-fb:96 goog-remb\r\n" +
		"a=rtcp-fb:96 transport-cc\r\n" +
		"a=rtcp-fb:96 ccm fir\r\n" +
		"a=rtcp-fb:96 nack\r\n" +
		"a=rtcp-fb:96 nack pli\r\n" +
		"a=rtpmap:97 rtx/90000\r\n" +
		"a=fmtp:97 apt=96\r\n" +
		"a=rtpmap:103 H264/90000\r\n" +
		"a=rtcp-fb:103 goog-remb\r\n" +
		"a=rtcp-fb:103 transport-cc\r\n" +
		"a=rtcp-fb:103 ccm fir\r\n" +
		"a=rtcp-fb:103 nack\r\n" +
		"a=rtcp-fb:103 nack pli\r\n" +
		"a=fmtp:103 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f\r\n" +
		"a=rtpmap:104 rtx/90000\r\n" +
		"a=fmtp:104 apt=103\r\n" +
		"a=rtpmap:107 H264/90000\r\n" +
		"a=fmtp:107 level-asymmetry-allowed=1;packetization-mode=0;profile-level-id=42001f\r\n" +
		"a=rtpmap:108 rtx/90000\r\n" +
		"a=fmtp:108 apt=107\r\n" +
		"a=rtpmap:109 H264/90000\r\n" +
		"a=fmtp:109 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f\r\n" +
		"a=rtpmap:114 rtx/90000\r\n" +
		"a=fmtp:114 apt=109\r\n" +
		"a=rtpmap:115 H264/90000\r\n" +
		"a=fmtp:115 level-asymmetry-allowed=1;packetization-mode=0;profile-level-id=42e01f\r\n" +
		"a=rtpmap:116 rtx/90000\r\n" +
		"a=fmtp:116 apt=115\r\n" +
		"a=rtpmap:117 H264/90000\r\n" +
		"a=fmtp:117 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=4d001f\r\n" +
		"a=rtpmap:118 rtx/90000\r\n" +
		"a=fmtp:118 apt=117\r\n" +
		"a=rtpmap:39 H264/90000\r\n" +
		"a=fmtp:39 level-asymmetry-allowed=1;packetization-mode=0;profile-level-id=4d001f\r\n" +
		"a=rtpmap:40 rtx/90000\r\n" +
		"a=fmtp:40 apt=39\r\n" +
		"a=rtpmap:45 AV1/90000\r\n" +
		"a=fmtp:45 level-idx=5;profile=0;tier=0\r\n" +
		"a=rtpmap:46 rtx/90000\r\n" +
		"a=fmtp:46 apt=45\r\n" +
		"a=rtpmap:98 VP9/90000\r\n" +
		"a=fmtp:98 profile-id=0\r\n" +
		"a=rtpmap:99 rtx/90000\r\n" +
		"a=fmtp:99 apt=98\r\n" +
		"a=rtpmap:100 VP9/90000\r\n" +
		"a=fmtp:100 profile-id=2\r\n" +
		"a=rtpmap:101 rtx/90000\r\n" +
		"a=fmtp:101 apt=100\r\n" +
		"a=rtpmap:119 H264/90000\r\n" +
		"a=fmtp:119 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=64001f\r\n" +
		"a=rtpmap:120 rtx/90000\r\n" +
		"a=fmtp:120 apt=119\r\n" +
		"a=rtpmap:49 H265/90000\r\n" +
		"a=fmtp:49 level-id=93;profile-id=1;tier-flag=0;tx-mode=SRST\r\n" +
		"a=rtpmap:50 rtx/90000\r\n" +
		"a=fmtp:50 apt=49\r\n" +
		"a=rtpmap:123 red/90000\r\n" +
		"a=rtpmap:124 rtx/90000\r\n" +
		"a=fmtp:124 apt=123\r\n" +
		"a=rtpmap:125 ulpfec/90000\r\n"

	firefoxOffer = "v=0\r\n" +
		"o=mozilla...THIS_IS_SDPARTA-133.0 5318405837761948224 0 IN IP4 0.0.0.0\r\n" +
		"s=-\r\n" +
		"t=0 0\r\n" +
		"a=fingerprint:sha-256 C2:7A:19:E5:03:B8:4F:6D:92:0C:AE:31:57:F8:2B:D4:66:19:8E:A3:05:CB:72:4D:E0:9F:38:B1:16:5A:C3:2E\r\n" +
		"a=group:BUNDLE 0 1\r\n" +
		"a=ice-options:trickle\r\n" +
		"a=msid-semantic:WMS *\r\n" +
		"m=audio 9 UDP/TLS/RTP/SAVPF 109 9 0 8 101\r\n" +
		"c=IN IP4 0.0.0.0\r\n" +
		"a=recvonly\r\n" +
		"a=extmap:1 urn:ietf:params:rtp-hdrext:ssrc-audio-level\r\n" +
		"a=extmap:2/recvonly urn:ietf:params:rtp-hdrext:csrc-audio-level\r\n" +
		"a=extmap:3 urn:ietf:params:rtp-hdrext:sdes:mid\r\n" +
		"a=fmtp:109 maxplaybackrate=48000;stereo=1;useinbandfec=1\r\n" +
		"a=fmtp:101 0-15\r\n" +
		"a=ice-pwd:6f0c2b9e41d87a35c0e9f1b24d6a7c88\r\n" +
		"a=ice-ufrag:3b9d1f2e\r\n" +
		"a=mid:0\r\n" +
		"a=rtcp-mux\r\n" +
		"a=rtpmap:109 opus/48000/2\r\n" +
		"a=rtpmap:9 G722/8000/1\r\n" +
		"a=rtpmap:0 PCMU/8000\r\n" +
		"a=rtpmap:8 PCMA/8000\r\n" +
		"a=rtpmap:101 telephone-event/8000/1\r\n" +
		"a=setup:actpass\r\n" +
		"a=ssrc:2143360547 cname:{6e1b0f3a-2c7d-4b8e-9a51-0d3f7c2e8b14}\r\n" +
		"m=video 9 UDP/TLS/RTP/SAVPF 120 124 121 125 126 127 97 98 105 106 123 122 119\r\n" +
		"c=IN IP4 0.0.0.0\r\n" +
		"a=recvonly\r\n" +
		"a=extmap:3 urn:ietf:params:rtp-hdrext:sdes:mid\r\n" +
		"a=extmap:4 http://www.webrtc.org/experiments/rtp-hdrext/abs-send-time\r\n" +
		"a=extmap:5 urn:ietf:params:rtp-hdrext:toffset\r\n" +
		"a=extmap:6/recvonly http://www.webrtc.org/experiments/rtp-hdrext/playout-delay\r\n" +
		"a=extmap:7 http://www.ietf.org/id/draft-holmer-rmcat-transport-wide-cc-extensions-01\r\n" +
		"a=fmtp:126 profile-level-id=42e01f;level-asymmetry-allowed=1;packetization-mode=1\r\n" +
		"a=fmtp:97 profile-level-id=42e01f;level-asymmetry-allowed=1\r\n" +
		"a=fmtp:120 max-fs=12288;max-fr=60\r\n" +
		"a=fmtp:124 apt=120\r\n" +
		"a=fmtp:121 max-fs=12288;max-fr=60\r\n" +
		"a=fmtp:125 apt=121\r\n" +
		"a=fmtp:127 apt=126\r\n" +
		"a=fmtp:98 apt=97\r\n" +
		"a=fmtp:105 profile-level-id=42001f;level-asymmetry-allowed=1;packetization-mode=1\r\n" +
		"a=fmtp:106 apt=105\r\n" +
		"a=fmtp:119 apt=122\r\n" +
		"a=ice-pwd:6f0c2b9e41d87a35c0e9f1b24d6a7c88\r\n" +
		"a=ice-ufrag:3b9d1f2e\r\n" +
		"a=mid:1\r\n" +
		"a=rtcp-fb:120 nack\r\n" +
		"a=rtcp-fb:120 nack pli\r\n" +
		"a=rtcp-fb:120 ccm fir\r\n" +
		"a=rtcp-fb:120 goog-remb\r\n" +
		"a=rtcp-fb:120 transport-cc\r\n" +
		"a=rtcp-fb:121 nack\r\n" +
		"a=rtcp-fb:121 nack pli\r\n" +
		"a=rtcp-fb:121 ccm fir\r\n" +
		"a=rtcp-fb:121 goog-remb\r\n" +
		"a=rtcp-fb:121 transport-cc\r\n" +
		"a=rtcp-fb:126 nack\r\n" +
		"a=rtcp-fb:126 nack pli\r\n" +
		"a=rtcp-fb:126 ccm fir\r\n" +
		"a=rtcp-fb:126 goog-remb\r\n" +
		"a=rtcp-fb:126 transport-cc\r\n" +
		"a=rtcp-fb:97 nack\r\n" +
		"a=rtcp-fb:97 nack pli\r\n" +
		"a=rtcp-fb:97 ccm fir\r\n" +
		"a=rtcp-fb:97 goog-remb\r\n" +
		"a=rtcp-fb:97 transport-cc\r\n" +
		"a=rtcp-fb:105 nack\r\n" +
		"a=rtcp-fb:105 nack pli\r\n" +
		"a=rtcp-fb:105 ccm fir\r\n" +
		"a=rtcp-fb:105 goog-remb\r\n" +
		"a=rtcp-fb:105 transport-cc\r\n" +
		"a=rtcp-fb:123 nack\r\n" +
		"a=rtcp-fb:123 nack pli\r\n" +
		"a=rtcp-fb:123 ccm fir\r\n" +
		"a=rtcp-fb:123 goog-remb\r\n" +
		"a=rtcp-fb:123 transport-cc\r\n" +
		"a=rtcp-fb:122 nack\r\n" +
		"a=rtcp-fb:122 nack pli\r\n" +
		"a=rtcp-fb:122 ccm fir\r\n" +
		"a=rtcp-fb:122 goog-remb\r\n" +
		"a=rtcp-fb:122 transport-cc\r\n" +
		"a=rtcp-mux\r\n" +
		"a=rtcp-rsize\r\n" +
		"a=rtpmap:120 VP8/90000\r\n" +
		"a=rtpmap:124 rtx/90000\r\n" +
		"a=rtpmap:121 VP9/90000\r\n" +
		"a=rtpmap:125 rtx/90000\r\n" +
		"a=rtpmap:126 H264/90000\r\n" +
		"a=rtpmap:127 rtx/90000\r\n" +
		"a=rtpmap:97 H264/90000\r\n" +
		"a=rtpmap:98 rtx/90000\r\n" +
		"a=rtpmap:105 H264/90000\r\n" +
		"a=rtpmap:106 rtx/90000\r\n" +
		"a=rtpmap:123 ulpfec/90000\r\n" +
		"a=rtpmap:122 red/90000\r\n" +
		"a=rtpmap:119 rtx/90000\r\n" +
		"a=setup:actpass\r\n" +
		"a=ssrc:2981374805 cname:{6e1b0f3a-2c7d-4b8e-9a51-0d3f7c2e8b14}\r\n"
)

// offerOf is an offer with these sections, each a media kind, a port and the
// lines under it. Its session lines are the ones pion's parser needs before
// any other (v=, o=, s=, t=), then the session's own.
func offerOf(session string, sections ...[]string) string {
	var offer strings.Builder
	offer.WriteString("v=0\r\no=- 4611731400430051336 2 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\n" + session)
	for _, section := range sections {
		offer.WriteString("m=" + section[0] + " " + section[1] + " UDP/TLS/RTP/SAVPF 96\r\n")
		for _, line := range section[2:] {
			offer.WriteString(line + "\r\n")
		}
	}

	return offer.String()
}

// What Broadcast Box can be handed: one audio and one video section, each
// saying it receives. Anything else is, or might be read by Broadcast Box's
// pion as, the offer that ends Broadcast Box 2.0.2 at the stream's next key
// frame (whepOfferServable says how), so it is never passed on.
func TestOnlyAnOfferBroadcastBoxCanServeIsPassedOn(t *testing.T) {
	audio := []string{"audio", "9", "a=mid:0", "a=recvonly"}
	video := []string{"video", "9", "a=mid:1", "a=recvonly"}
	videoWith := func(lines ...string) []string { return append([]string{"video", "9", "a=mid:1"}, lines...) }

	servable := map[string]string{
		"the offer the relay's tests send":   viewerOffer,
		"Matty's":                            mattyOffer,
		"the panel's, from Chrome":           chromeOffer,
		"the panel's, from Firefox":          firefoxOffer,
		"Matty's, with bare newlines":        strings.ReplaceAll(mattyOffer, "\r\n", "\n"),
		"Chrome's, with bare newlines":       strings.ReplaceAll(chromeOffer, "\r\n", "\n"),
		"video first":                        offerOf("", video, audio),
		"sendrecv":                           offerOf("", []string{"audio", "9", "a=sendrecv"}, []string{"video", "9", "a=sendrecv"}),
		"a section over the session's":       offerOf("a=inactive\r\n", audio, video),
		"video riding the audio's transport": offerOf("a=group:BUNDLE 0 1\r\n", audio, []string{"video", "0", "a=mid:1", "a=bundle-only", "a=recvonly"}),
		"a data channel beside them":         offerOf("", audio, video, []string{"application", "9", "a=mid:2"}),
	}
	for name, offer := range servable {
		require.True(t, whepOfferServable(offer), name)
	}

	refused := map[string]string{
		"nothing":                                 "",
		"no sections":                             "v=0\r\ns=-\r\n",
		"not SDP pion reads (no o= line)":         "v=0\r\ns=-\r\nt=0 0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 96\r\na=recvonly\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\na=recvonly\r\n",
		"audio only: the one that ends it":        offerOf("", audio),
		"video only":                              offerOf("", video),
		"video marked inactive":                   offerOf("", audio, videoWith("a=inactive")),
		"video to send only":                      offerOf("", audio, videoWith("a=sendonly")),
		"audio marked inactive":                   offerOf("", []string{"audio", "9", "a=inactive"}, video),
		"video turned down in the offer":          offerOf("", audio, []string{"video", "0", "a=recvonly"}),
		"no direction at all":                     offerOf("", []string{"audio", "9"}, []string{"video", "9"}),
		"only the session's direction":            offerOf("a=recvonly\r\n", []string{"audio", "9"}, []string{"video", "9"}),
		"inactive by the session, unsaid":         offerOf("a=inactive\r\n", []string{"audio", "9"}, []string{"video", "9"}),
		"video with no direction of its own":      offerOf("a=recvonly\r\n", audio, videoWith()),
		"inactive, then recvonly":                 offerOf("", audio, videoWith("a=inactive", "a=recvonly")),
		"sendonly, then recvonly":                 offerOf("", audio, videoWith("a=sendonly", "a=recvonly")),
		"inactive with a value":                   offerOf("", audio, videoWith("a=inactive:x", "a=recvonly")),
		"a direction in capitals":                 offerOf("", audio, videoWith("a=Inactive")),
		"recvonly with a space after it":          offerOf("", audio, videoWith("a=recvonly ")),
		"a lone carriage return before inactive":  offerOf("", audio, videoWith("a=recvonly\ra=inactive")),
		"a lone carriage return at the start":     "\r" + viewerOffer,
		"a turned-down video before the real one": offerOf("", audio, []string{"video", "0", "a=mid:2", "a=recvonly"}, video),
		"a turned-down video after the real one":  offerOf("", audio, video, []string{"video", "0", "a=mid:2", "a=recvonly"}),
		"two receiving videos":                    offerOf("", audio, video, []string{"video", "9", "a=mid:2", "a=recvonly"}),
		"two audios":                              offerOf("", audio, audio, video),
		"a data channel alone":                    offerOf("", []string{"application", "9"}),
		"an m= line that says nothing":            "v=0\r\nm=\r\n",
		"a direction before a second video":       offerOf("", audio, video, videoWith("a=inactive")),
		"one that does not end its last line":     strings.TrimSuffix(viewerOffer, "\r\n"),
	}
	for name, offer := range refused {
		require.False(t, whepOfferServable(offer), name)
	}
}

// Such an offer is refused with a code and a sentence, and Broadcast Box is
// asked nothing: not for the playback, and no session is noted for a face, a
// board or a clip.
func TestAnOfferBroadcastBoxCannotServeNeverReachesIt(t *testing.T) {
	origin, issued := fakeWHEPOrigin(t)
	defer origin.Close()

	p := stillsPlugin(t)
	p.httpClient = origin.Client()
	cfg := &configuration{BroadcastBoxURL: origin.URL, AlwaysOnStreamKeys: "videokey-watts", LivePreviews: true, ShowWatchers: true}
	cfg.normalize()
	p.setConfiguration(cfg)

	post := func(offer string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/whep", strings.NewReader(offer))
		request.Header.Set("X-Stream-Handle", p.streamHandle("videokey-dave"))
		recorder := httptest.NewRecorder()
		p.handleWHEP(recorder, request, "uid-a")

		return recorder
	}

	// What the clients send, padded past the limit with lines pion reads and
	// ignores: an offer that would pass, refused for its length alone. Its
	// first line past the limit starts exactly one byte over it, so that a
	// handler which only cut the body short there would still hand Broadcast
	// Box a whole offer.
	filler := "a=x-padding:" + strings.Repeat("0", maxWHEPOfferBytes+1-len(viewerOffer)-len("a=x-padding:\r\n")) + "\r\n"
	padded := viewerOffer + filler + "a=x-more:1\r\n"
	require.Len(t, viewerOffer+filler, maxWHEPOfferBytes+1)
	require.True(t, whepOfferServable(viewerOffer+filler))
	require.True(t, whepOfferServable(padded))

	for name, offer := range map[string]string{
		"audio only":              offerOf("", []string{"audio", "9", "a=recvonly"}),
		"video inactive":          offerOf("", []string{"audio", "9", "a=recvonly"}, []string{"video", "9", "a=inactive"}),
		"not an offer":            "v=0\r\n",
		"inactive, then recvonly": offerOf("", []string{"audio", "9", "a=recvonly"}, []string{"video", "9", "a=inactive", "a=recvonly"}),
		"over 64 KiB":             padded,
	} {
		response := post(offer)
		require.Equal(t, http.StatusBadRequest, response.Code, name)

		var refusal errorResponse
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &refusal), name)
		require.Equal(t, whepCodeBadOffer, refusal.Code, name)
		require.Equal(t, whepBadOffer, refusal.Error, name)
	}
	require.Empty(t, issued(), "Broadcast Box was never asked")
	require.Empty(t, rememberedSessions(p.watchers()))

	// What the clients send goes through as before.
	require.Equal(t, http.StatusOK, post(viewerOffer).Code)
	require.Len(t, issued(), 1)
}
