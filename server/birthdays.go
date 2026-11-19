package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	// The timezone database, embedded. A birthday is a DATE, and which date it
	// is depends entirely on where you are standing — but the plugin runs
	// wherever Mattermost is installed, which may be a scratch container with
	// no /usr/share/zoneinfo at all. Half a megabyte to make "today" mean the
	// same thing on every host is a bargain.
	_ "time/tzdata"

	"github.com/mattermost/mattermost/server/public/model"
)

// Birthdays.
//
// Replaces a cron job and a script holding its own bot token. The interesting
// part is not the message, it is posting it EXACTLY ONCE — a birthday greeting
// that arrives twice is worse than one that arrives late, and there are two
// separate ways for it to double:
//
//  1. The poller runs every fifteen seconds, so "is it today?" is asked some
//     five and a half thousand times a day.
//  2. Mattermost in high availability runs this plugin on every node, all of
//     them asking at once, none of them aware of the others.
//
// Both are answered by the same thing: the date is written with a
// compare-and-set that only succeeds if nobody has written it yet, and the node
// that wins the write is the only one that posts.

const (
	kvBirthdayPosted = "birthday_posted"

	defaultBirthdayHour = 9

	// The cake badge. Fixed rather than configurable: the live badge is
	// configurable because an admin might want a different dot, and there is
	// only one emoji anybody means by "it is their birthday".
	birthdayEmoji     = "birthday"
	birthdayBadgeText = "Birthday"

	// Their file is eight people. This is room for a village.
	maxBirthdayEntries = 500
)

// birthdayEntry is one line of the JSON an admin pastes in — deliberately the
// same shape as the birthdays.json the script used, so the file can be moved
// across without being rewritten.
type birthdayEntry struct {
	Username  string `json:"username"`
	Name      string `json:"name"`
	Birthday  string `json:"birthday"`
	BirthYear int    `json:"birth_year"`

	// One parent or two. The script accepted a bare string or a list, so this
	// does too rather than making somebody edit their data to suit the plugin.
	ParentUsername json.RawMessage `json:"parent_username"`
}

func (e birthdayEntry) parents() []string {
	if len(e.ParentUsername) == 0 {
		return nil
	}

	var one string
	if err := json.Unmarshal(e.ParentUsername, &one); err == nil {
		if one = strings.TrimSpace(one); one != "" {
			return []string{one}
		}

		return nil
	}

	var many []string
	if err := json.Unmarshal(e.ParentUsername, &many); err != nil {
		return nil
	}

	kept := make([]string, 0, len(many))
	for _, parent := range many {
		if parent = strings.TrimSpace(parent); parent != "" {
			kept = append(kept, parent)
		}
	}

	return kept
}

func (e birthdayEntry) firstName() string {
	if fields := strings.Fields(e.Name); len(fields) > 0 {
		return fields[0]
	}

	return e.Name
}

// parseBirthdays reads the configured list, and says what is wrong with it
// rather than silently doing nothing — a birthday list that is quietly not
// working looks exactly like a birthday list with no birthdays in it today.
func parseBirthdays(source string) ([]birthdayEntry, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, nil
	}

	var entries []birthdayEntry
	if err := json.Unmarshal([]byte(source), &entries); err != nil {
		return nil, fmt.Errorf("the birthday list is not valid JSON: %w", err)
	}

	if len(entries) > maxBirthdayEntries {
		return nil, fmt.Errorf("that is %d birthdays; the limit is %d", len(entries), maxBirthdayEntries)
	}

	for i, entry := range entries {
		if strings.TrimSpace(entry.Name) == "" {
			return nil, fmt.Errorf("entry %d has no name", i+1)
		}
		if !validMonthDay(entry.Birthday) {
			return nil, fmt.Errorf("%s has %q, which is not a MM-DD date", entry.Name, entry.Birthday)
		}
		if entry.Username == "" && len(entry.parents()) == 0 {
			return nil, fmt.Errorf("%s needs either a username or a parent_username", entry.Name)
		}
	}

	return entries, nil
}

// validMonthDay accepts what the script accepted, and nothing else. Parsed
// rather than pattern-matched so 02-30 is refused.
func validMonthDay(value string) bool {
	if len(value) != 5 || value[2] != '-' {
		return false
	}

	month, err := strconv.Atoi(value[:2])
	if err != nil || month < 1 || month > 12 {
		return false
	}

	day, err := strconv.Atoi(value[3:])
	if err != nil || day < 1 {
		return false
	}

	// 2020 is a leap year, so 02-29 is a date somebody can have been born on.
	return day <= time.Date(2020, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// birthdayZone is where "today" is measured. Empty means the machine's own
// zone, which is what the cron job it replaces used.
func (c *configuration) birthdayZone() *time.Location {
	name := strings.TrimSpace(c.BirthdayZone)
	if name == "" {
		return time.Local
	}

	location, err := time.LoadLocation(name)
	if err != nil {
		return time.Local
	}

	return location
}

// checkBirthdays is called from every poll. It is cheap on the several thousand
// occasions a day when there is nothing to do.
func (p *Plugin) checkBirthdays(cfg *configuration, now time.Time) {
	if !cfg.EnableBirthdays {
		return
	}

	local := now.In(cfg.birthdayZone())
	today := local.Format("2006-01-02")

	// Free, and true for all but one poll a day: this node has already dealt
	// with today and does not need to ask the database again.
	p.birthdayLock.Lock()
	settled := p.birthdayDone == today
	p.birthdayLock.Unlock()

	if settled || local.Hour() < cfg.birthdayHour() {
		return
	}

	entries, err := parseBirthdays(cfg.Birthdays)
	if err != nil {
		p.setBirthdayProblem(err.Error())

		return
	}
	p.setBirthdayProblem("")

	// The claim, before the post. Whoever wins this is the only node that will
	// greet anybody, and a node that loses has nothing left to do today.
	if !p.claimBirthday(today) {
		return
	}

	p.birthdayLock.Lock()
	p.birthdayDone = today
	p.birthdayLock.Unlock()

	p.greet(cfg, entries, local)
}

// claimBirthday writes today's date, and reports whether this node was the one
// that wrote it.
func (p *Plugin) claimBirthday(today string) bool {
	stored, appErr := p.API.KVGet(kvBirthdayPosted)
	if appErr != nil {
		p.API.LogWarn("Could not read the birthday marker", "error", appErr.Error())

		return false
	}

	if string(stored) == today {
		return false
	}

	// Compare-and-set against exactly what was read, so of several nodes
	// arriving together only one gets through.
	written, appErr := p.API.KVCompareAndSet(kvBirthdayPosted, stored, []byte(today))
	if appErr != nil {
		p.API.LogWarn("Could not write the birthday marker", "error", appErr.Error())

		return false
	}

	return written
}

func (p *Plugin) greet(cfg *configuration, entries []birthdayEntry, local time.Time) {
	channelID := p.channelIDFor(cfg.BirthdayChannel)
	botID := p.getBotUserID()
	if channelID == "" || botID == "" {
		return
	}

	monthDay := local.Format("01-02")

	for _, entry := range entries {
		if entry.Birthday != monthDay {
			continue
		}

		message := birthdayMessage(cfg, entry, local.Year())
		if message == "" {
			continue
		}

		if _, appErr := p.API.CreatePost(&model.Post{
			UserId:    botID,
			ChannelId: channelID,
			Message:   message,
		}); appErr != nil {
			p.API.LogWarn("Could not post a birthday", "error", appErr.Error())
		}
	}
}

// birthdayMessage fills in whichever template applies.
//
// Age is this year minus the year of birth, which is only right on the day
// itself — and the day itself is the only day this runs.
func birthdayMessage(cfg *configuration, entry birthdayEntry, year int) string {
	age := ""
	if entry.BirthYear > 0 && year > entry.BirthYear {
		age = strconv.Itoa(year - entry.BirthYear)
	}

	replace := func(template string, pairs ...string) string {
		return strings.NewReplacer(pairs...).Replace(template)
	}

	if entry.Username != "" {
		return replace(cfg.BirthdayMessage,
			"{full_name}", entry.Name,
			"{first_name}", entry.firstName(),
			"{username}", strings.TrimPrefix(entry.Username, "@"),
			"{age}", age,
		)
	}

	parents := entry.parents()
	if len(parents) == 0 {
		return ""
	}

	tagged := make([]string, 0, len(parents))
	for _, parent := range parents {
		tagged = append(tagged, "@"+strings.TrimPrefix(parent, "@"))
	}

	return replace(cfg.BirthdayKidMessage,
		"{full_name}", entry.Name,
		"{first_name}", entry.firstName(),
		"{parents}", strings.Join(tagged, " & "),
		"{age}", age,
	)
}

func (p *Plugin) setBirthdayProblem(problem string) {
	p.birthdayLock.Lock()
	p.birthdayProblem = problem
	p.birthdayLock.Unlock()
}

func (p *Plugin) birthdayAlert() string {
	p.birthdayLock.RLock()
	defer p.birthdayLock.RUnlock()

	return p.birthdayProblem
}

// celebratingToday resolves today's birthdays to Mattermost accounts, for the
// cake badge.
//
// The whole day, midnight to midnight in the birthday timezone — not from the
// hour the greeting is posted. It is their birthday from midnight whatever hour
// an admin would rather the channel found out.
//
// Only entries with a username can be badged: a custom status belongs to an
// account, and the children in that list do not have one. Their parents are not
// badged either. It is not their birthday.
//
// Cached for the day, because the alternative is a GetUserByUsername per
// celebrant per poll — some five and a half thousand lookups for one person's
// birthday. The cache is dropped when the day turns or the list is edited.
func (p *Plugin) celebratingToday(cfg *configuration, now time.Time) map[string]bool {
	if !cfg.EnableBirthdays || !cfg.BirthdayBadge {
		return nil
	}

	local := now.In(cfg.birthdayZone())
	today := local.Format("2006-01-02")

	p.celebrantLock.RLock()
	fresh := p.celebrantDay == today && p.celebrantList == cfg.Birthdays
	cached := p.celebrants
	p.celebrantLock.RUnlock()

	if fresh {
		return cached
	}

	// A list that will not parse is checkBirthdays's problem to report; here it
	// simply means nobody is badged, which is the same answer it gives for a
	// list with nothing in it today.
	entries, err := parseBirthdays(cfg.Birthdays)
	if err != nil {
		entries = nil
	}

	monthDay := local.Format("01-02")
	found := map[string]bool{}
	settled := true

	for _, entry := range entries {
		if entry.Birthday != monthDay {
			continue
		}

		username := strings.TrimPrefix(strings.TrimSpace(entry.Username), "@")
		if username == "" {
			continue
		}

		user, appErr := p.API.GetUserByUsername(username)
		if appErr != nil {
			// A name nobody has is a settled answer and worth remembering for
			// the day. Anything else — the database being briefly unhappy — is
			// not, and caching it would mean one bad moment costs somebody
			// their badge until tomorrow.
			if appErr.StatusCode != http.StatusNotFound {
				p.API.LogWarn("Could not look up a birthday account; will try again on the next poll",
					"username", username, "error", appErr.Error())
				settled = false
			}

			continue
		}

		if user == nil || user.DeleteAt != 0 || user.IsBot {
			continue
		}

		found[user.Id] = true
	}

	if !settled {
		return found
	}

	// Never mutated after this point, so handing the same map to every later
	// poll is safe. Two goroutines arriving together both build it and the
	// second overwrites the first with the same answer.
	p.celebrantLock.Lock()
	p.celebrants = found
	p.celebrantDay = today
	p.celebrantList = cfg.Birthdays
	p.celebrantLock.Unlock()

	return found
}
