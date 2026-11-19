package main

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// The lock (design/lock-screen.md). Nobody uses the VillaNET panel until they
// unlock it with their own bearer token, which is linking, or until a system
// admin has let them in to watch. The webapp shows a lock screen, but that is
// a courtesy: the rule is kept here, in front of every route, so a browser
// console gets no further than the lock does.
//
// Two layers. The first is the gate in ServeHTTP (passAccessGate), and the
// clip family's own gate (requireClipsAccess), which give every route a class
// and refuse whoever the class does not admit before anything else is looked
// at. The second is the per-route "linked" checks that were there before the
// lock (requireLinkedStream and the clip handlers' own), which stay.

// access is what somebody may do in the panel. Decided on the server for every
// request, from the link and the let-in list; the webapp only follows it.
type access int

const (
	accessLocked access = iota // neither linked nor let in: the lock screen
	accessLetIn                // let in by a system admin: watch only
	accessLinked               // linked their own stream with their bearer token
)

// String is the word /me uses: "locked", "letIn", "linked".
func (a access) String() string {
	switch a {
	case accessLinked:
		return "linked"
	case accessLetIn:
		return "letIn"
	default:
		return "locked"
	}
}

const (
	// How long a yes (linked, or let in) is taken as the answer by the routes
	// one playback or one channel of cards calls many times over.
	accessSeenTTL = 10 * time.Second

	// How long the gate remembers a refusal, so that somebody refused cannot
	// drive a read of the store with every request they make.
	accessNoTTL = 2 * time.Second

	// wsEventMe reaches a client as custom_ceo.villa.matty_me, and says one
	// thing: "what the server holds about you may have changed; ask again".
	// It is every event about one reader's own state: their access (a link,
	// an unlink, a let-in), and their own connections (Twitch, Steam,
	// Hytale). Until contract 3 those were three events, "access",
	// "twitch_link" and "account_link", which all carried nothing and all
	// meant this, and each client kept three handlers that did the same
	// thing. It carries nothing about anybody's access, to one person or to
	// everybody, so it can never be the thing that unlocks anybody or tells
	// anybody anything: the answer is always asked for, and the gate decides
	// it. Since contract 4 it carries what every lock screen shows anyway:
	// the server's name for the plugin and which artwork it has (meEvent).
	wsEventMe = "me"

	// The answer /me gives when the store did not say.
	accessUnknown = "unknown"

	accessCodeLocked    = "locked"
	accessCodeWatchOnly = "watch_only"
	accessCodeStore     = "store"

	accessWatchOnlyText = "You are let in to watch only. Link your stream to do that."
)

// accessLockedText is the locked refusal, with the server's name for the
// plugin (brandName).
func accessLockedText(name string) string {
	return name + " is locked. Unlock it with your bearer token."
}

// accessStoreText is the refusal when the access store did not answer.
func accessStoreText(name string) string {
	return name + " could not check your access just now. Try again in a moment."
}

// accessSeen is a yes, and when it was given.
type accessSeen struct {
	level access
	key   string
	at    time.Time
}

// accessRefusal is a no the gate gave, and when. notAdmin is set only when a
// watch-or-admin route asked whether the caller is a system admin and was told
// they are not, so a later request to such a route can be refused without
// asking again.
type accessRefusal struct {
	level    access
	notAdmin bool
	at       time.Time
}

// accessStore is this node's memory of recent answers. Kept small and short on
// purpose: a yes for ten seconds, a no for two, both forgotten the moment the
// person's access changes here, and never a failure.
type accessStore struct {
	lock sync.Mutex

	// epoch moves on every change, so that an answer read before a change is
	// never kept after it.
	epoch   uint64
	seen    map[string]accessSeen
	refused map[string]accessRefusal

	// The clock, which a test replaces. By default the clip store's, so that
	// the tests that move one clock move every cache that is about people.
	now func() time.Time
}

func (p *Plugin) accessCache() *accessStore {
	p.accessOnce.Do(func() {
		p.accessState = &accessStore{
			seen:    map[string]accessSeen{},
			refused: map[string]accessRefusal{},
			now:     func() time.Time { return p.clips().now() },
		}
	})

	return p.accessState
}

func (c *accessStore) currentEpoch() uint64 {
	c.lock.Lock()
	defer c.lock.Unlock()

	return c.epoch
}

// seenYes is a yes given in the last accessSeenTTL.
func (c *accessStore) seenYes(userID string, now time.Time) (accessSeen, bool) {
	c.lock.Lock()
	defer c.lock.Unlock()

	seen, found := c.seen[userID]
	if !found || seen.level == accessLocked || now.Before(seen.at) || now.Sub(seen.at) >= accessSeenTTL {
		return accessSeen{}, false
	}

	return seen, true
}

// noteSeen keeps a yes, unless somebody's access changed while it was being
// read.
func (c *accessStore) noteSeen(userID string, level access, key string, now time.Time, epoch uint64) {
	if level == accessLocked {
		return
	}

	c.lock.Lock()
	defer c.lock.Unlock()

	if epoch != c.epoch {
		return
	}

	if len(c.seen) >= maxClipPeople {
		for other, seen := range c.seen {
			if now.Sub(seen.at) >= accessSeenTTL || now.Before(seen.at) {
				delete(c.seen, other)
			}
		}
	}

	c.seen[userID] = accessSeen{level: level, key: key, at: now}
}

// refusedRecently is a no given in the last accessNoTTL.
func (c *accessStore) refusedRecently(userID string, now time.Time) (accessRefusal, bool) {
	c.lock.Lock()
	defer c.lock.Unlock()

	refusal, found := c.refused[userID]
	if !found || now.Before(refusal.at) || now.Sub(refusal.at) >= accessNoTTL {
		return accessRefusal{}, false
	}

	return refusal, true
}

// noteRefusal remembers a no, unless somebody's access changed while it was
// being read. A no about whether they are an admin is kept beside a later no
// at the same level, so asking a watch route does not make the next
// watch-or-admin request ask Mattermost again.
func (c *accessStore) noteRefusal(userID string, level access, notAdmin bool, now time.Time, epoch uint64) {
	c.lock.Lock()
	defer c.lock.Unlock()

	if epoch != c.epoch {
		return
	}

	if previous, found := c.refused[userID]; found && previous.level == level &&
		!now.Before(previous.at) && now.Sub(previous.at) < accessNoTTL {
		notAdmin = notAdmin || previous.notAdmin
	}

	if len(c.refused) >= maxClipPeople {
		for other, refusal := range c.refused {
			if now.Sub(refusal.at) >= accessNoTTL || now.Before(refusal.at) {
				delete(c.refused, other)
			}
		}
	}

	c.refused[userID] = accessRefusal{level: level, notAdmin: notAdmin, at: now}
}

// forget drops everything remembered about somebody and moves the epoch.
func (c *accessStore) forget(userID string) {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.epoch++
	delete(c.seen, userID)
	delete(c.refused, userID)
}

// accessOf reads both link entries first, fresh from the store: somebody is
// linked only when link_user_<them> names a key whose link_key_ names them
// back, so a write that failed or raced half way leaves nobody linked. Only
// for somebody not linked does it look at the let-in list (this node's copy,
// see letin.go). err != nil means the store did not answer: the caller is told
// 503 "store", never "locked" and never "let in", so a database blip neither
// shuts out a linked person nor opens the door to anybody.
//
// A username match, a Twitch, Steam or Hytale connection and being a system
// admin never count.
func (p *Plugin) accessOf(userID string) (level access, key string, err error) {
	key, err = p.linkOf(userID)
	if err != nil {
		return accessLocked, "", err
	}
	if key != "" {
		return accessLinked, key, nil
	}

	in, err := p.letInHas(userID)
	if err != nil {
		return accessLocked, "", err
	}
	if in {
		return accessLetIn, "", nil
	}

	return accessLocked, "", nil
}

// accessSeenOf is accessOf with a ten-second memory of yes-answers (linked or
// let in) per person, for the routes one playback or one channel of cards calls
// many times. A failed read is never remembered, so nobody is shut out by a
// blip, and nor is a no: the gate keeps its own, shorter memory of those.
func (p *Plugin) accessSeenOf(userID string) (level access, key string, err error) {
	level, key, _, err = p.accessRead(userID, true)

	return level, key, err
}

// accessRead is accessOf, or with seen accessSeenOf, and whether what it
// answers was read from the store just now rather than remembered: only a
// fresh answer may teach Best Clipper anything.
func (p *Plugin) accessRead(userID string, seen bool) (level access, key string, fresh bool, err error) {
	c := p.accessCache()
	now := c.now()

	if seen {
		if yes, found := c.seenYes(userID, now); found {
			return yes.level, yes.key, false, nil
		}
	}

	epoch := c.currentEpoch()
	level, key, err = p.accessOf(userID)
	if err != nil {
		return accessLocked, "", true, err
	}

	if seen {
		c.noteSeen(userID, level, key, now, epoch)
	}

	return level, key, true, nil
}

// accessForget drops what this node remembers about somebody's access, for a
// change that is not theirs to be told about (a link the clip tests make
// directly) as much as for one that is.
func (p *Plugin) accessForget(userID string) {
	p.accessCache().forget(userID)
}

// accessChanged is told whenever somebody's access may have changed: they
// linked or unlinked, somebody else proved their key, an admin let them in or
// out, or an admin unlinked them. What this node remembers about them goes at
// once, and their open windows are told, with nothing in the event: it only
// says "read /me again", so it can never be the thing that unlocks anybody.
// Other nodes catch up within letInMaxAge + accessSeenTTL.
func (p *Plugin) accessChanged(userID string) {
	if userID == "" {
		return
	}

	p.accessForget(userID)
	p.tellMe(userID)
}

// tellMe tells one person's open clients to ask again who they are to the
// plugin (wsEventMe).
func (p *Plugin) tellMe(userID string) {
	if userID == "" {
		return
	}

	p.API.PublishWebSocketEvent(wsEventMe, p.meEvent(), &model.WebsocketBroadcast{UserId: userID})
}

// tellEverybodyMe is the same to every connection, for a change that is
// everybody's at once: a service switched off, everything of one deleted, the
// name or the artwork changed.
func (p *Plugin) tellEverybodyMe() {
	p.API.PublishWebSocketEvent(wsEventMe, p.meEvent(), &model.WebsocketBroadcast{})
}

// meEvent is what a "me" event carries: the server's name for the plugin and
// which artwork it has, as /access and /me give them, and nothing about
// anybody. Plain maps, because the event crosses the plugin RPC by gob, which
// knows map[string]any and not this package's types.
func (p *Plugin) meEvent() map[string]any {
	art := p.artwork()

	return map[string]any{
		"name":    p.brandName(),
		"artwork": map[string]any{"emblem": art.Emblem, "ghost": art.Ghost},
	}
}

// writeCodedError answers with the text a person reads and a code the webapp
// words for itself.
func writeCodedError(w http.ResponseWriter, status int, message, code string) {
	if code == "" {
		code = refusalCodeOf(status)
	}

	writeJSON(w, status, errorResponse{Error: message, Code: code})
}

// writeAccessRefusal is the gate's 403. Never cached: whether somebody may see
// something is only true now.
func (p *Plugin) writeAccessRefusal(w http.ResponseWriter, level access) {
	w.Header().Set("Cache-Control", "no-store")

	if level == accessLetIn {
		writeCodedError(w, http.StatusForbidden, accessWatchOnlyText, accessCodeWatchOnly)
		return
	}

	writeCodedError(w, http.StatusForbidden, accessLockedText(p.brandName()), accessCodeLocked)
}

// routeClass is who a route is for.
type routeClass int

const (
	// routeLinked is the zero value on purpose: a route nobody classified,
	// or a method nobody listed for it, is for linked people only, so a route
	// added later is refused to everybody else until somebody decides.
	routeLinked routeClass = iota

	// routeOpen is for anybody signed in: what the lock screen itself needs,
	// what the badges and the popover outside the panel need, and taking
	// your own things back.
	routeOpen

	// routeWatch is for linked people and people let in.
	routeWatch

	// routeWatchOrAdmin is routeWatch, and system admins too, for the one
	// panel route the System Console uses.
	routeWatchOrAdmin
)

// adminPathRoot is where the System Console's routes live. They skip the
// gate, and each handler asks whether the caller is a system admin before
// anything else: that is what keeps the console working without a token.
// Being under it is how such a route is classified, rather than a line in
// routeClasses: GET /api/v1/admin/birthdays/seed, the starting list for
// "Start from the old bot's list", is one, because a birthday list is for
// system admins only, token or not. The starting list is empty since 3.7.2.
// TestAdminRoutesIgnoreTheLock finds every one of them in api.go.
const adminPathRoot = "/api/v1/admin"

// routeClasses is every route ServeHTTP's switch serves outside the admin
// paths and the clip family, by path and then method. TestEveryRouteHasAnAccessClass
// holds it to api.go.
//
// Twitch's two HTML routes, connect and callback, are not here: they are
// served above the user check (design/lock-screen.md §9.2), and each runs
// accessOf with the linked class after its own session checks. The callback
// throws the state away when the person is no longer allowed, because a state
// stays good for ten minutes and a let-out or an unlink inside them must still
// stop the connection. Nor are Steam's and Hytale's four, connect and callback
// for each (design/3.4.0.md §2.4), which do the same.
var routeClasses = map[string]map[string]routeClass{
	// The badges, medals and the profile popover outside the panel need the
	// list, and Pop out's address is no longer in it.
	"/api/v1/streams": {http.MethodGet: routeOpen},

	// The boards rode that list until 3.6.0, and a profile outside the panel
	// shows somebody's record from them to every reader: open, as they were
	// (Victor's answer of 2026-11-16).
	"/api/v1/boards": {http.MethodGet: routeOpen},

	// Profile banners, which a profile outside the panel shows to every
	// reader, as the boards are. Setting your own is open too, since it is
	// about nobody else's stream (the handler refuses guests and bots), and
	// taking it away is a takeback. Changing the part of it that shows is
	// setting it again, without the picture.
	"/api/v1/banners":        {http.MethodGet: routeOpen},
	"/api/v1/banner":         {http.MethodGet: routeOpen},
	"/api/v1/me/banner":      {http.MethodPost: routeOpen, http.MethodDelete: routeOpen},
	"/api/v1/me/banner/crop": {http.MethodPut: routeOpen},

	// The lock screen's own two requests, and the one question a player asks
	// while it plays: may I still watch.
	"/api/v1/me":      {http.MethodGet: routeOpen},
	"/api/v1/me/link": {http.MethodPost: routeOpen, http.MethodDelete: routeOpen},
	"/api/v1/access":  {http.MethodGet: routeOpen},

	// Whether this plugin is a signed release, with the manifest and its
	// signatures: anybody signed in, as the version on /access is.
	"/api/v1/release": {http.MethodGet: routeOpen},

	// The app check (app_check.go): anybody signed in, the lock screen
	// included, since the check is about the app and not its person. Each
	// has a meter of its own.
	"/api/v1/app/challenge": {http.MethodPost: routeOpen},
	"/api/v1/app/attest":    {http.MethodPost: routeOpen},

	// Push notifications (push_api.go): anybody signed in, the lock screen
	// included, since a notification is Mattermost's and not the panel's.
	// Taking your registration back is a takeback.
	"/api/v1/me/push":         {http.MethodGet: routeOpen, http.MethodPut: routeOpen, http.MethodDelete: routeOpen},
	"/api/v1/me/push/confirm": {http.MethodPost: routeOpen},
	"/api/v1/me/push/test":    {http.MethodPost: routeOpen},

	// The stream title is a streamer control.
	"/api/v1/me/profile": {http.MethodGet: routeLinked, http.MethodPost: routeLinked},

	// What's on? is for streamers; clearing your own is a takeback.
	"/api/v1/nowplaying": {http.MethodGet: routeLinked, http.MethodPost: routeLinked, http.MethodDelete: routeOpen},

	// Planning is for streamers. Join and leave share a route, decided by the
	// body: joining still needs a link in the handler, leaving is a takeback,
	// and so is cancelling (your own plan, or any plan for an admin).
	"/api/v1/schedule":        {http.MethodPost: routeLinked},
	"/api/v1/schedule/edit":   {http.MethodPost: routeLinked},
	"/api/v1/schedule/join":   {http.MethodPost: routeOpen},
	"/api/v1/schedule/remove": {http.MethodPost: routeOpen},

	// Title search is for the planning form. The popover outside the panel
	// shows now-playing art, which keeps its own fetch rule.
	"/api/v1/media/search": {http.MethodPost: routeLinked},
	"/api/v1/media/image":  {http.MethodGet: routeOpen},

	"/api/v1/channel/picture":   {http.MethodGet: routeWatch},
	"/api/v1/channel/thumbnail": {http.MethodGet: routeWatch},

	// A preview still is shown to everybody on the Live now card, which is
	// publishing, not watching; seeing them is watching, and the System
	// Console's preview pictures card uses the same address.
	"/api/v1/stills/claim": {http.MethodPost: routeLinked},
	"/api/v1/stills":       {http.MethodGet: routeWatchOrAdmin, http.MethodPost: routeLinked},

	// Watching. A peek, which exists only to take a still, is linked: see
	// routeClassOf.
	"/api/v1/whep": {http.MethodPost: routeWatch},

	// Pop out hands out a stream key, which a let-out or an unlink cannot
	// take back.
	"/api/v1/watch/popout": {http.MethodGet: routeLinked},

	// Connecting Twitch is for linked people: a connection makes somebody a
	// streamer whom everybody sees, which is more than watching.
	// Disconnecting and dismissing the takeover notice are takebacks, open to
	// everybody, the lock screen included, because Twitch's agreement says
	// to delete a person's data when they ask. A Twitch card's picture is
	// watching.
	"/api/v1/twitch/connect-ticket": {http.MethodPost: routeLinked},
	"/api/v1/twitch/link":           {http.MethodDelete: routeOpen},
	"/api/v1/twitch/notice":         {http.MethodPost: routeOpen},
	"/api/v1/twitch/preview":        {http.MethodGet: routeWatch},

	// Steam and Hytale (design/3.4.0.md §2.4). Seeing everybody's Steam
	// status and Hytale names is watching, never the snapshot, which reaches
	// locked people too. Connecting, from the panel or from Matty's own
	// sign-in window, is for linked people, as Twitch's is. Disconnecting
	// and dismissing the takeover notice are takebacks, open to everybody,
	// the lock screen included, whatever the switch says.
	"/api/v1/accounts":              {http.MethodGet: routeWatch},
	"/api/v1/steam/connect-ticket":  {http.MethodPost: routeLinked},
	"/api/v1/steam/app-connect":     {http.MethodPost: routeLinked},
	"/api/v1/steam/app-finish":      {http.MethodPost: routeLinked},
	"/api/v1/steam/link":            {http.MethodDelete: routeOpen},
	"/api/v1/steam/notice":          {http.MethodPost: routeOpen},
	"/api/v1/hytale/connect-ticket": {http.MethodPost: routeLinked},
	"/api/v1/hytale/app-connect":    {http.MethodPost: routeLinked},
	"/api/v1/hytale/app-finish":     {http.MethodPost: routeLinked},
	"/api/v1/hytale/link":           {http.MethodDelete: routeOpen},
	"/api/v1/hytale/notice":         {http.MethodPost: routeOpen},

	// Awards by your name (design/3.2.0.md §3): choosing is for linked
	// people, who are the only ones with the You tab; going back to
	// Automatic is a takeback, open to everybody, so somebody who has
	// unlinked can still clear what they chose.
	"/api/v1/awards/mine":    {http.MethodGet: routeLinked},
	"/api/v1/awards/display": {http.MethodPost: routeLinked, http.MethodDelete: routeOpen},

	// Your own takebacks, for the actions in your own profile popover,
	// which is outside the panel and reached linked or not (§4.6): since
	// 3.2.1 only deleting your sounds, as there is no biggest-fan opt-out
	// any more. From memory, so it costs nothing per request.
	"/api/v1/takebacks": {http.MethodGet: routeOpen},

	"/api/v1/achievements/mine": {http.MethodGet: routeLinked},

	// Sharing a plan into a channel is for streamers, as planning is.
	"/api/v1/schedule/share": {http.MethodPost: routeLinked},

	// The soundboard (§7): for linked people in Mumble, which the handlers
	// hold them to. Deleting your own sounds, one or all, is a takeback,
	// open even after an unlink or with the soundboard off.
	"/api/v1/sounds":             {http.MethodGet: routeLinked, http.MethodPost: routeLinked},
	"/api/v1/sounds/edit":        {http.MethodPost: routeLinked},
	"/api/v1/sounds/remove":      {http.MethodPost: routeOpen},
	"/api/v1/sounds/remove-mine": {http.MethodPost: routeOpen},
	"/api/v1/sounds/play":        {http.MethodPost: routeLinked},
	"/api/v1/sounds/audio":       {http.MethodGet: routeLinked},
}

// routeClassOf is a request's class, and whether its path is a route at all.
func routeClassOf(r *http.Request) (routeClass, bool) {
	methods, known := routeClasses[r.URL.Path]
	class := methods[r.Method]

	if r.URL.Path == "/api/v1/whep" && strings.TrimSpace(r.Header.Get(peekHeader)) != "" {
		class = routeLinked
	}

	return class, known
}

// refusedFromMemory is whether a refusal remembered from the last two seconds
// already answers a request of this class, and at which level. A request it
// would pass (somebody let in on a watch route, or a locked caller on a
// watch-or-admin route who was never found not to be an admin) is read as
// usual.
func refusedFromMemory(class routeClass, refusal accessRefusal) (access, bool) {
	switch class {
	case routeOpen:
		return accessLocked, false
	case routeWatch:
		return accessLocked, refusal.level == accessLocked
	case routeWatchOrAdmin:
		return accessLocked, refusal.level == accessLocked && refusal.notAdmin
	default:
		return refusal.level, refusal.level != accessLinked
	}
}

// passAccessGate is layer 1. True when the request may go on to the switch;
// otherwise it has answered.
//
// It runs before every meter, so a refused caller never spends a token they
// would need once unlocked, and a remembered refusal answers without a read,
// so a refused caller cannot make the store work for every request either.
func (p *Plugin) passAccessGate(w http.ResponseWriter, r *http.Request, userID string) bool {
	if underPath(r.URL.Path, adminPathRoot) {
		return true
	}

	class, known := routeClassOf(r)
	if class == routeOpen {
		return true
	}

	c := p.accessCache()
	now := c.now()

	if refusal, recent := c.refusedRecently(userID, now); recent {
		if level, refused := refusedFromMemory(class, refusal); refused {
			p.writeAccessRefusal(w, level)
			return false
		}
	}

	// A yes is remembered for the reads a playback or a page of pictures
	// makes many of, and for a path that is no route at all, so that a linked
	// person sending made-up paths costs one read per ten seconds.
	seen := r.Method == http.MethodGet && (class == routeWatch || class == routeWatchOrAdmin)
	if class == routeLinked && !known {
		seen = true
	}

	epoch := c.currentEpoch()
	level, _, _, err := p.accessRead(userID, seen)
	if err != nil {
		p.API.LogWarn("Could not read somebody's access to Matty", "error", err.Error())
		w.Header().Set("Cache-Control", "no-store")
		writeCodedError(w, http.StatusServiceUnavailable, accessStoreText(p.brandName()), accessCodeStore)

		return false
	}

	switch class {
	case routeWatch, routeWatchOrAdmin:
		if level != accessLocked {
			return true
		}

		// The System Console's preview pictures, for an admin with no token.
		// Asked only of somebody locked, so it costs a linked viewer nothing.
		notAdmin := false
		if class == routeWatchOrAdmin {
			if p.isSystemAdmin(userID) {
				return true
			}
			notAdmin = true
		}

		c.noteRefusal(userID, accessLocked, notAdmin, now, epoch)
		p.writeAccessRefusal(w, accessLocked)

		return false

	default:
		if level == accessLinked {
			return true
		}

		c.noteRefusal(userID, level, false, now, epoch)
		p.writeAccessRefusal(w, level)

		return false
	}
}
