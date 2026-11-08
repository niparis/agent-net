package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// "Needs you" (decision 0016). A session has an optional waiting state: what
// it waits to hear from its user. The daemon keeps it with the session, sends
// it to the gateway like the status, and clears it when the session works
// again or when the user prompts it.

// idlePromptText is the message of Claude Code's idle_prompt notification. It
// fires about 60 seconds after any turn ends and means the session finished,
// not that the owner is needed. An older hook stored it as a wait, so the
// daemon drops it: a request with this text clears the wait, and a discovery
// refresh does not keep a stored one.
const idlePromptText = "Claude is waiting for your input"

// isIdlePromptWait says whether a wait is only the generic idle_prompt state,
// which carries no request from the owner.
func isIdlePromptWait(w *protocol.Waiting) bool {
	return w != nil && w.Kind == protocol.WaitingInput && strings.TrimSpace(w.Text) == idlePromptText
}

// setWaiting replaces a session's waiting state (nil clears it) and tells the
// gateway. A new wait gets the time it began when the caller did not say.
func (d *Daemon) setWaiting(ls *localSession, w *protocol.Waiting) {
	w = protocol.CleanWaiting(w)
	d.mu.Lock()
	sessionID := ls.sessionID
	d.mu.Unlock()
	worker := d.workerBySession(sessionID)
	d.mu.Lock()
	oldWait := ls.info.Waiting
	if worker != nil {
		oldWait = ls.workerWaiting
		if w != nil && w.Text == "" {
			w = nil
		}
	}
	if w != nil && w.Since == 0 {
		if oldWait != nil {
			w.Since = oldWait.Since
			if !sameWaiting(oldWait, w) {
				w.Since = 0
			}
		}
		if w.Since == 0 {
			w.Since = time.Now().Unix()
		}
	}
	started := w != nil && !sameWaiting(oldWait, w)
	harness, native, at := ls.info.Harness, ls.info.NativeID, time.Now()
	if started || (oldWait != nil && w == nil) {
		ls.harnessActivity = at
	}
	if started {
		d.recordActivityLocked(ls, at)
	}
	if worker != nil {
		old := ls.workerWaiting
		changed := w != nil && (old == nil || old.Kind != w.Kind || old.Text != w.Text)
		ls.workerWaiting = w
		ls.info.Waiting = nil
		d.mu.Unlock()
		if started {
			d.saveActivity(harness, native, at)
		}
		if changed {
			d.wmu.Lock()
			owner := worker.owner
			d.wmu.Unlock()
			if owner != "" {
				body := fmt.Sprintf("worker %s is blocked: %s", sessionID, w.Text)
				if _, err := d.sendWorkerNoticeBody(context.Background(), worker, body, "", "", ""); err != nil {
					d.Logf("worker %s: blocked notice: %v", worker.id, err)
					d.mu.Lock()
					if ls.workerWaiting == w {
						ls.workerWaiting = nil
					}
					d.mu.Unlock()
				}
			}
		}
		d.pushSession(ls)
		return
	}
	ls.info.Waiting = w
	d.mu.Unlock()
	if started {
		d.saveActivity(harness, native, at)
	}
	d.pushSession(ls)
}

// sameWaiting says whether a report describes the wait already stored, so a
// re-report does not count as new activity.
func sameWaiting(a, b *protocol.Waiting) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Explicit == b.Explicit && a.Blocking == b.Blocking && a.Kind == b.Kind && a.Text == b.Text &&
		a.Since == b.Since && a.Urgency == b.Urgency && slices.Equal(a.Options, b.Options)
}

// waitingCall serves MethodWaiting. A plugin reports for the session it is
// bound to. A command line (a harness hook) names the session it runs for and
// is silent when the daemon does not know it: a hook must never disturb the
// turn that runs it.
func (lc *localConn) waitingCall(client string, ls *localSession, raw json.RawMessage) (any, error) {
	var p protocol.WaitingRequest
	if json.Unmarshal(raw, &p) != nil {
		return nil, errors.New("bad waiting params")
	}
	d := lc.d
	if client == "cli" {
		if p.NativeID == "" {
			return nil, nil
		}
		d.mu.Lock()
		ls = d.sessions[key(p.Harness, p.NativeID)]
		d.mu.Unlock()
		if ls == nil {
			return nil, nil
		}
	} else if ls == nil {
		return nil, errors.New("this method needs a bound session")
	}
	if p.Explicit && d.workerBySession(ls.sessionID) != nil {
		d.setWaiting(ls, &protocol.Waiting{Kind: p.Kind, Text: p.Text, Explicit: true})
		return nil, errors.New("you are a worker: put the question in your hand-back or ask your caller with agent_message_reply")
	}
	if p.Clear {
		d.setWaiting(ls, nil)
		return nil, nil
	}
	w := protocol.CleanWaiting(&protocol.Waiting{Kind: p.Kind, Text: p.Text, Options: p.Options, Urgency: p.Urgency, Explicit: p.Explicit, Blocking: !p.Explicit})
	if w == nil || isIdlePromptWait(w) {
		// Nothing to wait for, or only the generic idle_prompt text: clear any
		// stale state instead of storing it again.
		d.setWaiting(ls, nil)
		return nil, nil
	}
	d.setWaiting(ls, w)
	return nil, nil
}

// keepWaiting carries a session's waiting state across a discovery refresh.
// Discovery (the Claude adapter, for example) reports only the session file,
// so it would erase the wait on every poll. A session that becomes busy again
// is no longer waiting.
//
// A harness that says itself that the session waits (in.HarnessWaits: Claude
// Code's session file, a Codex thread flag; decision 0057) starts and ends the
// wait. While it keeps waiting, the wait keeps its start, also when a hook
// reported the same dialog first. When it stops waiting, the user answered:
// every wait but an explicit ask ends. A worker's wait is its caller's
// business (setWaiting), never discovery's.
func keepWaiting(old protocol.SessionInfo, in protocol.SessionInfo) *protocol.Waiting {
	if in.Worker != nil {
		return nil
	}
	if in.Waiting != nil {
		w := protocol.CleanWaiting(in.Waiting)
		if isIdlePromptWait(w) {
			return nil
		}
		if w != nil && in.HarnessWaits {
			if old.Waiting != nil && old.Waiting.Kind == w.Kind && old.Waiting.Text == w.Text {
				return old.Waiting
			}
			if old.Waiting != nil && !old.Waiting.Explicit {
				w.Since = old.Waiting.Since
			}
			if w.Since == 0 {
				w.Since = time.Now().Unix()
			}
		}
		return w
	}
	if old.HarnessWaits && !in.HarnessWaits {
		if old.Waiting != nil && old.Waiting.Explicit {
			return old.Waiting
		}
		return nil
	}
	if old.Status != "busy" && in.Status == "busy" {
		return nil
	}
	// A turn that ended (busy to idle) with no wait from the harness: a hook
	// wait (a permission denied, an interrupt) has no later event to clear it.
	// An explicit ask or an announced wait outlives the turn.
	if old.Status == "busy" && in.Status == "idle" && old.Waiting != nil && old.Waiting.Blocking {
		return nil
	}
	if isIdlePromptWait(old.Waiting) {
		return nil
	}
	return old.Waiting
}

// onWaitClear ends the explicit wait of a session whose question or approval
// the owner dismissed in the Inbox. A wait a harness reported stays: it is
// about a dialog, not about the item.
func (d *Daemon) onWaitClear(sessionID string) {
	ls := d.sessionByID(sessionID)
	if ls == nil {
		return
	}
	d.mu.Lock()
	explicit := ls.info.Waiting != nil && ls.info.Waiting.Explicit
	d.mu.Unlock()
	if explicit {
		d.setWaiting(ls, nil)
	}
}
