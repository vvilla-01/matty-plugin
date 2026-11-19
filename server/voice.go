package main

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"sort"
	"strings"
	"time"

	"layeh.com/gumble/gumble"
	"layeh.com/gumble/gumbleutil"
)

// Who is in voice, read from the Mumble server.
//
// The plugin holds ONE Mumble client connection for this, parked wherever the
// server drops it, and it never moves. It does not need to: a Mumble client
// is sent every channel and every user connected to the SERVER, not only the
// ones beside it, so the roster is the whole channel tree and everybody in
// it. It hears nobody: Mumble sends it the audio of anybody who talks in the
// channel where it is parked (usually the root), and gumble drops that
// unheard, since no codec is registered to decode it.
//
// People who talk from Matty each have a Mumble connection of their own,
// made by this node for them; the registry of those (voice_phones.go) is
// how the roster knows which Mattermost account each one is.
//
// With the soundboard on (design/3.2.0.md §7), the connection also SENDS
// audio: the sounds people play, made in their browsers and checked on the
// server, shouted into the presser's own channel through a voice target while
// the account itself stays where it is parked. See sounds_play.go.
//
// Deliberately NOT built on the Matterbridge that already bridges chat. Its
// Mumble side turns arrivals and departures into prose chat messages ("Victor
// joined") for the one channel it sits in, and its HTTP API carries messages
// only. Rebuilding a roster from that stream would start every plugin restart
// knowing nothing, with no way to ask — an empty row on an evening when four
// people are in there is worse than no row at all.
//
// Besides Broadcast Box, this, the title lookups and Twitch are the only
// connections the server makes.

const (
	voiceDialTimeout = 10 * time.Second

	// A voice server that is down should not be dialled every five seconds for
	// the rest of the evening.
	voiceMinBackoff = 5 * time.Second
	voiceMaxBackoff = 5 * time.Minute

	// Why the connection ended, when it was a panic (recovery.go). The
	// stack is in the server log.
	voicePanicProblem = "the voice connection stopped on an internal error"
)

// errVoiceDialPanicked is a dial that panicked, which is a dial that failed.
var errVoiceDialPanicked = errors.New("dialling stopped on an internal error")

// voicePerson is one person in voice, as the panel receives them.
type voicePerson struct {
	// Mumble's number for this connection, which is how the voice server
	// itself tells two people apart while they are connected.
	Session uint32 `json:"session"`

	Name string `json:"name"`

	// The Mattermost account this is the same person as, when that could be
	// worked out: the account that joined, for somebody here through Matty,
	// else by the name rules below. The panel uses it to say "you", and to
	// offer the soundboard to whoever it names. The server never takes it as
	// leave to do anything: a press is matched again, by the same rule, from
	// Mumble's live state (sounds_play.go).
	UserID string `json:"userId,omitempty"`

	// The id of the channel they are in. Absent is the root, 0.
	Channel uint32 `json:"channel,omitempty"`

	// Muted, self muted, deafened, self deafened or suppressed: silent, for
	// whatever reason.
	Muted bool `json:"muted,omitempty"`

	// Deafened or self deafened: they hear nobody.
	Deaf bool `json:"deaf,omitempty"`

	// There through Matty, on a Mumble connection this node holds for them.
	Phone bool `json:"phone,omitempty"`
}

// voiceChannel is one channel of the voice server's tree.
type voiceChannel struct {
	// Always written, 0 included: 0 is the root.
	ID   uint32 `json:"id"`
	Name string `json:"name"`

	// The channel this one is under, 0 included; nil for the root alone.
	Parent *uint32 `json:"parent,omitempty"`

	// How deep in the tree: 0 for the root, 1 under it, and so on.
	Depth int `json:"depth"`

	// How many people the channel takes, when it has a limit of its own.
	Max uint32 `json:"max,omitempty"`
}

// voiceView is what rides the snapshot. Connected is the honest part: an empty
// list means nobody is in voice, and that is ONLY true when the plugin is
// actually connected and in a position to know.
type voiceView struct {
	Connected bool           `json:"connected"`
	Channels  []voiceChannel `json:"channels,omitempty"`
	People    []voicePerson  `json:"people,omitempty"`
}

type voiceState struct {
	connected bool
	channels  []voiceChannel
	people    []voicePerson

	// Why the last attempt failed, for the admin-only notice. Never sent to
	// anybody else, and never to anybody at all while the connection is up.
	problem string
}

func (p *Plugin) getVoice() voiceState {
	p.voiceLock.RLock()
	defer p.voiceLock.RUnlock()

	return p.voice
}

// voiceOf is the snapshot's view: nil when the feature is off, so the field
// disappears from the payload entirely rather than riding along as an empty
// object on every push for ever.
func (p *Plugin) voiceOf(cfg *configuration) *voiceView {
	if !cfg.EnableVoice {
		return nil
	}

	state := p.getVoice()
	if !state.connected {
		return &voiceView{}
	}

	return &voiceView{Connected: true, Channels: state.channels, People: state.people}
}

// What /access says about voice, from 4.3.0, while voice is on.
const (
	voiceTalkOn  = "on"
	voiceTalkOff = "off"

	voiceTalkSwitchedOffText = "Talking from Matty is switched off on this server."
)

// voiceWord is /access's voice word: whether a Matty may join a voice
// channel and talk from it here, or why not.
type voiceWord struct {
	// Talk is "on" or "off".
	Talk string `json:"talk"`

	// Reason is why not, while off.
	Reason string `json:"reason,omitempty"`
}

// voiceWordNow is /access's voice word, nil (left out) while voice is
// switched off. It reads nothing from the store: talk is on while its
// switch is, the voice port is open, the roster connection is connected and
// Mattermost is not a cluster (voiceTalkUnavailable), else off with why.
func (p *Plugin) voiceWordNow(cfg *configuration) *voiceWord {
	if !cfg.EnableVoice {
		return nil
	}

	if reason := p.voiceTalkUnavailable(cfg); reason != "" {
		return &voiceWord{Talk: voiceTalkOff, Reason: reason}
	}

	return &voiceWord{Talk: voiceTalkOn}
}

// beginVoiceEpoch marks the start of a connection attempt and returns its
// number. Every later attempt, and shutting down, makes the previous number
// stale.
//
// A COUNTER rather than a pointer comparison against the live client, because
// of the order gumble does things in: DialWithDialer blocks until the server
// has synchronised, and it fires onConnect on its read goroutine BEFORE it
// returns — so at the moment the first roster arrives, the plugin does not yet
// have a client to compare against and would throw that roster away.
func (p *Plugin) beginVoiceEpoch() uint64 {
	p.voiceLock.Lock()
	defer p.voiceLock.Unlock()

	p.voiceEpoch++

	return p.voiceEpoch
}

// currentVoiceEpoch reports whether an event belongs to the connection the
// plugin is actually holding.
//
// gumble dispatches its events inline on its own read goroutine, which the
// plugin has no handle on and cannot join. So a handler from a connection that
// has just been replaced, or abandoned, can still be mid-flight — and without
// this it would write its roster over the live one, or bring "connected" back
// after the plugin had let go.
func (p *Plugin) currentVoiceEpoch(epoch uint64) bool {
	p.voiceLock.RLock()
	defer p.voiceLock.RUnlock()

	return p.voiceEpoch == epoch
}

func (p *Plugin) setVoiceClient(client *gumble.Client) {
	p.voiceLock.Lock()
	p.voiceClient = client
	p.voiceLock.Unlock()
}

func (p *Plugin) setVoice(next voiceState) {
	p.voiceLock.Lock()
	p.voice = next
	p.voiceLock.Unlock()
}

// ---------------------------------------------------------------- the client

func (p *Plugin) restartVoice() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	// Swapped under the lock for the same reason the poller's is: two
	// concurrent restarts must not both read the same old connection and leave
	// one of them running.
	p.voiceRunLock.Lock()
	oldCancel, oldDone := p.voiceCancel, p.voiceDone
	p.voiceCancel = cancel
	p.voiceDone = done
	p.voiceRunLock.Unlock()

	if oldCancel != nil {
		oldCancel()
		<-oldDone
	}

	go p.runVoice(ctx, done)
}

func (p *Plugin) stopVoice() {
	p.voiceRunLock.Lock()
	cancel := p.voiceCancel
	done := p.voiceDone
	p.voiceCancel = nil
	p.voiceDone = nil
	p.voiceRunLock.Unlock()

	if cancel == nil {
		return
	}

	cancel()
	<-done
}

// runVoice keeps one connection up for as long as the plugin is activated and
// the feature is switched on.
func (p *Plugin) runVoice(ctx context.Context, done chan struct{}) {
	defer close(done)

	cfg := p.getConfiguration()
	if !cfg.needsMumble() {
		p.storeVoice(voiceState{})

		return
	}

	backoff := voiceMinBackoff

	for ctx.Err() == nil {
		// A connection that panics is let go by holdVoiceConnection's own
		// defers, and dialled again after the backoff, as one that dropped
		// (recovery.go). The locks it takes are all let go by a defer.
		var problem string
		var reached bool
		if p.safely("the voice connection", func() { problem, reached = p.holdVoiceConnection(ctx, cfg) }) {
			problem = voicePanicProblem
		}
		if ctx.Err() != nil {
			break
		}

		// A connection that stood up and then dropped starts the backoff
		// again. Without this a link that lasted all evening would come back
		// only after the five minutes earned by whatever brief trouble
		// happened hours ago, which is the opposite of what backoff is for.
		if reached {
			backoff = voiceMinBackoff
		}

		p.storeVoice(voiceState{problem: problem})
		p.publishRosterSoon()

		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > voiceMaxBackoff {
			backoff = voiceMaxBackoff
		}
	}

	p.beginVoiceEpoch()
	p.storeVoice(voiceState{})
}

// storeVoice writes a state the connection itself decided on — dropped,
// stopped — in turn with any roster being built, so a roster that finishes a
// moment later cannot put "connected" back over it. (It cannot anyway: the
// epoch is retired when a connection ends. This keeps the order obvious.)
func (p *Plugin) storeVoice(next voiceState) {
	p.voicePublishLock.Lock()
	defer p.voicePublishLock.Unlock()

	p.setVoice(next)
}

// holdVoiceConnection dials, stays until the connection ends, and returns why
// it ended and whether it ever stood up.
func (p *Plugin) holdVoiceConnection(ctx context.Context, cfg *configuration) (string, bool) {
	config := gumble.NewConfig()
	config.Username = cfg.MumbleUsername
	config.Password = cfg.MumblePassword

	ended := make(chan string, 1)
	finish := func(reason string) {
		select {
		case ended <- reason:
		default:
		}
	}

	// Claimed before the dial, because gumble fires Connect before
	// DialWithDialer returns.
	epoch := p.beginVoiceEpoch()

	// Every callback reads the CURRENT configuration rather than closing over
	// the one this connection was dialled with.
	//
	// The connection is long-lived and is deliberately not rebuilt for settings
	// that do not change where it dials — so a captured copy would be the
	// settings as they were when Mumble was last connected to, possibly days
	// ago. Switching the chat bridge on would then do nothing at all until
	// something unrelated happened to force a re-dial, which is exactly the
	// bug this comment exists to stop somebody reintroducing.
	//
	// They run on gumble's own goroutine, which reads what the voice server
	// sends, so each is run safely (recovery.go): a panic over one event is
	// that event lost, not the plugin.
	config.Attach(gumbleutil.Listener{
		Connect: func(event *gumble.ConnectEvent) {
			p.safely("a voice connect event", func() {
				if p.currentVoiceEpoch(epoch) {
					p.noteMumbleBandwidth(event.MaximumBitrate)
				}
				p.publishVoice(event.Client, p.getConfiguration(), epoch)
			})
		},
		ServerConfig: func(event *gumble.ServerConfigEvent) {
			p.safely("a voice server config event", func() {
				if p.currentVoiceEpoch(epoch) && event.MaximumBitrate != nil {
					p.noteMumbleBandwidth(event.MaximumBitrate)
				}
			})
		},
		UserChange: func(event *gumble.UserChangeEvent) {
			p.safely("a voice user event", func() { p.publishVoice(event.Client, p.getConfiguration(), epoch) })
		},
		// A channel made, removed, renamed, moved, reordered or given another
		// limit. Changes nobody sees (a description) are dropped by the
		// comparison in publishVoiceLocked.
		ChannelChange: func(event *gumble.ChannelChangeEvent) {
			p.safely("a voice channel event", func() { p.publishVoice(event.Client, p.getConfiguration(), epoch) })
		},
		TextMessage: func(event *gumble.TextMessageEvent) {
			p.safely("a Mumble chat message", func() {
				if p.currentVoiceEpoch(epoch) {
					p.relayFromMumble(event, p.getConfiguration())
				}
			})
		},
		Disconnect: func(event *gumble.DisconnectEvent) {
			finish(disconnectReason(event))
		},
	})

	// Mumble servers use a self-signed certificate as a matter of course — the
	// desktop client trusts them on first use and remembers the fingerprint.
	// There is no certificate authority to check one against, so verification
	// is off. What that costs is bounded and worth stating: on a network where
	// somebody can already intercept traffic, they could impersonate the voice
	// server and collect the server password set below. Nothing else crosses
	// this connection — no Mattermost credential and no stream key — but,
	// with the soundboard on, the sounds people play, which are theirs to
	// hear anyway.
	tlsConfig := &tls.Config{InsecureSkipVerify: true} //nolint:gosec // see above

	// Dialled on its own goroutine so that deactivating the plugin, or saving
	// a setting, does not wait out a ten-second connect to a machine that is
	// switched off. OnDeactivate has custom statuses to hand back and a poller
	// to stop; it must not be stuck here.
	type dialed struct {
		client *gumble.Client
		err    error
	}

	// Counted with the phone sessions' connections against the budget that
	// keeps the voice server's autoban off this node (voice_sessions.go).
	// The roster connection itself is never refused.
	p.voiceTalk().noteRosterDial()

	attempt := make(chan dialed, 1)
	go func() {
		var client *gumble.Client
		var err error
		if p.safely("dialling the voice server", func() {
			client, err = gumble.DialWithDialer(
				&net.Dialer{Timeout: voiceDialTimeout},
				withDefaultPort(cfg.MumbleServer, "64738"),
				config,
				tlsConfig,
			)
		}) {
			client, err = nil, errVoiceDialPanicked
		}
		attempt <- dialed{client, err}
	}()

	var client *gumble.Client

	select {
	case <-ctx.Done():
		// Abandoned. Whatever the dial produces is closed by the goroutine
		// below rather than left holding a socket open.
		p.goSafely("disconnecting an abandoned voice dial", func() {
			if late := <-attempt; late.client != nil {
				_ = late.client.Disconnect()
			}
		})

		return "", false
	case outcome := <-attempt:
		if outcome.err != nil {
			p.API.LogWarn("Could not reach the voice server", "error", outcome.err.Error())

			return "could not connect: " + outcome.err.Error(), false
		}
		client = outcome.client
	}

	// Published before anything else can look at it. gumble may already be
	// dispatching Connect on its read goroutine, and the staleness check in
	// publishVoice would throw that first roster away.
	p.setVoiceClient(client)

	// The epoch is retired with the client, so anything still working from
	// this connection — a roster being rebuilt after a settings change, a late
	// event — is thrown away rather than written over the dropped state.
	defer func() {
		p.setVoiceClient(nil)
		p.beginVoiceEpoch()
		_ = client.Disconnect()
	}()

	// The soundboard's player lives exactly as long as the connection, and
	// is stopped before it goes (deferred calls run last first), so a sound
	// never outlives its connection or its epoch. Started whether or not the
	// soundboard is on: it does nothing until a press hands it a sound, and
	// switching the soundboard on must not need a reconnect.
	player := p.startSoundPlayer(client)
	defer player.stop()

	// What VillaNET's own session can do, for the admins' line, once the
	// connection is up.
	if p.getConfiguration().soundboardOn() {
		p.checkSoundBot()
	}

	select {
	case <-ctx.Done():
		return "", true
	case reason := <-ended:
		return reason, true
	}
}

func disconnectReason(event *gumble.DisconnectEvent) string {
	switch {
	case event.Type.Has(gumble.DisconnectKicked):
		return "the voice server kicked the plugin"
	case event.Type.Has(gumble.DisconnectBanned):
		return "the voice server banned the plugin"
	case event.String != "":
		return event.String
	default:
		return "the voice connection dropped"
	}
}

// withDefaultPort lets an admin type a bare host, which is what they will do.
func withDefaultPort(address, port string) string {
	address = strings.TrimSpace(address)
	if _, _, err := net.SplitHostPort(address); err == nil {
		return address
	}

	return net.JoinHostPort(address, port)
}

// publishVoice rebuilds the roster and pushes it, but only when the panel would
// see a difference.
//
// Mumble is chatty in ways the panel does not care about — a comment change,
// somebody's texture — and every one of those arrives here. Comparing first
// keeps those from becoming a WebSocket message to every client each. A change
// the panel does show, a self-mute included now that muted people are dimmed,
// goes out without a poll of Broadcast Box, and a run of them as one message
// (publishRosterSoon).
func (p *Plugin) publishVoice(client *gumble.Client, cfg *configuration, epoch uint64) {
	p.voicePublishLock.Lock()
	defer p.voicePublishLock.Unlock()

	p.publishVoiceLocked(client, cfg, epoch)
}

// publishVoiceLocked is publishVoice for a caller already holding
// voicePublishLock. Held across the whole of it — the staleness check, the
// roster, the comparison and the store — so two builds cannot interleave.
//
// It cannot deadlock with gumble: gumble lets go of its own lock before it
// calls the listeners that land here, and rosterOf takes that lock only for
// the moment it reads the user list.
func (p *Plugin) publishVoiceLocked(client *gumble.Client, cfg *configuration, epoch uint64) {
	if !p.currentVoiceEpoch(epoch) {
		return
	}

	if !cfg.EnableVoice {
		// Connected for the text bridge alone. Nothing to show, but the
		// connection is real, so an admin is not told it is broken.
		p.setVoice(voiceState{connected: true})

		return
	}

	channels := voiceChannelsOf(client)
	people := p.rosterOf(client, cfg)

	previous := p.getVoice()
	if previous.connected && sameChannels(previous.channels, channels) && sameRoster(previous.people, people) {
		return
	}

	p.setVoice(voiceState{connected: true, channels: channels, people: people})
	p.publishRosterSoon()
}

// republishVoice rebuilds the roster on the connection already held, for a
// change of settings that alters it without changing where it connects.
//
// It takes its turn first and only THEN reads the client, the epoch and the
// settings — all as they are now, not as they were when it was asked. Two
// saves in quick succession therefore both end on the later rules, and a
// connection that has dropped in the meantime has retired its epoch, so
// nothing stale is written.
func (p *Plugin) republishVoice() {
	p.voicePublishLock.Lock()
	defer p.voicePublishLock.Unlock()

	p.voiceLock.RLock()
	client, epoch := p.voiceClient, p.voiceEpoch
	p.voiceLock.RUnlock()

	if client == nil {
		return
	}

	p.publishVoiceLocked(client, p.getConfiguration(), epoch)
}

// rosterOf reads the connected users, in a stable order, without the bots.
func (p *Plugin) rosterOf(client *gumble.Client, cfg *configuration) []voicePerson {
	hidden := cfg.hiddenVoiceNames()
	overrides := cfg.voiceNameMap()

	type raw struct {
		session uint32
		name    string
		exact   string
		channel uint32
		muted   bool
		deaf    bool
	}

	var found []raw

	// Under the client's own lock: the read goroutine changes this map as
	// events arrive, and ranging it without this is a data race.
	client.Do(func() {
		for session, user := range client.Users {
			if user == nil {
				continue
			}

			name := strings.TrimSpace(user.Name)
			if name == "" || hidden[strings.ToLower(name)] {
				continue
			}

			var channel uint32
			if user.Channel != nil {
				channel = user.Channel.ID
			}

			// Suppressed too: somebody Mumble will not let speak in their
			// channel is as silent as somebody muted, and the soundboard
			// refuses them the same way (§7.15).
			found = append(found, raw{
				session: session,
				name:    name,
				exact:   user.Name,
				channel: channel,
				muted:   user.Muted || user.SelfMuted || user.Deafened || user.SelfDeafened || user.Suppressed,
				deaf:    user.Deafened || user.SelfDeafened,
			})
		}
	})

	// The plugin's own connection is in that list, and it is furniture rather
	// than a person. Hidden by the name it connected under, so an admin who
	// renames it in the settings does not have to remember to hide it too.
	own := strings.ToLower(strings.TrimSpace(cfg.MumbleUsername))

	people := make([]voicePerson, 0, len(found))
	for _, entry := range found {
		if own != "" && strings.EqualFold(entry.name, own) {
			continue
		}

		person := voicePerson{
			Session: entry.session,
			Name:    entry.name,
			Channel: entry.channel,
			Muted:   entry.muted,
			Deaf:    entry.deaf,
		}

		// Somebody here through Matty is the account that joined, whatever
		// name the voice server knows them by. Asked before the name rules,
		// which would match that name to nobody, or to somebody else.
		if userID, ok := p.voicePhones.userOf(entry.session); ok {
			person.UserID, person.Phone = userID, true
		} else {
			person.UserID = p.voiceUserID(entry.exact, overrides, cfg)
		}

		people = append(people, person)
	}

	// A map has no order, and an order that changes every poll would republish
	// the snapshot for ever.
	sort.Slice(people, func(i, j int) bool {
		left, right := strings.ToLower(people[i].Name), strings.ToLower(people[j].Name)
		if left != right {
			return left < right
		}
		if people[i].Name != people[j].Name {
			return people[i].Name < people[j].Name
		}

		return people[i].Session < people[j].Session
	})

	return people
}

// voiceChannelsOf reads the voice server's channel tree in the order a
// Mumble client draws it: every channel before the channels under it, and
// channels beside each other by their position and then by name. A channel
// whose parent is not in the tree is left out, with everything under it.
func voiceChannelsOf(client *gumble.Client) []voiceChannel {
	type raw struct {
		id       uint32
		name     string
		parent   uint32
		root     bool
		max      uint32
		position int32
	}

	var found []raw

	client.Do(func() {
		for id, channel := range client.Channels {
			if channel == nil {
				continue
			}

			entry := raw{id: id, name: channel.Name, max: channel.MaxUsers, position: channel.Position}
			if channel.Parent == nil {
				entry.root = true
			} else {
				entry.parent = channel.Parent.ID
			}
			found = append(found, entry)
		}
	})

	if len(found) == 0 {
		return nil
	}

	// Beside each other: by position, then by name in any case, then by name
	// exactly, then by id, so that two channels with the same name and place
	// still come out in the same order every time.
	sort.Slice(found, func(i, j int) bool {
		a, b := found[i], found[j]
		if a.position != b.position {
			return a.position < b.position
		}
		if left, right := strings.ToLower(a.name), strings.ToLower(b.name); left != right {
			return left < right
		}
		if a.name != b.name {
			return a.name < b.name
		}

		return a.id < b.id
	})

	children := map[uint32][]raw{}
	var roots []raw
	for _, entry := range found {
		if entry.root {
			roots = append(roots, entry)
		} else {
			children[entry.parent] = append(children[entry.parent], entry)
		}
	}

	channels := make([]voiceChannel, 0, len(found))
	seen := map[uint32]bool{}

	var walk func(entry raw, depth int)
	walk = func(entry raw, depth int) {
		// A tree that loops back on itself is a server telling nonsense:
		// each channel is listed once, where it was first reached.
		if seen[entry.id] {
			return
		}
		seen[entry.id] = true

		channel := voiceChannel{ID: entry.id, Name: entry.name, Depth: depth, Max: entry.max}
		if !entry.root {
			parent := entry.parent
			channel.Parent = &parent
		}
		channels = append(channels, channel)

		for _, child := range children[entry.id] {
			walk(child, depth+1)
		}
	}

	for _, root := range roots {
		walk(root, 0)
	}

	return channels
}

// voiceUserID matches a Mumble name to a Mattermost account by
// voiceNameMatches's rule, which the headphones and the soundboard share so
// that they can never disagree about who somebody is.
func (p *Plugin) voiceUserID(name string, overrides map[string]string, cfg *configuration) string {
	candidate := voiceCandidate(name, overrides, cfg)
	if candidate == "" {
		return ""
	}

	user, err := p.API.GetUserByUsername(candidate)
	if err != nil || user == nil || user.DeleteAt != 0 || !voiceNameMatches(name, user.Username, overrides, cfg) {
		return ""
	}

	return user.Id
}

// voiceNameMatches is whether a Mumble name, exactly as Mumble sent it,
// belongs to the Mattermost account with this username: through Names that
// differ, else, with Match names to usernames on, by being that username.
//
// It used to say only "you" in the panel, and a wrong match granted nothing.
// From 3.2.0 a match also decides into which Mumble channel a linked person's
// sound plays (sounds_play.go), so it is stricter, for the headphones too:
//
//   - By username, only a name that is already plain letters, digits, dots,
//     dashes and underscores matches, lowered ASCII-only. Mattermost
//     usernames are only those characters, so no honest match is lost, and a
//     lookalike such as "vİctor" (a Turkish dotted I), the Kelvin sign or a
//     fullwidth letter matches nobody. Go's strings.ToLower maps some of
//     those onto plain letters where Murmur's own clash test, which lowers
//     with Qt, does not, so they could sit in Mumble beside the real name.
//   - By Names that differ, the admin's Mumble name is compared folded
//     ASCII-only, and every other character must be the one they typed.
//   - Hidden names and VillaNET's own never match.
//
// What a match proves, and what it does not, is in design/3.2.0.md §7.3: an
// unregistered Mumble name is first come, first served, and on a default
// Mumble server anybody may register a free one. A match lets nobody act as
// somebody else in Mattermost; it only lets a linked person play into the
// channel where a session with their name sits, and every play is logged with
// their account.
func voiceNameMatches(name, username string, overrides map[string]string, cfg *configuration) bool {
	candidate := voiceCandidate(name, overrides, cfg)

	return candidate != "" && candidate == username
}

// voiceCandidate is the Mattermost username a Mumble name stands for by that
// rule, lowered ASCII-only, or "" for nobody.
func voiceCandidate(name string, overrides map[string]string, cfg *configuration) string {
	if name == "" || cfg.hiddenVoiceNames()[strings.ToLower(strings.TrimSpace(name))] {
		return ""
	}
	if own := strings.TrimSpace(cfg.MumbleUsername); own != "" && strings.EqualFold(strings.TrimSpace(name), own) {
		return ""
	}

	candidate := ""
	if mapped := overrides[asciiFold(name)]; mapped != "" {
		candidate = mapped
	} else if cfg.VoiceMatchUsernames {
		candidate = name
	}

	if !plainUsername(candidate) {
		return ""
	}

	return asciiFold(candidate)
}

// plainUsername is a name made only of what a Mattermost username may hold,
// [A-Za-z0-9._-], 1 to 64 of them.
func plainUsername(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}

	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
		default:
			return false
		}
	}

	return true
}

// sameChannels is whether two channel trees would be drawn the same.
func sameChannels(a, b []voiceChannel) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		left, right := a[i], b[i]
		if left.ID != right.ID || left.Name != right.Name || left.Depth != right.Depth || left.Max != right.Max {
			return false
		}
		if (left.Parent == nil) != (right.Parent == nil) || (left.Parent != nil && *left.Parent != *right.Parent) {
			return false
		}
	}

	return true
}

func sameRoster(a, b []voicePerson) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}
