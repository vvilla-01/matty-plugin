package main

import "sync"

// The phone sessions this node holds: for each Mumble connection made here
// for somebody talking from Matty, its session number on the voice server and
// the Mattermost account that joined.
//
// The roster asks this before the name rules (rosterOf), so somebody here
// through Matty is shown as the account that joined, with the phone mark,
// whatever name the voice server knows them by. Every change to it rebuilds
// and republishes the roster, so a session that never moves, mutes or speaks
// is still shown as who it is.
//
// Only this node's own sessions are in it: the plugin holds Mumble per node.

// voicePhoneRegistry is the registry. Its zero value is empty and ready.
type voicePhoneRegistry struct {
	lock     sync.Mutex
	sessions map[uint32]*voicePhoneEntry
}

// voicePhoneEntry is one session in the registry. An entry is taken out by
// itself, never by its session number alone, so a session that ends late
// cannot take out the one that has since been given the same number.
type voicePhoneEntry struct {
	userID string
}

// userOf is the Mattermost account behind a session on the voice server,
// when the session is one this node holds.
func (r *voicePhoneRegistry) userOf(session uint32) (string, bool) {
	r.lock.Lock()
	defer r.lock.Unlock()

	entry, ok := r.sessions[session]
	if !ok {
		return "", false
	}

	return entry.userID, true
}

// count is how many sessions the registry holds.
func (r *voicePhoneRegistry) count() int {
	r.lock.Lock()
	defer r.lock.Unlock()

	return len(r.sessions)
}

// put records a session as the given account's, in place of whatever had
// that number before, and returns what takes it out again: that call removes
// this entry only, and only once.
func (r *voicePhoneRegistry) put(session uint32, userID string) func() bool {
	entry := &voicePhoneEntry{userID: userID}

	r.lock.Lock()
	if r.sessions == nil {
		r.sessions = map[uint32]*voicePhoneEntry{}
	}
	r.sessions[session] = entry
	r.lock.Unlock()

	return func() bool {
		r.lock.Lock()
		defer r.lock.Unlock()

		if r.sessions[session] != entry {
			return false
		}
		delete(r.sessions, session)

		return true
	}
}

// putVoicePhone records one of this node's phone sessions and has the roster
// rebuilt and republished; what it returns takes the session out again, and
// has the roster rebuilt in turn.
func (p *Plugin) putVoicePhone(session uint32, userID string) func() {
	remove := p.voicePhones.put(session, userID)
	p.goSafely("rebuilding the voice roster", p.republishVoice)

	return func() {
		if remove() {
			p.goSafely("rebuilding the voice roster", p.republishVoice)
		}
	}
}
