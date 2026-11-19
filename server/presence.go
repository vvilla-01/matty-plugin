package main

import (
	"encoding/json"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// The live badge: the custom status emoji, borrowed while somebody is
// broadcasting and handed straight back when they stop.
//
// The custom status is the only thing a plugin can put beside a name in
// Mattermost's own furniture — the members list, the direct message list — and
// there is exactly one of it per person. So it is only ever borrowed for
// something that lasts an evening.
//
// An airtime award lived here too for one version, and is deliberately gone. A
// medal can last months, and holding somebody's own status for a season is not
// a thing a television plugin should do. The awards still appear beside a name
// on a post and in the profile popover, where the plugin has a slot of its own
// and takes nothing from anybody.

// liveRecord is what the plugin remembers about a slot it has borrowed: the
// emoji it put there, and what was there before.
//
// The emoji is stored so the two things that look identical from outside can be
// told apart — an admin changing the Live badge emoji setting, which should
// update the badge, and the person changing their own status, which means they
// have taken the slot back and the plugin should stop touching it.
type liveRecord struct {
	Emoji    string              `json:"set,omitempty"`
	Previous *model.CustomStatus `json:"prev,omitempty"`

	// Theirs marks a slot the plugin has given up on: they set their own status
	// while it was borrowed, so it is theirs for the rest of the broadcast.
	//
	// A tombstone rather than simply forgetting them. Forgetting would put the
	// badge straight back on the next poll, because they are still live and the
	// plugin would have no memory of having been told otherwise — it would look
	// to them like the plugin was overwriting their status every fifteen
	// seconds, which is precisely the thing this rule exists to stop.
	Theirs bool `json:"theirs,omitempty"`
}

// readLive returns the record, and whether the plugin is holding the slot at
// all. Holding it is the existence of the document, not anything inside it.
//
// Three shapes decode here, because two older ones may still be on disk: this
// one, the 1.16 record that had a "kind" beside these fields, and the bare
// *model.CustomStatus written before that. All three are read for the previous
// status, so an upgrade hands people back what was theirs instead of stranding
// them wearing a red dot.
func (p *Plugin) readLive(userID string) (*liveRecord, bool) {
	data, appErr := p.API.KVGet(kvPrevStatusPrefix + userID)
	if appErr != nil || len(data) == 0 {
		return nil, false
	}

	var record liveRecord
	if err := json.Unmarshal(data, &record); err == nil && (record.Emoji != "" || record.Theirs) {
		return &record, true
	}

	var previous *model.CustomStatus
	if err := json.Unmarshal(data, &previous); err != nil {
		// Unreadable. The slot is still held — the document exists — but there
		// is nothing to give back, so clearing will simply empty it.
		return &liveRecord{}, true
	}

	return &liveRecord{Previous: previous}, true
}

func (p *Plugin) writeLive(userID string, record *liveRecord) bool {
	data, err := json.Marshal(record)
	if err != nil {
		return false
	}

	if appErr := p.API.KVSet(kvPrevStatusPrefix+userID, data); appErr != nil {
		p.API.LogWarn("Could not remember the previous custom status", "user_id", userID, "error", appErr.Error())

		return false
	}

	return true
}

func (p *Plugin) currentStatus(userID string) *model.CustomStatus {
	user, err := p.API.GetUser(userID)
	if err != nil || user == nil {
		return nil
	}

	return user.GetCustomStatus()
}

func statusEmpty(status *model.CustomStatus) bool {
	return status == nil || (status.Emoji == "" && status.Text == "")
}

// reclaimed reports whether the person has taken their own slot back while the
// plugin was holding it. When they have, it is marked as theirs and the plugin
// stops touching it until the broadcast ends.
func (p *Plugin) reclaimed(userID string, record *liveRecord) bool {
	if record == nil {
		return false
	}
	if record.Theirs {
		return true
	}
	if record.Emoji == "" {
		return false
	}

	now := p.currentStatus(userID)
	if now == nil || now.Emoji == record.Emoji {
		return false
	}

	p.writeLive(userID, &liveRecord{Theirs: true})

	return true
}

// setLiveBadge puts the live emoji in somebody's slot, remembering what was
// there first. It reports whether the slot is the plugin's when it is done:
// not when the person has taken it back, and not when it could not be set.
func (p *Plugin) setLiveBadge(userID, emoji, text string) bool {
	record, holding := p.readLive(userID)

	if holding {
		if p.reclaimed(userID, record) {
			return false
		}
		if record.Emoji == emoji {
			// Already wearing the right one. Rewriting it every poll would be
			// a status update per live streamer every fifteen seconds.
			return true
		}
	}

	// What was in the slot before the plugin ever touched it: from the record
	// when it is already holding it, from the person otherwise.
	previous := p.currentStatus(userID)
	if holding {
		previous = record.Previous
	}

	// Remembered first: if the update below fails this is rolled back, but if
	// the server dies in between the plugin would rather restore twice than
	// strand a badge it has no memory of.
	if !p.writeLive(userID, &liveRecord{Emoji: emoji, Previous: previous}) {
		return false
	}

	if appErr := p.API.UpdateUserCustomStatus(userID, &model.CustomStatus{Emoji: emoji, Text: text}); appErr != nil {
		p.API.LogWarn("Could not set the live badge", "user_id", userID, "error", appErr.Error())
		if !holding {
			_ = p.API.KVDelete(kvPrevStatusPrefix + userID)
		}

		return false
	}

	return true
}

// clearLiveBadge gives a slot back to its owner.
func (p *Plugin) clearLiveBadge(userID string) {
	record, holding := p.readLive(userID)
	if !holding {
		return
	}

	// Theirs now, so there is nothing to give back — only the tombstone to
	// clear, so the next broadcast starts fresh.
	if p.reclaimed(userID, record) {
		_ = p.API.KVDelete(kvPrevStatusPrefix + userID)

		return
	}

	// The record is only forgotten once the slot has actually been handed back.
	//
	// Deleting it regardless is how a badge gets welded on: the restore fails
	// once — a row lock, a moment of database trouble — the plugin forgets it
	// ever borrowed the slot, and reconcileBadges never looks at that person
	// again, because the only reason it had to was the record that has just
	// been deleted. They keep the badge for ever and their own status is gone.
	//
	// Keeping the record means the next poll tries again, which is what the
	// grace window already does for everything else here.
	if !statusEmpty(record.Previous) {
		if appErr := p.API.UpdateUserCustomStatus(userID, record.Previous); appErr != nil {
			p.API.LogWarn("Could not restore the previous custom status; will try again on the next poll",
				"user_id", userID, "error", appErr.Error())

			return
		}
	} else if appErr := p.API.RemoveUserCustomStatus(userID); appErr != nil {
		p.API.LogWarn("Could not clear the badge; will try again on the next poll",
			"user_id", userID, "error", appErr.Error())

		return
	}

	_ = p.API.KVDelete(kvPrevStatusPrefix + userID)
}

// clearAllBadges gives every slot the plugin holds back to its owner. It runs
// on deactivation, and whenever the badge is switched off.
func (p *Plugin) clearAllBadges() {
	for _, key := range p.kvKeysWithPrefix(kvPrevStatusPrefix) {
		p.clearLiveBadge(strings.TrimPrefix(key, kvPrevStatusPrefix))
	}
}
