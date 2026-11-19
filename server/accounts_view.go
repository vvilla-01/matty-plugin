package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// GET /api/v1/accounts (design/3.4.0.md §2.9): everybody's Steam status and
// Hytale username, for whoever may watch the panel, linked or let in, in one
// answer from memory. No KV read and no outside request: it is built from the
// loop's cached link documents joined to the Steam statuses.
//
// Not the snapshot, and not /streams. Both reach everybody, locked people
// included: the snapshot is published to every connection and /streams is
// open, so anything put there would be shown to the very people Victor said
// must see nothing. The gate refuses a locked reader this route with its own
// 403, and the accounts event that says "read again" carries nothing.

// accountsView is the answer.
type accountsView struct {
	V      string                        `json:"v"` // the first 12 hex of sha256 of people as sent
	Steam  accountsViewSteam             `json:"steam"`
	Hytale accountsViewService           `json:"hytale"`
	People map[string]accountsViewPerson `json:"people"` // always an object, {} when nobody: never omitempty
}

// people holds everybody there is something to say about: who is connected to
// a service that is on, and, for the activity line alone, anybody on the air
// whose stream names a game.

type accountsViewService struct {
	On bool `json:"on"`
}

// When that read was is deliberately not here. No client shows it, and it
// moves with every good read (once a minute): with it, every answer differed
// from the one before, and a conditional read (handleAccounts) never matched.
type accountsViewSteam struct {
	On bool `json:"on"`

	// A good read in the last five minutes. Always sent, false while Steam is
	// off: a field that is sometimes absent is one every client has to guess
	// a meaning for.
	Fresh bool `json:"fresh"`
}

type accountsViewPerson struct {
	Steam  *accountsViewSteamPerson  `json:"steam,omitempty"`
	Hytale *accountsViewHytalePerson `json:"hytale,omitempty"`

	// What they are doing, in a line: "Playing" or "Streaming" a game, as
	// accounts_activity.go decides it. Also for somebody connected to
	// neither service whose own stream says which game it is, who is in
	// people for this alone.
	Activity *accountsViewActivity `json:"activity,omitempty"`
}

type accountsViewSteamPerson struct {
	Name   string  `json:"name"`
	URL    string  `json:"url"`
	State  string  `json:"state"`            // "online" | "busy" | "away" | "offline", or "" while not fresh
	InGame bool    `json:"inGame,omitempty"` // Steam reports a game for a public profile
	Game   *string `json:"game,omitempty"`   // only with inGame; may be ""
}

type accountsViewHytalePerson struct {
	Name string `json:"name"`
}

// buildViewLocked is the answer as memory has it now, with lock held. Only
// services that are on contribute; a document set aside contributes nothing;
// people blocked, or found deactivated or gone, are left out (rule 2: the
// cached documents, joined to the statuses by SteamID).
func (s *accountsStore) buildViewLocked(cfg *configuration, now time.Time) accountsView {
	view := accountsView{People: map[string]accountsViewPerson{}}

	// Who Steam reports in a game, for the line that says what they are
	// doing (addActivitiesLocked).
	steamGames := map[string]steamGame{}

	if cfg.steamOn() {
		// A key Steam refused leaves no status standing, however recent the
		// last good read.
		fresh := !s.steamLastGood.IsZero() && now.Sub(s.steamLastGood) <= steamKeepGood && !s.keyRefused
		view.Steam = accountsViewSteam{On: true, Fresh: fresh}

		if service := s.serviceLocked(steamProvider); !service.bad && service.links != nil {
			for userID, link := range service.links.Users {
				if s.leftOutLocked(service.links, userID) {
					continue
				}

				person := view.People[userID]
				row := &accountsViewSteamPerson{Name: link.Name, URL: steamProfileURL(link.ID)}
				if status, known := s.steam[link.ID]; fresh && known {
					row.State = status.State
					if status.InGame && !status.Hidden {
						game := status.Game
						row.InGame, row.Game = true, &game
						steamGames[userID] = steamGame{title: status.Game, appID: status.GameID}
					}
				}
				person.Steam = row
				view.People[userID] = person
			}
		}
	}

	if cfg.hytaleOn() {
		view.Hytale = accountsViewService{On: true}

		if service := s.serviceLocked(hytaleProvider); !service.bad && service.links != nil {
			for userID, link := range service.links.Users {
				if s.leftOutLocked(service.links, userID) {
					continue
				}

				person := view.People[userID]
				person.Hytale = &accountsViewHytalePerson{Name: link.Name}
				view.People[userID] = person
			}
		}
	}

	s.addActivitiesLocked(&view, steamGames)

	view.V = accountsPeopleV(view.People)

	return view
}

// accountsPeopleV is the first 12 hex of sha256 of people as sent. Go writes a
// map's keys sorted, so the same people are the same bytes and the same v.
func accountsPeopleV(people map[string]accountsViewPerson) string {
	encoded, _ := json.Marshal(people)
	sum := sha256.Sum256(encoded)

	return hex.EncodeToString(sum[:])[:12]
}

// leftOutLocked is whether somebody connected is left out of the view: blocked,
// or a Mattermost account the loop found deactivated or gone.
func (s *accountsStore) leftOutLocked(doc *accountLinks, userID string) bool {
	if _, blocked := doc.Blocked[userID]; blocked {
		return true
	}

	return s.inactive[userID]
}

// rebuildView builds the answer again and keeps it, after a link write or a
// step. When v moves, every panel is to be told to read again.
func (s *accountsStore) rebuildView(cfg *configuration, now time.Time) {
	if s.keepView(cfg, now) {
		s.announce()
	}
}

// buildView is the answer as of now, built under the lock and let go by a
// defer.
func (s *accountsStore) buildView(cfg *configuration) accountsView {
	s.lock.Lock()
	defer s.lock.Unlock()

	return s.buildViewLocked(cfg, s.now())
}

// keepView builds the answer and keeps it, and is whether v moved. The lock
// is let go by a defer: building the answer is real work, and a panic in it
// must not leave every reader of the accounts waiting for good.
func (s *accountsStore) keepView(cfg *configuration, now time.Time) bool {
	s.lock.Lock()
	defer s.lock.Unlock()

	view := s.buildViewLocked(cfg, now)
	encoded, err := json.Marshal(view)
	if err != nil {
		return false
	}

	moved := view.V != s.viewV
	s.view, s.viewV = encoded, view.V

	return moved
}

// announce is the accounts event, to everybody, with nothing in it: "read
// GET /api/v1/accounts again, if you may watch". A broadcast cannot name a
// set of people, so it goes to every connection, and says nothing a locked
// reader could use but that something changed; reading again is the gate's
// business.
//
// The first change of a quiet spell (no event in the last accountsEventGap)
// goes out at once: a game somebody just started is beside their name as soon
// as Steam has said so, not ten seconds after. A change sooner than that after
// an event waits accountsEventGap, and a change while one is waiting is
// covered by that one. So a burst of changes is two events, its first change
// and its last state, and there is never more than one in any ten seconds.
func (s *accountsStore) announce() {
	s.eventLock.Lock()

	if s.eventCancel != nil {
		s.eventLock.Unlock()
		return
	}

	if now := s.now(); s.eventLast.IsZero() || now.Sub(s.eventLast) >= accountsEventGap {
		s.eventLast = now
		s.eventLock.Unlock()
		s.plugin.API.PublishWebSocketEvent(wsEventAccounts, map[string]any{}, &model.WebsocketBroadcast{})

		return
	}
	defer s.eventLock.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	s.eventCancel = cancel
	wait := s.after(accountsEventGap)

	// A panic once the wait is over is logged and ends this event alone
	// (recovery.go); the wait itself has nothing in it to panic, and the
	// plugin is not asked for its log before it is over. eventWait and the
	// context are given back by the defers, the lock by its own, and the event
	// no longer counts as waiting before anything that could panic, so the
	// next change still asks for an event of its own.
	s.eventWait.Add(1)
	go func() {
		defer s.eventWait.Done()
		defer cancel()

		select {
		case <-ctx.Done():
			return
		case <-wait:
		}

		s.plugin.safely("the delayed accounts event", func() {
			// Once it is no longer waiting, the next change asks for the next
			// event; one stopped meanwhile sends nothing.
			stopped := false
			func() {
				s.eventLock.Lock()
				defer s.eventLock.Unlock()

				stopped = ctx.Err() != nil
				if !stopped {
					s.eventCancel = nil
					s.eventLast = s.now()
				}
			}()
			if stopped {
				return
			}

			s.plugin.API.PublishWebSocketEvent(wsEventAccounts, map[string]any{}, &model.WebsocketBroadcast{})
		})
	}()
}

// stopEvents drops an accounts event that is still waiting, and waits for
// the goroutine that holds it.
func (s *accountsStore) stopEvents() {
	s.eventLock.Lock()
	if s.eventCancel != nil {
		s.eventCancel()
		s.eventCancel = nil
	}
	s.eventLock.Unlock()

	s.eventWait.Wait()
}

// handleAccounts answers GET /api/v1/accounts. The gate has already found the
// caller linked or let in. Built from memory on every request, so the
// freshness a reader is told is the freshness now, whether or not the loop
// has stepped since.
//
// A reader who still holds the last answer says so with If-None-Match and is
// told 304 when nothing in it has changed (not who is connected, not a
// status, not whether Steam's read is fresh). It is still no-store: only the
// reader's own copy, in memory, is ever revalidated, never a cache's.
func (p *Plugin) handleAccounts(w http.ResponseWriter, r *http.Request) {
	s := p.accounts()
	cfg := p.getConfiguration()

	view := s.buildView(cfg)

	w.Header().Set("Cache-Control", "no-store")

	// The whole answer as sent, not just V: V is the people, and whether a
	// service is on, or Steam's read fresh, changes what a client shows
	// without changing them. Nothing in it moves by itself (see
	// accountsViewSteam), so somebody who asks again a minute or an hour
	// later with nothing changed is told so in a few bytes.
	if encoded, err := json.Marshal(view); err == nil {
		sum := sha256.Sum256(encoded)
		if unchangedSince(w, r, hex.EncodeToString(sum[:8])) {
			return
		}
	}

	writeJSON(w, http.StatusOK, view)
}
