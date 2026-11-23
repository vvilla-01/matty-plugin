package main

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"layeh.com/gumble/gumble"
)

// Playing a sound into Mumble (design/3.2.0.md §7.4, §7.5, §7.10).
//
// A sound plays into the presser's own Mumble channel, as a shout: voice
// target 1 names that channel, and VillaNET's account stays wherever Mumble
// parked it, so nobody sees it arrive and it starts receiving nobody's audio.
// Everybody in that channel who is not deafened hears it, the presser
// included; nobody elsewhere does.
//
// The packets are the browser's own Opus, checked when they were added
// (sounds_check.go), written straight onto gumble's connection with its
// exported Conn.WriteAudio: no codec is registered, and none is needed to send
// what is already encoded. gumble's own sending path would want one, and
// gumble's only Opus codec needs cgo. One packet every 20 ms, in real time,
// which keeps them under Murmur's per-sender bandwidth meter; the sequence
// counts 10 ms units, as Mumble's own client does, so +2 a packet.
//
// Who may press is read from gumble's live state, never the published roster,
// which lags up to two seconds: at the press, again in full before the first
// packet, from which the target channel is taken, and the presser's session
// again before every packet after. A presser who mutes, deafens, is
// suppressed, leaves or moves channel stops their own sound there: Victor's
// rule is about not abusing the sounds, and a sound that follows its presser
// out of the room, or plays on after they mute, is that abuse. Grey tiles in
// the panel are a courtesy; this is the rule.

const (
	// wsEventSound reaches the panel as custom_ceo.villa.matty_sound.
	wsEventSound = "sound"

	// The voice target a sound is shouted through, and the legacy UDPTunnel
	// format number for Opus (Murmur reads tunnelled audio in the legacy
	// format from a client below 1.5, and gumble says it is 1.3).
	soundVoiceTarget = 1
	soundOpusFormat  = 4

	// Mumble's client counts frames through silence for five seconds, then
	// starts its sequence again.
	soundSequenceReset = 5 * time.Second

	// The longest a press waits to learn whether VillaNET may whisper in the
	// presser's channel, outside every lock.
	soundWhisperWait = time.Second

	// How long stopping the player waits for a packet being written.
	soundStopWait = 2 * time.Second
)

// mumblePermissions is how the soundboard asks what VillaNET may do in a
// channel: gumble's own, unless a test hands in a stand-in, because a Channel
// built outside gumble cannot answer.
type mumblePermissions interface {
	// whisperIn is whether VillaNET may whisper in the channel, and whether
	// that is known yet. Called inside client.Do.
	whisperIn(channel *gumble.Channel) (has, known bool)

	// ask asks Mumble to say, when it is not known.
	ask(channel *gumble.Channel)
}

type gumblePermissions struct{}

func (gumblePermissions) whisperIn(channel *gumble.Channel) (bool, bool) {
	permission := channel.Permission()
	if permission == nil {
		return false, false
	}

	return permission.Has(gumble.PermissionWhisper), true
}

func (gumblePermissions) ask(channel *gumble.Channel) {
	channel.RequestPermission()
}

// voiceRead is §7.5 step 4: what gumble says of a presser and of VillaNET's
// own session, read in one client.Do.
type voiceRead struct {
	// "" when they may play, or why not.
	code string

	// Matched to a Mumble session now, in any state, two included.
	in bool

	// VillaNET's own session can speak, and if not, the admins' line.
	botOK   bool
	botLine string

	// The presser's one session, its channel, and whether VillaNET may
	// whisper there and whether that is known.
	session      uint32
	channel      *gumble.Channel
	channelID    uint32
	channelName  string
	whisperKnown bool
	whisperHas   bool
}

// botCanSpeak is whether the plugin's own Mumble session can be heard, and the
// admins' line when it cannot. SuperUser is told by its name, never by a
// UserID of 0, which an unregistered account has as well: only SuperUser's
// password logs that name in, and Mumble never lets SuperUser speak.
func botCanSpeak(self *gumble.User) (bool, string) {
	switch {
	case self == nil:
		return false, "The soundboard can't play: " + productName + " isn't connected to Mumble."
	case asciiFold(strings.TrimSpace(self.Name)) == "superuser":
		return false, "The soundboard can't play: " + productName + "'s Mumble account has logged in as SuperUser, which Mumble never lets speak."
	case self.Muted || self.SelfMuted:
		return false, "The soundboard can't play: " + productName + "'s Mumble account is muted on the Mumble server."
	case self.Deafened || self.SelfDeafened:
		return false, "The soundboard can't play: " + productName + "'s Mumble account is deafened on the Mumble server."
	case self.Suppressed:
		return false, "The soundboard can't play: " + productName + "'s Mumble account isn't allowed to speak where it sits."
	}

	return true, ""
}

// voicePresser is who a press is read for: the Mattermost account and its
// username, this node's phone sessions (voice_phones.go), nil for none, and
// the channels the roster shows (Channels to show), nil for every one.
type voicePresser struct {
	userID   string
	username string
	phones   *voicePhoneRegistry
	shown    map[uint32]bool
}

// owns is whether a Mumble session is the presser's, by the rule the roster
// shows people by: a phone session this node holds is the account that
// joined, whatever its name; any other is theirs by voiceNameMatches.
func (who voicePresser) owns(user *gumble.User, overrides map[string]string, cfg *configuration) bool {
	if who.phones != nil {
		if owner, ok := who.phones.userOf(user.Session); ok {
			return owner == who.userID
		}
	}

	return voiceNameMatches(user.Name, who.username, overrides, cfg)
}

// sees is whether a session is in a channel the roster shows. Somebody in a
// channel that is not shown is not in voice as far as the panel and Matty
// are told, so the soundboard neither finds them there nor plays into it.
func (who voicePresser) sees(user *gumble.User) bool {
	if who.shown == nil {
		return true
	}

	var channel uint32
	if user.Channel != nil {
		channel = user.Channel.ID
	}

	return who.shown[channel]
}

// readVoice is the presser's state and VillaNET's, from gumble's live state in
// one client.Do: their sessions by voicePresser's rule (none, not in voice;
// two, ambiguous: one person, one Mumble client); that one session's
// deafened, muted, suppressed and registered state, a phone session's as the
// voice server reports it; VillaNET's own; and, when gumble already knows
// it, Whisper in their channel.
func readVoice(client *gumble.Client, who voicePresser, cfg *configuration, perms mumblePermissions) voiceRead {
	overrides := cfg.voiceNameMap()
	read := voiceRead{}

	client.Do(func() {
		var matched []*gumble.User
		for _, user := range client.Users {
			if user == nil || user == client.Self {
				continue
			}
			if who.owns(user, overrides, cfg) && who.sees(user) {
				matched = append(matched, user)
			}
		}

		read.botOK, read.botLine = botCanSpeak(client.Self)

		switch len(matched) {
		case 0:
			read.code = soundCodeNotInVoice
			return
		case 1:
		default:
			read.in = true
			read.code = soundCodeTwoSessions

			return
		}

		one := matched[0]
		read.in = true
		read.session = one.Session
		if one.Channel != nil {
			read.channel = one.Channel
			read.channelID = one.Channel.ID
			read.channelName = one.Channel.Name
		}

		// Deafened before muted: deafening mutes too, and "unmute" would
		// be no use to somebody deafened.
		switch {
		case one.Deafened || one.SelfDeafened:
			read.code = soundCodeDeafened
		case one.Muted || one.SelfMuted:
			read.code = soundCodeMuted
		case one.Suppressed:
			read.code = soundCodeSuppressed
		case cfg.SoundsRegisteredOnly && !one.IsRegistered():
			read.code = soundCodeNotRegistered
		case read.channel == nil:
			read.code = soundCodeNotInVoice
		case !read.botOK:
			read.code = soundCodeBotCannot
		default:
			read.whisperHas, read.whisperKnown = perms.whisperIn(read.channel)
			if read.whisperKnown && !read.whisperHas {
				read.code = soundCodeBotCannot
			}
		}
	})

	return read
}

// currentVoiceClient is the Mumble connection held now, and its player.
func (p *Plugin) currentVoiceClient() (*gumble.Client, *soundPlayer) {
	p.voiceLock.RLock()
	defer p.voiceLock.RUnlock()

	return p.voiceClient, p.soundPlayer
}

// voiceStateOf is readVoice for a Mattermost account, on the connection held
// now: bot_offline with none, store when the account cannot be read.
func (p *Plugin) voiceStateOf(userID string, cfg *configuration) voiceRead {
	client, _ := p.currentVoiceClient()
	if client == nil {
		return voiceRead{code: soundCodeBotOffline}
	}

	user, appErr := p.API.GetUser(userID)
	if appErr != nil || user == nil {
		return voiceRead{code: soundCodeStore}
	}

	read := readVoice(client, p.presser(userID, user.Username), cfg, p.sounds().permissions())
	p.noteSoundRead(read)

	return read
}

// presser is a Mattermost account as a press is read for it.
func (p *Plugin) presser(userID, username string) voicePresser {
	return voicePresser{userID: userID, username: username, phones: &p.voicePhones, shown: p.shownVoiceChannelIDs()}
}

// permissions is the stand-in a test gave, or gumble's.
func (st *soundStore) permissions() mumblePermissions {
	st.lock.Lock()
	defer st.lock.Unlock()

	if st.perms != nil {
		return st.perms
	}

	return gumblePermissions{}
}

// noteSoundRead keeps what a read found wrong with VillaNET's own session, for
// the admins' line.
func (p *Plugin) noteSoundRead(read voiceRead) {
	st := p.sounds()
	st.lock.Lock()
	defer st.lock.Unlock()

	if read.botOK {
		st.botLine = ""
	} else if read.botLine != "" {
		st.botLine = read.botLine
	}

	if read.code == soundCodeBotCannot && read.botOK && read.whisperKnown && !read.whisperHas {
		st.whisperLine = fmt.Sprintf("The soundboard can't play into %s: "+productName+"'s Mumble account lacks Whisper there.", read.channelName)
	}
}

// noteMumbleBandwidth keeps the admins' line about the Mumble server's
// bandwidth limit, from what it said when the connection came up. Sounds need
// 64 kbit/s (128-byte packets, 32 bytes of overhead each, 50 a second).
func (p *Plugin) noteMumbleBandwidth(bitsPerSecond *int) {
	line := ""
	if bitsPerSecond != nil && *bitsPerSecond > 0 && *bitsPerSecond < 64000 {
		line = fmt.Sprintf("Mumble's bandwidth limit (%d kbit/s) is below the 64 kbit/s sounds need; they may be cut off.", *bitsPerSecond/1000)
	}

	st := p.sounds()
	st.lock.Lock()
	st.bandwidthLine = line
	st.lock.Unlock()
}

// checkSoundBot is a fresh look at the plugin's own session, for the admins'
// line when the connection comes up and when an admin presses Check again.
func (p *Plugin) checkSoundBot() (bool, string) {
	client, _ := p.currentVoiceClient()
	if client == nil {
		return false, "The soundboard can't play: " + productName + " isn't connected to Mumble."
	}

	var ok bool
	var line string
	client.Do(func() {
		ok, line = botCanSpeak(client.Self)
	})

	st := p.sounds()
	st.lock.Lock()
	st.botLine = line
	if !ok {
		st.lock.Unlock()
		return false, line
	}
	if st.whisperLine != "" {
		line = st.whisperLine
	} else {
		line = st.bandwidthLine
	}
	st.lock.Unlock()

	return true, line
}

// soundAlertFor is /me's admin-only soundAlert: why the soundboard cannot
// play, most important first, or "" with nothing wrong or the soundboard off.
func (p *Plugin) soundAlertFor(cfg *configuration) string {
	if !cfg.soundboardOn() {
		return ""
	}

	st := p.sounds()
	st.lock.Lock()
	defer st.lock.Unlock()

	for _, line := range []string{st.botLine, st.whisperLine, st.bandwidthLine} {
		if line != "" {
			return line
		}
	}

	return ""
}

// soundJob is one admitted press, handed to the player.
type soundJob struct {
	userID   string
	username string
	soundID  string
	packets  [][]byte
	ticket   gateTicket
	cooldown time.Duration

	// The channel the press found the presser in: a sound is never played
	// into a channel they were not in when they pressed.
	channelID uint32

	// The player's word: the first packet is on the wire, or why not.
	// Buffered, so the player never waits on a handler that has gone.
	started chan soundStart
}

type soundStart struct {
	code      string
	startedAt time.Time
	endsAt    time.Time
	channel   string
}

// soundPlayer plays one sound at a time on one Mumble connection, and dies
// with it.
type soundPlayer struct {
	plugin *Plugin
	client *gumble.Client

	// Unbuffered, and sent to only with a select that does not wait: a job
	// is taken only while the player is idle, and nothing ever queues.
	jobs chan *soundJob

	cancel context.CancelFunc
	done   chan struct{}
	ctx    context.Context

	stopOnce sync.Once

	// Only the player's own goroutine touches these.
	targetSent    bool
	targetChannel uint32
	sentAny       bool
	lastSeq       int64
	lastPacketAt  time.Time
}

// startSoundPlayer starts the player for a connection that has just come up,
// and makes it the one presses are handed to.
func (p *Plugin) startSoundPlayer(client *gumble.Client) *soundPlayer {
	ctx, cancel := context.WithCancel(context.Background())
	player := &soundPlayer{
		plugin: p,
		client: client,
		jobs:   make(chan *soundJob),
		cancel: cancel,
		done:   make(chan struct{}),
		ctx:    ctx,
	}

	p.voiceLock.Lock()
	p.soundPlayer = player
	p.voiceLock.Unlock()

	go player.run()

	return player
}

// stop ends the player, a sound playing included, and waits for it; and it is
// no longer the one presses are handed to. A player stuck in a write to a
// server that has stopped reading (gumble sets no write deadline) is freed by
// closing the connection under it, which is going anyway.
func (pl *soundPlayer) stop() {
	pl.stopOnce.Do(func() {
		p := pl.plugin
		p.voiceLock.Lock()
		if p.soundPlayer == pl {
			p.soundPlayer = nil
		}
		p.voiceLock.Unlock()

		pl.cancel()

		select {
		case <-pl.done:
		case <-time.After(soundStopWait):
			if pl.client.Conn != nil {
				_ = pl.client.Conn.Close()
			}
			<-pl.done
		}
	})
}

func (pl *soundPlayer) run() {
	defer close(pl.done)

	for {
		select {
		case <-pl.ctx.Done():
			return
		case job := <-pl.jobs:
			// A sound that panics frees the gate as one that ended, and a
			// press still waiting for its start is told it could not be
			// played (recovery.go). Its locks are let go by a defer or held
			// only around an assignment; gumble's are let go by defers.
			if pl.plugin.safely("playing a sound", func() { pl.play(job) }) {
				pl.plugin.sounds().gate.ended(job.ticket, time.Now(), job.cooldown)
				select {
				case job.started <- soundStart{code: soundCodeBotOffline}:
				default:
				}
			}
		}
	}
}

// presserStillThere is the check before every packet after the first: the
// same session, in the same channel, not muted, deafened or suppressed, in a
// channel the roster shows, and still theirs, by name or as their phone
// session. A read lock over gumble's maps, cheap at fifty a second;
// WriteAudio takes the connection's own lock separately.
func (pl *soundPlayer) presserStillThere(job *soundJob, session, channelID uint32, cfg *configuration, overrides map[string]string) bool {
	there := false
	who := pl.plugin.presser(job.userID, job.username)

	pl.client.Do(func() {
		user := pl.client.Users[session]
		if user == nil || user.Channel == nil || user.Channel.ID != channelID {
			return
		}
		if user.Muted || user.SelfMuted || user.Deafened || user.SelfDeafened || user.Suppressed {
			return
		}

		there = who.owns(user, overrides, cfg) && who.sees(user)
	})

	return there
}

// play is one sound (§7.10).
func (pl *soundPlayer) play(job *soundJob) {
	p := pl.plugin
	st := p.sounds()
	cfg := p.getConfiguration()

	// In full again before the first packet, and the target is the channel
	// THIS read finds: a presser who muted since the press puts nothing on
	// the wire, and one who moved channel is refused rather than played into
	// a channel they did not press in, the one they left least of all.
	read := readVoice(pl.client, p.presser(job.userID, job.username), cfg, st.permissions())
	p.noteSoundRead(read)
	switch {
	case read.code == "" && read.channel == nil:
		read.code = soundCodeNotInVoice
	case read.code == "" && read.channelID != job.channelID:
		read.code = soundCodeMoved
	}
	if read.code != "" {
		st.gate.undo(job.ticket)
		job.started <- soundStart{code: read.code}

		return
	}

	if !pl.targetSent || pl.targetChannel != read.channelID {
		target := &gumble.VoiceTarget{ID: soundVoiceTarget}
		target.AddChannel(read.channel, false, false, "")
		pl.client.Send(target)
		pl.targetSent, pl.targetChannel = true, read.channelID
	}

	// The sequence carries on through a short silence, as Mumble's own
	// client counts it, and starts again after five seconds of it.
	now := time.Now()
	sequence := int64(0)
	if pl.sentAny && now.Sub(pl.lastPacketAt) < soundSequenceReset {
		sequence = pl.lastSeq + int64(math.Round(float64(now.Sub(pl.lastPacketAt))/float64(10*time.Millisecond)))
	}

	last := len(job.packets) - 1
	if err := pl.client.Conn.WriteAudio(soundOpusFormat, soundVoiceTarget, sequence, last == 0, job.packets[0], nil, nil, nil); err != nil {
		p.API.LogWarn("Could not send a sound to Mumble", "user_id", job.userID, "sound_id", job.soundID, "error", err.Error())
		st.gate.undo(job.ticket)
		job.started <- soundStart{code: soundCodeBotOffline}

		return
	}
	pl.sentAny, pl.lastSeq, pl.lastPacketAt = true, sequence, now

	endsAt := now.Add(time.Duration(len(job.packets)) * soundFrameMs * time.Millisecond)
	st.gate.started(job.ticket, endsAt, job.cooldown)
	st.lock.Lock()
	st.whisperLine = ""
	st.lock.Unlock()

	job.started <- soundStart{startedAt: now, endsAt: endsAt, channel: read.channelName}
	p.sendSoundEvent(endsAt.Add(soundGapAfter))

	overrides := cfg.voiceNameMap()
	ticker := time.NewTicker(soundFrameMs * time.Millisecond)
	defer ticker.Stop()

	sent := 1
packets:
	for i := 1; i <= last; i++ {
		// A connection that goes, or a deactivation, stops the sound
		// where it is: nothing outlives its connection.
		select {
		case <-pl.ctx.Done():
			break packets
		case <-ticker.C:
		}

		final := i == last
		if !pl.presserStillThere(job, read.session, read.channelID, cfg, overrides) {
			final = true
		}

		sequence += 2
		at := time.Now()
		if err := pl.client.Conn.WriteAudio(soundOpusFormat, soundVoiceTarget, sequence, final, job.packets[i], nil, nil, nil); err != nil {
			p.API.LogWarn("Could not send a sound to Mumble; it stopped there", "user_id", job.userID, "sound_id", job.soundID, "error", err.Error())
			break packets
		}
		pl.lastSeq, pl.lastPacketAt = sequence, at
		sent++

		if final {
			break packets
		}
	}

	ended := time.Now()
	st.gate.ended(job.ticket, ended, job.cooldown)
	p.sendSoundEvent(ended.Add(soundGapAfter))

	p.API.LogInfo("Played a sound", "user_id", job.userID, "sound_id", job.soundID, "ms", sent*soundFrameMs)
}

// sendSoundEvent tells the panels of everybody matched in the roster when the
// next sound may start, so they grey their tiles meanwhile. It names no sound
// and no person.
func (p *Plugin) sendSoundEvent(busyUntil time.Time) {
	told := map[string]bool{}
	for _, person := range p.getVoice().people {
		if person.UserID == "" || told[person.UserID] {
			continue
		}
		told[person.UserID] = true

		p.API.PublishWebSocketEvent(wsEventSound, map[string]any{"busyUntil": busyUntil.UnixMilli()}, &model.WebsocketBroadcast{UserId: person.UserID})
	}
}

// refundablePress is whether a refusal gives the press token back: only for
// what the presser could not see coming and did not cause (§7.5). The rest
// the panel already greys out, so an honest person rarely spends a token on
// them, and a script pressing while muted or cooling down is held to thirty a
// minute.
func refundablePress(code string) bool {
	switch code {
	case soundCodeStore, soundCodeBotOffline, soundCodeBotCannot, soundCodeGone:
		return true
	}

	return false
}

// soundStatus is the HTTP status of a press's refusal.
func soundStatus(code string) int {
	switch code {
	case soundCodeOff:
		return http.StatusNotFound
	case soundCodeBadRequest:
		return http.StatusBadRequest
	case soundCodeGone:
		return http.StatusGone
	case soundCodeCooldown, soundCodeTooMany, soundCodeRateLimited:
		return http.StatusTooManyRequests
	case soundCodeBusy, soundCodeTwoSessions, soundCodeMoved:
		return http.StatusConflict
	case soundCodeStore, soundCodeBotOffline, soundCodeBotCannot:
		return http.StatusServiceUnavailable
	default:
		return http.StatusForbidden
	}
}

type soundPlayResponse struct {
	StartedAt     int64  `json:"startedAt"`
	EndsAt        int64  `json:"endsAt"`
	CooldownUntil int64  `json:"cooldownUntil"`
	Channel       string `json:"channel"`
}

// handleSoundPlay is POST /api/v1/sounds/play?id= (§7.5): cheap checks in
// memory first, then a look at the gate, then Mumble's live state, then one
// atomic admission, and only then the costly work. Answered once the first
// packet is on the wire, or with the code that stopped it.
func (p *Plugin) handleSoundPlay(w http.ResponseWriter, r *http.Request, userID string) {
	cfg := p.getConfiguration()
	if !soundsOn(w, cfg) {
		return
	}

	st := p.sounds()
	now := st.now()
	press := p.limiters().soundPress

	refuse := func(code string, retry time.Duration) {
		if refundablePress(code) {
			press.refund(userID)
		}
		writeSoundRefusal(w, soundStatus(code), code, retry, p.brandName())
	}

	// 1. In memory: the id, the meter, blocked, on the board.
	soundID := r.URL.Query().Get("id")
	if !validPlanID(soundID) {
		writeSoundRefusal(w, http.StatusBadRequest, soundCodeBadRequest, 0, p.brandName())
		return
	}
	if !press.allowAt(userID, now) {
		writeSoundRefusal(w, http.StatusTooManyRequests, soundCodeRateLimited, press.wait(userID, now), p.brandName())
		return
	}

	doc, readable, err := p.board()
	if err != nil || !readable {
		refuse(soundCodeStore, 0)
		return
	}
	if _, blocked := doc.Blocked[userID]; blocked {
		refuse(soundCodeBlocked, 0)
		return
	}
	onBoard := false
	for _, entry := range doc.Entries {
		onBoard = onBoard || entry.ID == soundID
	}
	if !onBoard {
		refuse(soundCodeGone, 0)
		return
	}

	// 2. A look at the gate, changing nothing.
	if code, left := st.gate.peek(userID, now); code != "" {
		refuse(code, left)
		return
	}

	// 3. Mumble connected on this node.
	client, _ := p.currentVoiceClient()
	if client == nil {
		refuse(soundCodeBotOffline, 0)
		return
	}

	// 4. One client.Do, from gumble's live state.
	user, appErr := p.API.GetUser(userID)
	if appErr != nil || user == nil {
		refuse(soundCodeStore, 0)
		return
	}
	read := readVoice(client, p.presser(userID, user.Username), cfg, st.permissions())
	p.noteSoundRead(read)
	if read.code != "" {
		refuse(read.code, 0)
		return
	}

	// 5. The one admission.
	cooldown := time.Duration(cfg.soundCooldownSeconds()) * time.Second
	ticket, code, left := st.gate.admit(userID, now, cooldown)
	if code != "" {
		refuse(code, left)
		return
	}

	// 6. After admission, any failure undoes it.
	blob, err := p.soundBlobFor(soundID)
	if err != nil {
		st.gate.undo(ticket)
		if err == errSoundGone {
			refuse(soundCodeGone, 0)
		} else {
			refuse(soundCodeStore, 0)
		}

		return
	}

	if !read.whisperKnown {
		has, known := p.waitForWhisper(client, read.channel)
		if known && !has {
			st.gate.undo(ticket)
			p.noteSoundRead(voiceRead{code: soundCodeBotCannot, botOK: true, whisperKnown: true, channelName: read.channelName})
			refuse(soundCodeBotCannot, 0)

			return
		}
	}

	// 7. Handed to the player only while it is idle; nothing queues.
	_, player := p.currentVoiceClient()
	if player == nil {
		st.gate.undo(ticket)
		refuse(soundCodeBotOffline, 0)

		return
	}

	job := &soundJob{
		userID:    userID,
		username:  user.Username,
		soundID:   soundID,
		packets:   blob.packets,
		ticket:    ticket,
		cooldown:  cooldown,
		channelID: read.channelID,
		started:   make(chan soundStart, 1),
	}

	select {
	case player.jobs <- job:
	default:
		st.gate.undo(ticket)
		refuse(soundCodeBusy, soundStartingRetry)

		return
	}

	// 8. The player's word.
	start := <-job.started
	if start.code != "" {
		refuse(start.code, 0)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, soundPlayResponse{
		StartedAt:     start.startedAt.UnixMilli(),
		EndsAt:        start.endsAt.UnixMilli(),
		CooldownUntil: start.endsAt.Add(cooldown).UnixMilli(),
		Channel:       start.channel,
	})
}

// waitForWhisper asks Mumble whether VillaNET may whisper in a channel it does
// not know about yet, and waits up to a second, under no lock but gumble's
// read lock for each look. Still not known after that is played anyway:
// Mumble would make it silent, and the admins' next check says so.
func (p *Plugin) waitForWhisper(client *gumble.Client, channel *gumble.Channel) (bool, bool) {
	if channel == nil {
		return false, false
	}

	perms := p.sounds().permissions()
	perms.ask(channel)

	deadline := time.Now().Add(p.sounds().whisperWaitOrDefault())
	for {
		var has, known bool
		client.Do(func() {
			has, known = perms.whisperIn(channel)
		})
		if known || !time.Now().Before(deadline) {
			return has, known
		}

		time.Sleep(50 * time.Millisecond)
	}
}

// whisperWaitOrDefault is how long a press waits to learn about Whisper.
func (st *soundStore) whisperWaitOrDefault() time.Duration {
	st.lock.Lock()
	defer st.lock.Unlock()

	if st.whisperWait > 0 {
		return st.whisperWait
	}

	return soundWhisperWait
}
