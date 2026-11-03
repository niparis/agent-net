package claude

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// deliveryHealth checks the transport only. The known inbox frame is a user
// message; there is no documented harmless probe, so no bytes are sent.
// One reachable live session is enough to establish that the contract works.
func deliveryHealth(sessions map[string]found) string {
	if len(sessions) == 0 {
		return "unknown"
	}
	for _, f := range sessions {
		if f.sock == "" || checkSocket(f.sock) != nil {
			continue
		}
		c, err := net.DialTimeout("unix", f.sock, 200*time.Millisecond)
		if err == nil {
			c.Close()
			return "ok"
		}
	}
	return "broken"
}

// CheckDelivery checks fresh session files after harness detection, including at
// daemon start and after an update. No session means unknown, never broken.
func (a *Adapter) CheckDelivery(version string) string {
	sessions := map[string]found{}
	entries, _ := os.ReadDir(a.cfg.SessionsDir)
	for _, e := range entries {
		m := pidFile.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(a.cfg.SessionsDir, e.Name()))
		if err != nil {
			continue
		}
		var sf sessionFile
		if json.Unmarshal(raw, &sf) != nil || strconv.Itoa(sf.PID) != m[1] || sf.SessionID == "" || !pidAlive(sf.PID) {
			continue
		}
		sessions[sf.SessionID] = found{sock: sf.MessagingSocketPath}
	}
	broken := deliveryHealth(sessions) == "broken"
	if version == "" {
		version = "unknown"
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// The warning blocks every Claude delivery on this machine, so it needs
	// two broken checks in a row: a socket can be away for a moment while a
	// session restarts. A passing or unknown check clears it at once.
	if broken {
		a.healthBroken++
	} else {
		a.healthBroken = 0
	}
	a.healthWarning = ""
	if a.healthBroken >= 2 {
		a.healthWarning = "Claude delivery broken by Claude Code " + version
	}
	return a.healthWarning
}

func (a *Adapter) deliveryWarning() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.healthWarning
}
