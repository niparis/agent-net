package daemon

import (
	"context"
	"encoding/json"

	"github.com/niparis/agent-net/internal/protocol"
)

// rebindResumedSession keeps a resumed session's ID when the harness gave the
// resumed conversation a new native ID (a Claude Code background copy does).
// The gateway record follows the new handle, so the session keeps its ID and
// every worker, delivery change and queued message that names it.
func (d *Daemon) rebindResumedSession(ctx context.Context, sessionID, nativeID string) error {
	if sessionID == "" || nativeID == "" {
		return nil
	}
	_, err := d.call(ctx, protocol.FrameSessionResumeRebind, protocol.SessionResumeRebind{SessionID: sessionID, NativeID: nativeID})
	return err
}

// adoptResumedSession moves a restarted session's work to the session its new
// conversation registered, when the harness could not keep the old ID. A
// resume that kept the ID needs nothing: the two IDs are the same.
//
// The gateway does the ownership move in one transaction. The daemon then
// moves its own worker records, because a worker's hand-back and step tasks
// are addressed with the caller ID the daemon holds.
func (d *Daemon) adoptResumedSession(ctx context.Context, sessionID, newSessionID string) {
	if sessionID == "" || newSessionID == "" || sessionID == newSessionID {
		return
	}
	data, err := d.call(ctx, protocol.FrameSessionReplace, protocol.SessionReplace{SessionID: sessionID, NewSessionID: newSessionID})
	if err != nil {
		d.Logf("session resume: could not move %s to %s: %v", sessionID, newSessionID, err)
		return
	}
	var res struct {
		Moved bool `json:"moved"`
	}
	if len(data) > 0 {
		if json.Unmarshal(data, &res) == nil && !res.Moved {
			// Already moved, or the old record is still online. Nothing to do.
			return
		}
	}
	d.reassignWorkerOwner(sessionID, newSessionID)
	d.Logf("session resume: %s was replaced by %s; its work moved", sessionID, newSessionID)
}

// reassignWorkerOwner moves the caller ID of every worker the daemon holds,
// running or queued, to the session that replaced its caller.
func (d *Daemon) reassignWorkerOwner(sessionID, newSessionID string) {
	d.wmu.Lock()
	for _, w := range d.workers {
		if w.owner == sessionID {
			w.owner = newSessionID
		}
	}
	d.wmu.Unlock()
	if err := d.st.moveWorkerOwner(sessionID, newSessionID); err != nil {
		d.Logf("session resume: could not move the caller of the workers of %s: %v", sessionID, err)
	}
}
