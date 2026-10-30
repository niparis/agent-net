package daemon

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// "Needs you" (decision 0016). A session has an optional waiting state: what
// it waits to hear from its user. The daemon keeps it with the session, sends
// it to the gateway like the status, and clears it when the session works
// again or when the user prompts it.

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
	if p.Clear || (p.Kind == "" && p.Text == "") {
		d.setWaiting(ls, nil)
		return nil, nil
	}
	d.setWaiting(ls, &protocol.Waiting{Kind: p.Kind, Text: p.Text})
	return nil, nil
}

// keepWaiting carries a session's waiting state across a discovery refresh.
// Discovery (the Claude adapter, for example) reports only the session file,
// so it would erase the wait on every poll. A session that becomes busy again
// is no longer waiting.
func keepWaiting(old protocol.SessionInfo, in protocol.SessionInfo) *protocol.Waiting {
	if in.Waiting != nil {
		return protocol.CleanWaiting(in.Waiting)
	}
	if old.Status != "busy" && in.Status == "busy" {
		return nil
	}
	return old.Waiting
}
