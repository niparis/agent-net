package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

type antigravityEvent struct {
	ConversationID string    `json:"conversationId"`
	WorkspacePaths []string  `json:"workspacePaths"`
	Event          string    `json:"event"`
	Time           time.Time `json:"time"`
	FullyIdle      bool      `json:"fullyIdle"`
	Started        bool      `json:"-"`
	PID            int       `json:"-"`
	ProcessStart   string    `json:"-"`
}

func (d *Daemon) recordAntigravity(raw json.RawMessage) error {
	var e antigravityEvent
	if err := json.Unmarshal(raw, &e); err != nil {
		return err
	}
	if e.ConversationID == "" || len(e.ConversationID) > 256 {
		return errors.New("invalid conversation ID")
	}
	switch e.Event {
	case "SessionStart", "PreInvocation", "Stop":
	default:
		return errors.New("invalid event")
	}
	e.Time = time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.agyEvents == nil {
		d.agyEvents = make(map[string]antigravityEvent)
	}
	old := d.agyEvents[e.ConversationID]
	if e.Event == "SessionStart" {
		e.Started = true
	} else {
		e.Started, e.PID, e.ProcessStart = old.Started, old.PID, old.ProcessStart
		if old.Started {
			e.WorkspacePaths = old.WorkspacePaths
		}
	}
	d.agyEvents[e.ConversationID] = e
	return nil
}

// antigravityHook records the hook and returns only messages addressed to the
// discovered process. An unrelated local process cannot drain its queue.
// antigravityHook is also used by in-process callers that accept the reply
// directly. The local socket path confirms only after writing its response.
func (d *Daemon) antigravityHook(raw json.RawMessage, pid int) (map[string]any, error) {
	reply, err := d.prepareAntigravityHook(raw, pid)
	if err != nil {
		return nil, err
	}
	reply.confirm(nil)
	return reply.value, nil
}

type agyHookReply struct {
	value    map[string]any
	daemon   *Daemon
	messages []protocol.Message
}

func (r *agyHookReply) confirm(writeErr error) {
	if len(r.messages) == 0 {
		return
	}
	// Save the result before returning, but do not hold up the hook for the
	// network receipt. Stop waits for the background update.
	for _, m := range r.messages {
		var err error
		if writeErr == nil {
			err = r.daemon.st.markDelivered(m.ID)
		} else {
			err = r.daemon.st.markDeliveryUnconfirmed(m.ID)
		}
		state, detail := protocol.StateDelivered, "Antigravity hook"
		if writeErr != nil {
			state, detail = protocol.StateFailed, "write failed after connecting; the message may have arrived: "+writeErr.Error()
		}
		if err != nil {
			state, detail = protocol.StateFailed, "may have arrived: cannot save hook delivery result: "+err.Error()
		}
		r.daemon.releaseDelivery(m.ID)
		r.daemon.goBackground(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			r.daemon.sendUpdate(ctx, m.ID, state, detail)
		})
	}
}

func (d *Daemon) prepareAntigravityHook(raw json.RawMessage, pid int) (*agyHookReply, error) {
	var e antigravityEvent
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, err
	}
	a, _ := d.adapters["agy"].(*agyAdapter)
	if a != nil {
		a.mu.Lock()
		defer a.mu.Unlock()
	}
	d.mu.Lock()
	old := d.agyEvents[e.ConversationID]
	d.mu.Unlock()
	if old.Started && !pidChain(pid)[old.PID] && agyProcess(old.PID) {
		return &agyHookReply{value: map[string]any{}}, nil
	}
	if err := d.recordAntigravity(raw); err != nil {
		return nil, err
	}
	d.mu.Lock()
	current := d.agyEvents[e.ConversationID]
	d.mu.Unlock()
	if e.Event == "SessionStart" {
		root := agyAncestor(pid)
		if root == 0 || agyWorkspace(root, e.WorkspacePaths) == "" || d.workerForPeer(root) != nil || d.agyWorkerConversation(e.ConversationID) {
			root = 0
		}
		d.mu.Lock()
		current.PID = root
		current.ProcessStart = agyProcessStart(root)
		current.Started = root != 0 && current.ProcessStart != ""
		d.agyEvents[e.ConversationID] = current
		d.mu.Unlock()
	} else if current.PID == 0 || !pidChain(pid)[current.PID] {
		// A hook without a verified SessionStart cannot receive messages.
		return &agyHookReply{value: map[string]any{}}, nil
	}
	if a == nil {
		return &agyHookReply{value: map[string]any{}}, nil
	}
	a.wake()
	return a.takeHook(e, pid), nil
}
