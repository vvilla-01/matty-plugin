package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
)

// Clips over time: starting and stopping with the plugin, and the sweep that
// removes clips once they have been kept as long as an admin said, banking
// their upvotes, and tidies up whatever a crash left on the disk.

// clipsSweepJob names the sweep's cluster job. It keeps two KV keys of its
// own, cron_clips_sweep and mutex_cron_clips_sweep, so that one node at a time
// runs it, every clipsSweepInterval.
const clipsSweepJob = "clips_sweep"

// clipsJob is the sweep's schedule: a cluster job, or a test's stand-in, which
// runs nothing by itself so that a test calls the sweep when it wants to.
type clipsJob interface {
	Close() error
}

// scheduleClipsSweep is how the sweep is scheduled when nothing has replaced
// it: on one node of the cluster at a time, every ten minutes from whenever it
// last finished anywhere, and at once when it never has.
func scheduleClipsSweep(api cluster.JobPluginAPI, run func()) (clipsJob, error) {
	job, err := cluster.Schedule(api, clipsSweepJob, cluster.MakeWaitForInterval(clipsSweepInterval), run)
	if err != nil {
		// Never a typed nil inside the interface: the caller would take it
		// for a job and Close it.
		return nil, err
	}

	return job, nil
}

// clipsTimer is the republish timer: time.AfterFunc's, or a test's stand-in,
// which fires when the test says.
type clipsTimer interface {
	Stop() bool
}

func afterClipsDelay(wait time.Duration, run func()) clipsTimer {
	return time.AfterFunc(wait, run)
}

// startClips readies this node for clips, from OnActivate. Nothing in it can
// fail activation: whatever goes wrong is a problem an admin is shown, and the
// rest of the plugin carries on.
//
//  1. Work out where clip files go, make the directories, and check they take
//     a write.
//  2. Empty ring/ and drafts/: neither outlives the process that made them.
//  3. Read the document, which halts every write to it if it will not decode.
//  4. Count the room clips take.
//  5. Schedule the sweep, start the minute's housekeeping, and let the
//     recorder manager start recorders, from the first poll on.
//  6. Work out Best Clipper again, with everybody looked up afresh.
func (p *Plugin) startClips() {
	s := p.clips()
	now := s.now()

	// A second activation without a deactivation between has a sweep, a
	// housekeeping and maybe recorders of its own already. Stopped first, so
	// there are never two, and the recorders under the root they started
	// with, before it is worked out again.
	p.recorders().shutdown(now)
	p.stopClipsSweep()
	p.stopClipsHousekeeping()

	// A missing Site URL is said once per activation, and this is one.
	s.shares.noSiteURLLogged.Store(false)

	root, problem := p.resolveClipsRoot()
	if problem == "" {
		if err := prepareClipsRoot(root); err != nil {
			problem = "Clips cannot write to " + root + ": " + err.Error() + "."
		}
	}
	s.setStorage(root, problem)

	if problem != "" {
		p.API.LogWarn("Clips cannot be kept on this server", "problem", problem)
	} else {
		p.API.LogInfo("Clips are kept in this directory", "root", root)
	}

	// Whatever drafts this process held went with the files.
	p.wipeClipsScratch()
	s.drafts.forgetAll()

	doc, err := p.readClipsFresh(now)
	if err != nil {
		// Halting has said so already, and a store that did not answer is
		// asked again by the first reader.
		doc = nil
	}
	s.recountUsage(doc)

	s.markRunning()

	run, stopRun := context.WithCancel(context.Background())
	// Each pass run safely, and the next comes on the clock (recovery.go).
	housekeeping := s.every(clipsHousekeepEvery, func() {
		p.safely("the clips housekeeping", func() { p.housekeepClips(s.now()) })
	})
	s.runLock.Lock()
	s.runCtx, s.runCancel, s.housekeeping = run, stopRun, housekeeping
	s.runLock.Unlock()

	p.recorders().begin()

	ctx, cancel := context.WithCancel(context.Background())
	job, err := s.schedule(p.API, func() { p.sweepClips(ctx) })
	if err != nil {
		cancel()
		p.API.LogError("Could not schedule the clips sweep, so expired clips will stay on the disk", "error", err.Error())
	} else {
		s.runLock.Lock()
		s.job, s.sweepCancel = job, cancel
		s.runLock.Unlock()
	}

	if err := p.recomputeBestClipper(now); err != nil && !errors.Is(err, errClipsHalted) {
		p.API.LogWarn("Could not work out Best Clipper on activation", "error", err.Error())
	}
}

// stopClips is OnDeactivate's: every recorder stops, their sessions on
// Broadcast Box closed and their goroutines waited for, five seconds at most
// for them all (spec §2.14); the sweep is stopped, and waited for if it is
// running, after being told to stop where it is; the housekeeping stops, and
// every export and trim under way is cancelled; nothing more is republished
// or rearmed; every draft, and who was watching what, is forgotten; and
// ring/ and drafts/ are emptied. Published clips, and the document, are left
// exactly as they are.
func (p *Plugin) stopClips() {
	s := p.clips()

	p.recorders().shutdown(s.now())
	p.stopClipsSweep()
	p.stopClipsHousekeeping()

	s.runLock.Lock()
	timer := s.republishTimer
	s.republishTimer, s.republishArmed = nil, false
	s.runLock.Unlock()

	if timer != nil {
		timer.Stop()
	}

	s.stopRunning()
	s.drafts.forgetAll()
	s.viewers.clear()
	p.wipeClipsScratch()
}

// stopClipsHousekeeping stops the minute's housekeeping, waiting for a run
// under way, and cancels every export and trim, which then stop where they
// are and tidy up after themselves.
func (p *Plugin) stopClipsHousekeeping() {
	s := p.clips()

	s.runLock.Lock()
	job, cancel := s.housekeeping, s.runCancel
	s.housekeeping, s.runCtx, s.runCancel = nil, nil, nil
	s.runLock.Unlock()

	if cancel != nil {
		cancel()
	}
	if job != nil {
		_ = job.Close()
	}
}

// stopClipsSweep cancels a sweep under way and closes the job, which waits
// for that sweep to return.
func (p *Plugin) stopClipsSweep() {
	s := p.clips()

	s.runLock.Lock()
	job, cancel := s.job, s.sweepCancel
	s.job, s.sweepCancel = nil, nil
	s.runLock.Unlock()

	if cancel != nil {
		cancel()
	}
	if job != nil {
		if err := job.Close(); err != nil {
			p.API.LogWarn("Could not stop the clips sweep", "error", err.Error())
		}
	}
}

// wipeClipsScratch empties ring/ and drafts/, when there is a root that may be
// written to, and nothing at all otherwise.
func (p *Plugin) wipeClipsScratch() {
	s := p.clips()

	for _, sub := range []string{clipsRingDir, clipsDraftsDir} {
		if err := s.wipeClipsDir(sub); err != nil {
			p.API.LogWarn("Could not empty a clips directory", "directory", sub, "error", err.Error())
		}
	}
}

// clipsSweepReport is what one sweep did, for its log line and for tests.
type clipsSweepReport struct {
	Ran      bool
	Expired  int
	Deferred int
	Orphans  int
	Freed    int64
}

// sweepClips is the cluster job's run: one sweep, now.
func (p *Plugin) sweepClips(ctx context.Context) {
	p.sweepClipsAt(ctx, time.Now())
}

// sweepClipsAt is the sweep, at the moment given.
//
//  1. Read the document afresh. Nothing below happens without it: a sweep
//     that could not read which clips exist must not decide any file is
//     nobody's.
//  2. In one write, store how long clips are kept as of now, if that has
//     moved, and settle every clip past it, banking its upvotes. The same
//     write works Best Clipper out again, as every write does, which is how
//     a holder deactivated since is caught.
//  3. Delete the files of the clips it settled.
//  4. Delete files no clip names, once they are a day old, and writes that
//     never finished, once they are an hour old.
//  5. Count the room clips take again.
//
// Not while the document is halted, nor while there is nowhere to keep clips.
// ctx is cancelled when the plugin stops, and the sweep stops where it is:
// everything it leaves is picked up by the next one.
func (p *Plugin) sweepClipsAt(ctx context.Context, now time.Time) clipsSweepReport {
	s := p.clips()
	report := clipsSweepReport{}

	if halted, _ := s.halt.state(); halted {
		return report
	}
	root := s.writableRoot()
	if root == "" || ctx.Err() != nil {
		return report
	}

	if _, err := p.readClipsFresh(now); err != nil {
		if !errors.Is(err, errClipsHalted) {
			p.API.LogWarn("Could not read the clips to sweep them", "error", err.Error())
		}

		return report
	}
	report.Ran = true

	outcome, err := p.expireClips(now)
	if err != nil {
		if !errors.Is(err, errClipsHalted) {
			p.API.LogWarn("Could not remove the clips that have expired", "error", err.Error())
		}
	} else {
		report.Expired = len(outcome.Settled)
		report.Deferred = len(outcome.Deferred)
		report.Freed += p.clipsSettled(outcome.Settled)
	}

	// The write itself may have found the document broken, or written by a
	// newer version, in the moment since the read above, and halted. Then
	// the copy in hand is no longer the store's, and while halted nothing is
	// deleted on the strength of it (spec §2.8).
	if halted, _ := s.halt.state(); halted {
		return report
	}

	if ctx.Err() == nil {
		orphans, freed := p.sweepClipOrphans(ctx, root, s.cached(), now)
		report.Orphans = orphans
		report.Freed += freed
	}

	s.recountUsage(s.cached())

	if report.Expired > 0 || report.Deferred > 0 || report.Orphans > 0 {
		p.API.LogInfo("Clips swept", "expired", report.Expired, "deferred", report.Deferred, "orphans", report.Orphans, "bytes", report.Freed)
	}

	return report
}

// sweepClipOrphans deletes from clips/ what no clip in doc accounts for: a
// video or picture whose clip has gone, or a picture its clip no longer
// shows, once a day old; and a write that never finished, once an hour old.
// The ages are the margin for a clip being published as the sweep looks: its
// files are in place a moment before the write that names them lands. Files
// it does not recognise are not its to delete.
func (p *Plugin) sweepClipOrphans(ctx context.Context, root string, doc *clipsDoc, now time.Time) (int, int64) {
	if doc == nil || root == "" {
		return 0, 0
	}

	dir := filepath.Join(root, clipsFilesDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}

	// A halt set by any other reader part way through stops it too, for the
	// same reason the sweep checks before it starts.
	halt := &p.clips().halt
	removed, freed := 0, int64(0)
	for _, entry := range entries {
		if halted, _ := halt.state(); halted || ctx.Err() != nil {
			break
		}
		if !entry.Type().IsRegular() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}
		name := entry.Name()
		age := now.Sub(info.ModTime())

		orphan := false
		switch ext := filepath.Ext(name); {
		case strings.HasPrefix(name, clipTempPrefix):
			orphan = age >= tmpAge
		case ext == clipVideoExt || ext == clipThumbExt:
			id := strings.TrimSuffix(name, ext)
			if !validClipID(id) {
				continue
			}

			entry := doc.clip(id)
			orphan = age >= orphanAge && (entry == nil || (ext == clipThumbExt && entry.Thumb == ""))
		}
		if !orphan {
			continue
		}

		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			continue
		}
		removed++
		freed += info.Size()
	}

	return removed, freed
}
