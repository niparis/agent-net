package daemon

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// Automatic continues: when a worker stops early (a partial hand-back, or idle
// without a hand-back), the daemon sends it one more message from the reserved
// agent-net sender instead of telling the caller at once. At most
// protocol.DefaultAutoContinues per worker task. These are not the caller's
// rework and do not count in the difficulty calibration.

// autoContinueEnabledWith combines the run choice with the current rule.
func autoContinueEnabledWith(snap settings.Snapshot, repo string, requested *bool) bool {
	where := settings.Where{Repo: repo}
	if requested != nil {
		b, _ := json.Marshal(*requested)
		where.Run = map[string]json.RawMessage{"workers.auto_continue": b}
	}
	return snap.Bool("workers.auto_continue", where)
}

// autoContinueEligible uses the defaults for callers without a snapshot.
func autoContinueEligible(w *worker) bool {
	return autoContinueEligibleWith(w, settings.Snapshot{}, "")
}

// autoContinueEligibleWith is called while holding d.wmu.
func autoContinueEligibleWith(w *worker, snap settings.Snapshot, repo string) bool {
	var maximum int
	if e, err := snap.Resolve("workers.auto_continue_max", settings.Where{Repo: repo}); err == nil {
		_ = json.Unmarshal(e.Value, &maximum)
	}
	return autoContinueEnabledWith(snap, repo, &w.autoContinue) && w.autoContinueCount < maximum &&
		!w.stopping && w.doneStatus == "" && w.sessionID != ""
}

// tryAutoContinue sends the worker one automatic continue. It reports whether
// a message was sent; false means the caller must be told instead.
func (d *Daemon) tryAutoContinue(ctx context.Context, w *worker) bool {
	snap := d.SettingsSnapshot()
	d.wmu.Lock()
	if d.workers[w.id] != w || !autoContinueEligibleWith(w, snap, w.project) {
		d.wmu.Unlock()
		return false
	}
	// Take the slot before delivery, so two stop signals at once cannot both
	// pass the limit. It is given back if the message cannot be delivered.
	w.autoContinueCount++
	w.autoContinueRun++
	count, sid, project := w.autoContinueCount, w.sessionID, w.project
	d.saveWorkerLocked(w)
	var inform *protocol.WorkerInfo
	if w.info != nil {
		cp := *w.info
		cp.AutoContinue = count
		w.info = &cp
		snap := cp
		inform = &snap
	}
	d.wmu.Unlock()
	if inform != nil {
		if ls := d.setSessionWorker(sid, inform); ls != nil {
			go d.pushSession(ls)
		}
	}
	// A new turn starts a new idle period: the next stop is a new signal.
	d.resetIdleNotice(w)
	m := protocol.Message{ID: protocol.NewID("m"), From: "agent-net", FromName: "agent-net", FromHarness: "agent-net",
		To: sid, Type: protocol.TypeNotification, Body: protocol.WorkerAutoContinueWithSettings(snap, project)}
	if err := d.deliverAutoContinue(ctx, w, m); err != nil {
		d.wmu.Lock()
		if w.autoContinueCount > 0 {
			w.autoContinueCount--
		}
		if w.autoContinueRun > 0 {
			w.autoContinueRun--
		}
		d.saveWorkerLocked(w)
		d.wmu.Unlock()
		d.Logf("worker %s: auto continue: %v", w.id, err)
		return false
	}
	d.Logf("worker.auto_continue worker=%s session=%s count=%d", w.id, sid, count)
	return true
}

// deliverAutoContinue hands the reserved message to the worker's own harness.
// Codex is driven turn by turn here; the other harnesses take it through their
// own adapter, and an OpenCode turn waits behind the current one.
func (d *Daemon) deliverAutoContinue(ctx context.Context, w *worker, m protocol.Message) error {
	d.wmu.Lock()
	harness, sid, cc, owner, task := w.harness, w.sessionID, w.codex, w.owner, w.taskMsg
	d.wmu.Unlock()
	if harness == "codex" {
		if cc == nil {
			return errors.New("the Codex worker is not connected")
		}
		// The continue comes from the reserved agent-net sender and is never
		// stored at the gateway, but the turn's report is the worker's
		// hand-back: it goes to the caller, as a reply to the task.
		if !cc.submit(codexJob{from: owner, msgID: task, text: m.Body}) {
			return errors.New("the Codex worker is ending")
		}
		return nil
	}
	if queued, retry := d.queueOpenCodeTurn(w, m); retry {
		return errors.New("the worker is ending")
	} else if queued {
		return nil
	}
	ls := d.sessionByID(sid)
	if ls == nil {
		return errors.New("the worker session is not connected")
	}
	res := d.deliverTo(ctx, ls, m)
	if !res.Delivered {
		if res.Detail == "" {
			res.Detail = "the harness did not accept the message"
		}
		return errors.New(res.Detail)
	}
	return nil
}
