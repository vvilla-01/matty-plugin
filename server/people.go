package main

import (
	"maps"
	"net/http"
	"strings"
)

// The people index: what the snapshot says about each person, by user id.
//
// A client shows people in many places that are not a stream card: beside a
// name on a post, in a member list, on a profile, in a direct message row.
// Each of those asks the same three things: are they on the air (and on
// which card), are they in voice, and is the custom status beside their name
// theirs or the plugin's. Until 3.5.0 every client worked those out for
// itself, from the cards, the voice roster and a guess at the status, and
// the two clients did not agree: one ringed the owner of a 24/7 channel as
// live for ever, read a person with two voice sessions as muted when one of
// them was, and told the live badge from somebody's own status by matching
// the words "Live now". Here the rules are applied once, where everything
// they need is known, and a client looks a person up.
//
// Only what every signed-in reader was always shown about people is here, so
// it tells a locked reader nothing new: the cards carry who is live, the
// voice section who is in voice, a custom status is visible to anybody on
// the server, and the awards beside a name are drawn on every post. Steam
// and Hytale are not here, and must not be: they are for people who may
// watch, through GET /api/v1/accounts.

// personView is one person's entry. Every field is absent when it has nothing
// to say, and a person with nothing to say has no entry.
type personView struct {
	// The handle of the card that stands for them while they are on the air:
	// their own Broadcast Box stream before their Twitch one, and never a
	// 24/7 channel, a paused stream or an entry another card covers.
	Live string `json:"live,omitempty"`

	// In voice: voiceOpen, or voiceMuted when every session of theirs is.
	Voice string `json:"voice,omitempty"`

	// The custom status the plugin is holding for them right now: badgeLive
	// or badgeBirthday. Absent when the status beside their name is their own
	// (they never had a badge, or took the slot back), so a client can leave
	// the plugin's badge out where it would only repeat what the client
	// already shows, without ever hiding words a person chose.
	Badge string `json:"badge,omitempty"`

	// The awards drawn beside their name, in order, at most four: their own
	// choice, or Automatic. Decided by publish from the boards, which are no
	// longer in the snapshot: see awards_decide.go. Absent for the great
	// majority of people, which is the point of an award.
	Awards []besideAward `json:"awards,omitempty"`
}

const (
	voiceOpen  = "open"
	voiceMuted = "muted"

	badgeLive     = "live"
	badgeBirthday = "birthday"
)

// liveHandles is who is on the air, by person: one handle each.
//
// The rule is the one the web panel applied for itself until 3.5.0 (its
// liveHandleByUser), word for word: never a covered entry, a paused stream or
// a 24/7 channel, and the Broadcast Box stream rather than the Twitch one
// when a person has both. It is now the only copy: the panel and the app read
// the index, and the badge beside a name (reconcileBadges) reads this.
func liveHandles(streams []apiStream) map[string]string {
	byUser := map[string]string{}
	fromBroadcastBox := map[string]bool{}

	for _, stream := range streams {
		if stream.UserID == "" || stream.Paused || stream.AlwaysOn || stream.CoveredBy != "" {
			continue
		}

		broadcastBox := stream.Source == ""
		if fromBroadcastBox[stream.UserID] && !broadcastBox {
			continue
		}

		byUser[stream.UserID] = stream.Handle
		fromBroadcastBox[stream.UserID] = broadcastBox
	}

	return byUser
}

// voiceByUser is who is in voice, by person. Somebody connected twice (a
// desktop and a phone) is muted only when both are: one open microphone is an
// open microphone. People the roster could not match to an account have no
// user id and are not in it.
func voiceByUser(voice *voiceView) map[string]string {
	byUser := map[string]string{}
	if voice == nil || !voice.Connected {
		return byUser
	}

	for _, person := range voice.People {
		if person.UserID == "" {
			continue
		}
		if !person.Muted {
			byUser[person.UserID] = voiceOpen
		} else if _, seen := byUser[person.UserID]; !seen {
			byUser[person.UserID] = voiceMuted
		}
	}

	return byUser
}

// peopleIndex puts the three together. Never nil: a quiet snapshot says {},
// which is "nobody", and not the silence of a server with no index at all.
func peopleIndex(streams []apiStream, voice *voiceView, badges map[string]string) map[string]personView {
	live := liveHandles(streams)
	inVoice := voiceByUser(voice)

	people := make(map[string]personView, len(live)+len(inVoice)+len(badges))
	for userID, handle := range live {
		person := people[userID]
		person.Live = handle
		people[userID] = person
	}
	for userID, state := range inVoice {
		person := people[userID]
		person.Voice = state
		people[userID] = person
	}
	for userID, kind := range badges {
		person := people[userID]
		person.Badge = kind
		people[userID] = person
	}

	return people
}

// wornBadges is who is wearing one of the plugin's badges, and which: as of
// the last time reconcileBadges looked.
func (p *Plugin) wornBadges() map[string]string {
	p.badgeLock.RLock()
	defer p.badgeLock.RUnlock()

	return maps.Clone(p.badges)
}

// setWornBadges records who is wearing what. When that has changed, the
// snapshot goes out again with it, through the same coalesced republish the
// voice roster uses: the status itself changed a moment ago, and a client
// that only heard about the stream would show the badge as the person's own
// words until the next poll.
func (p *Plugin) setWornBadges(worn map[string]string) {
	p.badgeLock.Lock()
	changed := !maps.Equal(p.badges, worn)
	p.badges = worn
	p.badgeLock.Unlock()

	if changed {
		p.publishRosterSoon()
	}
}

// quoted is a tag as an entity tag.
func quoted(tag string) string {
	return `"` + tag + `"`
}

// unchangedSince answers a conditional read. A client that still holds the
// answer it was last given sends that answer's tag back as If-None-Match, and
// when nothing a person could see has changed since, it is told so in a few
// bytes instead of being sent the whole document to decode and compare again.
// It reports whether the answer has been given (304), and otherwise leaves
// the tag on the response for the next time.
func unchangedSince(w http.ResponseWriter, r *http.Request, tag string) bool {
	if tag == "" {
		return false
	}

	w.Header().Set("ETag", quoted(tag))

	for _, candidate := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == quoted(tag) {
			w.WriteHeader(http.StatusNotModified)

			return true
		}
	}

	return false
}
