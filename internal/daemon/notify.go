package daemon

import (
	"database/sql"
	"errors"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// notificationTracker watches the network snapshot while an owner TUI is open.
// Questions themselves never enter commands or logs.
type notificationTracker struct {
	mu   sync.Mutex
	seen map[string]int64
	last map[string]time.Time
}

func (n *notificationTracker) observe(agents []protocol.AgentView, now time.Time, enabled bool) []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.last == nil {
		n.last = make(map[string]time.Time)
	}
	next := make(map[string]int64)
	var out []string
	for _, a := range agents {
		w := a.Waiting
		if !a.Online || w == nil || !w.Explicit {
			continue
		}
		next[a.SessionID] = w.Since
		old, seen := n.seen[a.SessionID]
		if enabled && (!seen || old != w.Since) && (n.last[a.SessionID].IsZero() || now.Sub(n.last[a.SessionID]) >= 5*time.Minute) {
			n.last[a.SessionID] = now
			out = append(out, a.SessionID)
		}
	}
	n.seen = next
	return out
}

func (d *Daemon) notificationsDisabled() (bool, error) {
	var disabled bool
	err := d.st.db.QueryRow(`SELECT value FROM owner_preferences WHERE key='notifications_disabled'`).Scan(&disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return disabled, err
}

func (d *Daemon) notifyWaiting(agents []protocol.AgentView) {
	disabled, err := d.notificationsDisabled()
	if err != nil {
		return
	}
	now := time.Now()
	for _, id := range d.notify.observe(agents, now, !disabled) {
		allowed, err := d.reserveNotification(id, now)
		if err != nil || !allowed {
			continue
		}
		send := d.notifySend
		if send == nil {
			send = desktopNotification
		}
		go send()
	}
}

func desktopNotification() {
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("osascript", "-e", `display notification "An agent needs your input." with title "agent-net"`).Run()
	case "linux":
		_ = exec.Command("notify-send", "agent-net", "An agent needs your input.").Run()
	}
}

// reserveNotification stores the rate limit across daemon restarts.
func (d *Daemon) reserveNotification(id string, now time.Time) (bool, error) {
	result, err := d.st.db.Exec(`INSERT INTO notification_sent(session_id, at) VALUES(?, ?) ON CONFLICT(session_id) DO UPDATE SET at=excluded.at WHERE notification_sent.at <= excluded.at - 300`, id, now.Unix())
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
