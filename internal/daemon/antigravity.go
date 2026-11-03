package daemon

import (
	"encoding/json"
	"errors"
	"time"
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
func (d *Daemon) antigravityHook(raw json.RawMessage, pid int) (map[string]any, error) {
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
		return map[string]any{}, nil
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
		return map[string]any{}, nil
	}
	if a == nil {
		return map[string]any{}, nil
	}
	a.wake()
	return a.takeHook(e, pid), nil
}
