package daemon

import (
	"encoding/json"
	"errors"
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
	if w = protocol.CleanWaiting(w); w != nil && w.Since == 0 {
		w.Since = time.Now().Unix()
	}
	d.mu.Lock()
	ls.info.Waiting = w
	d.mu.Unlock()
	d.pushSession(ls)
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
	if p.Clear {
		d.setWaiting(ls, nil)
		return nil, nil
	}
	w := protocol.CleanWaiting(&protocol.Waiting{Kind: p.Kind, Text: p.Text, Options: p.Options, Urgency: p.Urgency})
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
func keepWaiting(old protocol.SessionInfo, in protocol.SessionInfo) *protocol.Waiting {
	if in.Waiting != nil {
		w := protocol.CleanWaiting(in.Waiting)
		if isIdlePromptWait(w) {
			return nil
		}
		return w
	}
	if old.Status != "busy" && in.Status == "busy" {
		return nil
	}
	if isIdlePromptWait(old.Waiting) {
		return nil
	}
	return old.Waiting
}
