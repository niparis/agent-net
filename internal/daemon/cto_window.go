package daemon

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// This grant exists only in the CTO host daemon's memory. It is never an
// argument, a setting, a database row, or a value an agent can replay.
type ctoGraphWindow struct {
	mu    sync.Mutex
	until time.Time
}

// Callers hold mu across deciding and starting a graph operation.
func (w *ctoGraphWindow) active(now time.Time) bool {
	if !now.Before(w.until) {
		w.until = time.Time{}
	}
	return !w.until.IsZero()
}
func (w *ctoGraphWindow) toggle(now time.Time) {
	if w.active(now) {
		w.until = time.Time{}
	} else {
		w.until = now.Add(30 * time.Minute)
	}
}

func (lc *localConn) ctoGraphWindow(raw json.RawMessage) (any, error) {
	// dispatchOperator already checked the OS process ancestry. Check again here
	// because dispatchOwner is also used by the private proposal runner.
	if err := lc.operatorGate("cto_graph_window"); err != nil {
		return nil, err
	}
	lc.mu.Lock()
	tui := lc.tui
	lc.mu.Unlock()
	if !tui || lc.cto {
		return nil, errors.New("CTO graph window requires the owner's TUI")
	}
	var q struct {
		Toggle bool `json:"toggle"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &q) != nil {
		return nil, errors.New("bad CTO graph window request")
	}
	w := &lc.d.ctoGraph
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now()
	if q.Toggle {
		w.toggle(now)
	}
	w.active(now)
	return protocol.CTOGraphWindow{Until: w.until}, nil
}
