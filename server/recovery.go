package main

import (
	"context"
	"fmt"
	"net/http"
	"runtime/debug"
	"time"
)

// What a panic would cost, and what it costs instead.
//
// Mattermost's side of a plugin calls ServeHTTP with no recover, and a panic
// nobody recovers ends the whole process, whichever goroutine it is on: every
// clip recorder and its ring, the Mumble connection, the rate limiters and
// everything else held in memory, until Mattermost notices and starts the
// plugin again, and a panic that comes back keeps it down. So ServeHTTP and
// every goroutine of the plugin's own that lives for long catch one, log it
// with its stack, and carry on: a request is refused, and a loop runs its
// next pass, after a pause where nothing else would pace it.
//
// A lock held without a deferred Unlock when a panic comes stays held. So a
// pass is only run again where what it locks is either let go by a defer or
// held only around a few plain assignments, which have nothing in them to
// panic; each loop that is wrapped says what it does.

// panicLog is where a caught panic is written: the plugin API, or a
// recorder's log.
type panicLog interface {
	LogError(msg string, keyValuePairs ...any)
}

// panicLogged is the line a caught panic is logged under. Beside it are where
// it happened, what it said and the stack: never a request's body or headers,
// which can carry stream keys and session tokens.
const panicLogged = "Matty caught a panic and carried on"

// panicRefusalText is what a request that panicked is told. What went wrong
// is in the server log for an admin; the person asking can only try again.
const panicRefusalText = "Something went wrong on the server. Try again in a moment."

// loopPanicPause is how long a loop that nothing else paces waits after a pass
// that panicked, so that a pass panicking every time costs a log line a
// minute rather than a loop spinning on it.
const loopPanicPause = time.Minute

// safely runs f, and is whether it panicked. A panic is logged with where it
// happened and its stack, and goes no further.
func safely(log panicLog, where string, f func()) (panicked bool) {
	defer func() {
		if value := recover(); value != nil {
			panicked = true
			logPanic(log, where, value)
		}
	}()

	f()

	return false
}

func logPanic(log panicLog, where string, value any) {
	if log == nil {
		return
	}
	log.LogError(panicLogged, "where", where, "panic", fmt.Sprint(value), "stack", string(debug.Stack()))
}

// safely is safely, logged through the plugin API.
func (p *Plugin) safely(where string, f func()) bool {
	return safely(p.API, where, f)
}

// goSafely runs f on a goroutine of its own, which a panic ends without
// taking anything else with it.
func (p *Plugin) goSafely(where string, f func()) {
	go p.safely(where, f)
}

// passSafely runs one pass of a loop that would run the next at once if this
// one panicked before it said when, and after a panic waits out pause, or
// until ctx ends, before it lets the loop go on.
func passSafely(ctx context.Context, log panicLog, where string, pause time.Duration, pass func()) {
	if !safely(log, where, pass) {
		return
	}

	timer := time.NewTimer(pause)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// recoverRequest is ServeHTTP's first deferred call: a panic serving a
// request is logged with the route alone and answered with the plugin's own
// refusal, a 500 with the code "failed", as far as the answer has not begun. http.ErrAbortHandler is how a
// handler means to cut a response short, and is passed on as it is.
func (p *Plugin) recoverRequest(w http.ResponseWriter, r *http.Request) {
	value := recover()
	if value == nil {
		return
	}
	if value == http.ErrAbortHandler { //nolint:errorlint // recover gives the value itself, never a wrapped one
		panic(value)
	}

	logPanic(p.API, "serving "+r.Method+" "+r.URL.Path, value)
	writeError(w, http.StatusInternalServerError, panicRefusalText)
}
