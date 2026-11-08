package daemon

import (
	"time"

	"github.com/niparis/agent-net/internal/providererr"
)

// Provider usage limits. When a harness reports a turn that failed with a
// usage or rate limit, the session carries limited_until and limited_reason
// to the gateway until the time passes or it completes a turn. Pi and
// OpenCode report through their plugins (turn_error, turn_ok and the status
// changes), Codex through its turn/completed notification, and Claude Code
// through its transcript (see internal/daemon/claude). All of them use
// providererr, the same classifier as the worker failure check.

// turnFailed records the text of a failed turn. A usage limit sets the limit
// on the session. Any failed turn stops the idle change after it from
// counting as a success.
func (d *Daemon) turnFailed(ls *localSession, text string) {
	d.turnFailedAt(ls, text, time.Now())
}

func (d *Daemon) turnFailedAt(ls *localSession, text string, at time.Time) {
	if len(text) > 2000 {
		text = text[:2000]
	}
	l, limited := providererr.Limit(text, at)
	d.mu.Lock()
	ls.turnErrored = true
	changed := false
	if limited && (ls.limitUntil != l.Until.Unix() || ls.limitReason != l.Reason) {
		ls.limitUntil, ls.limitReason = l.Until.Unix(), l.Reason
		changed = true
	}
	d.mu.Unlock()
	if changed {
		d.pushSession(ls)
	}
}

// turnSucceeded clears the limit: the provider answered a turn.
func (d *Daemon) turnSucceeded(ls *localSession) {
	d.mu.Lock()
	ls.turnErrored = false
	changed := ls.limitUntil != 0
	ls.limitUntil, ls.limitReason = 0, ""
	d.mu.Unlock()
	if changed {
		d.pushSession(ls)
	}
}

// turnStartedLocked forgets the failure of the turn before. The caller holds d.mu.
func (ls *localSession) turnStartedLocked() { ls.turnErrored = false }

// turnIdleLocked is a busy session going idle: a turn that reported no error
// ended, so the limit is over. It reports whether the limit was cleared.
// The caller holds d.mu.
func (ls *localSession) turnIdleLocked() bool {
	if ls.turnErrored || ls.limitUntil == 0 {
		return false
	}
	ls.limitUntil, ls.limitReason = 0, ""
	return true
}
