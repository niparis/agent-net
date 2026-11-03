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
	d.agyEvents[e.ConversationID] = e
	return nil
}
