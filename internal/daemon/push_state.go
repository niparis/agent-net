package daemon

import (
	"crypto/sha256"
	"encoding/json"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A session's report to the gateway changes often in two ways only: its
// activity time moves each time its harness prints something, and a busy
// worker's token counters move on every stats tick. Sending every
// one cost the gateway a write and a round of delivery sweeps, about three
// times a second for a handful of sessions. So the daemon sends a report when
//
//   - a field other than the activity time changed (at once),
//   - only the activity time changed and pushActivityEvery has passed (the
//     newest value goes out when the time is up), or
//   - nothing changed and pushKeepalive has passed (a slow sign of life, so
//     the gateway's last_seen stays fresh).
//
// A session that stops is still taken offline at once: discovery, the plugin
// and the link send session.remove and session.sync, which this does not
// touch. A new link starts again from the gateway's snapshot, so the first
// report on it always goes out.
const (
	pushActivityEvery = 30 * time.Second
	pushKeepalive     = 2 * time.Minute
)

type pushTiming struct{ activity, keepalive time.Duration }

func (d *Daemon) pushTiming() pushTiming {
	t := pushTiming{activity: pushActivityEvery, keepalive: pushKeepalive}
	if d.cfg.testPushActivityEvery > 0 {
		t.activity = d.cfg.testPushActivityEvery
	}
	if d.cfg.testPushKeepalive > 0 {
		t.keepalive = d.cfg.testPushKeepalive
	}
	return t
}

// pushState is what the gateway was last told about one session.
type pushState struct {
	sent     bool
	hash     [sha256.Size]byte // of the report without its activity times
	activity [5]int64          // activity times in seconds and the worker's token counters
	at       time.Time
	epoch    uint64
	timer    *time.Timer
	timerAt  time.Time // when timer fires; zero when none is waiting
}

// invalidatePush makes the next report of a session go out. Call it when the
// gateway changed the session without the daemon's report: a removal.
func (d *Daemon) invalidatePush(sessionID string) {
	if sessionID == "" {
		return
	}
	if ls := d.sessionByID(sessionID); ls != nil {
		ls.pushInvalid.Store(true)
	}
}

// pushDigest splits a report into what is sent at once when it changes and
// the activity times that are sent on a delay.
func pushDigest(in protocol.SessionInfo) ([sha256.Size]byte, [5]int64) {
	act := [5]int64{in.HarnessActivity, in.LastActive}
	in.HarnessActivity, in.LastActive, in.SessionID = 0, 0, ""
	if in.Worker != nil {
		// A busy worker's token counters change on every stats tick.
		w := *in.Worker
		act[2], act[3], act[4] = w.ContextTokens, w.ContextLimit, w.Tokens
		w.ContextTokens, w.ContextLimit, w.Tokens = 0, 0, 0
		in.Worker = &w
	}
	raw, err := json.Marshal(in)
	if err != nil {
		// Cannot compare: send every time, as before.
		return [sha256.Size]byte{}, [5]int64{-1}
	}
	return sha256.Sum256(raw), act
}

// due tells whether to send the report now. When it does not, wait is how
// long until the report is due.
func (p *pushState) due(in protocol.SessionInfo, epoch uint64, now time.Time, t pushTiming) (wait time.Duration, send bool) {
	hash, act := pushDigest(in)
	if !p.sent || p.epoch != epoch || p.hash != hash || act[0] < 0 {
		return 0, true
	}
	age := now.Sub(p.at)
	if act != p.activity {
		if age >= t.activity {
			return 0, true
		}
		return t.activity - age, false
	}
	if age >= t.keepalive {
		return 0, true
	}
	return t.keepalive - age, false
}

// record notes that the gateway now has the report.
func (p *pushState) record(in protocol.SessionInfo, epoch uint64, now time.Time) {
	p.hash, p.activity = pushDigest(in)
	p.sent, p.epoch, p.at = true, epoch, now
}

// armPush makes sure the session is pushed again after wait, so a report that
// was held back goes out and the keepalive keeps running. The caller holds
// ls.pushMu.
func (d *Daemon) armPush(ls *localSession, wait time.Duration) {
	due := time.Now().Add(wait)
	if ls.push.timer != nil && !ls.push.timerAt.IsZero() && !ls.push.timerAt.After(due) {
		return
	}
	if ls.push.timer != nil {
		ls.push.timer.Stop()
	}
	ls.push.timerAt = due
	ls.push.timer = time.AfterFunc(wait, func() {
		d.goBackground(func() { d.pushSessionChanged(ls) })
	})
}
