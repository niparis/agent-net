package claude

import (
	"net"
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
