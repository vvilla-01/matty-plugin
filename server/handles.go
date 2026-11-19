package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"

	"github.com/mattermost/mattermost/server/public/model"
)

// Stream keys never reach the browser. The panel refers to a stream by an
// opaque handle instead, and the plugin maps it back server-side when it
// relays the WHEP offer.
//
// Without this, any signed-in Mattermost user could read real stream keys out
// of devtools and use them directly against Broadcast Box — which defeats the
// point of making Mattermost the only way in. The handle is an HMAC of the
// stream key under a per-installation secret, so it is stable across polls
// (the panel can keep a selection) but reveals nothing about the key.

func (p *Plugin) loadHandleSecret() {
	if data, appErr := p.API.KVGet(kvHandleSecret); appErr == nil && len(data) == 32 {
		p.handleSecret = data
		return
	}

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		p.API.LogError("Could not generate a handle secret", "error", err.Error())
		return
	}

	// Atomic on purpose: a plain read-then-write lets two cluster nodes starting
	// together each persist a different secret, after which handles minted by
	// one node do not resolve on the other and playback fails intermittently.
	written, appErr := p.API.KVSetWithOptions(kvHandleSecret, secret, model.PluginKVSetOptions{
		Atomic:   true,
		OldValue: nil,
	})
	if appErr != nil {
		p.API.LogWarn("Could not persist the handle secret", "error", appErr.Error())
	}

	if !written {
		// Another node won the race; adopt its secret so handles agree.
		if data, getErr := p.API.KVGet(kvHandleSecret); getErr == nil && len(data) == 32 {
			p.handleSecret = data
			return
		}
	}

	p.handleSecret = secret
}

func (p *Plugin) streamHandle(streamKey string) string {
	if len(p.handleSecret) == 0 {
		return ""
	}

	// Hash the exact key that will be relayed. Hashing a case-folded, prefix-
	// stripped form made distinct live streams share one handle, and the
	// first-match lookup below would then serve the wrong one.
	mac := hmac.New(sha256.New, p.handleSecret)
	mac.Write([]byte(streamKey))

	return hex.EncodeToString(mac.Sum(nil))[:32]
}

// twitchHandle is a Twitch stream's handle: an HMAC under the same secret,
// over the Twitch id with a prefix no stream key can have, so it can never
// collide with a Broadcast Box handle. Never streamHandle, which would make a
// Twitch id and a stream key of the same spelling share one.
func (p *Plugin) twitchHandle(twitchID string) string {
	if len(p.handleSecret) == 0 || twitchID == "" {
		return ""
	}

	mac := hmac.New(sha256.New, p.handleSecret)
	mac.Write([]byte("villanet-twitch\x00" + twitchID))

	return hex.EncodeToString(mac.Sum(nil))[:32]
}

// streamKeyForHandle resolves a handle back to a live stream key. Only streams
// that are currently live resolve, so the endpoint cannot be used to probe for
// keys that are not already being served. A Twitch stream never resolves: it
// has no key, and nothing that relays to Broadcast Box may be handed one.
func (p *Plugin) streamKeyForHandle(handle string) string {
	if handle == "" {
		return ""
	}

	for _, stream := range p.getSnapshot().Streams {
		if stream.Source != "" {
			continue
		}
		if hmac.Equal([]byte(stream.Handle), []byte(handle)) {
			return stream.StreamKey
		}
	}

	return ""
}
