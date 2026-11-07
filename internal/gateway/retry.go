package gateway

import (
	"context"
	"strings"
	"time"
)

// Retry only known pre-write failures. Unknown refusals, policy decisions and
// uncertain writes must not authorize another injection.
func retryableFailure(detail string) bool {
	d := strings.ToLower(detail)
	if strings.Contains(d, "may have arrived") {
		return false
	}
	for _, p := range []string{"no such local session", "no such claude session", "claude session is gone or has no inbox socket", "inbox socket refused:", "connect to inbox failed:", "pi worker extension is disconnected", "agent-net is not connected to the codex app server", "this codex thread is not open any more", "the codex worker is not connected", "this antigravity session is no longer open", "worker session is gone", "claude delivery broken by claude code "} {
		if strings.HasPrefix(d, p) {
			return true
		}
	}
	return false
}
func retryDelay(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 30 * time.Second
	case 2:
		return 2 * time.Minute
	case 3:
		return 10 * time.Minute
	default:
		return 30 * time.Minute
	}
}

// RunRetries only selects due failures; claiming each one happens immediately
// before sending, after checking that the destination is connected.
func (g *Gateway) RunRetries(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-g.appsDone:
			return
		case <-t.C:
			func() {
				defer g.slowLoop("retries", time.Now())
				rows, err := g.store.db.Query(`SELECT message_id FROM delivery_retries WHERE next_at <= ?`, g.store.now().UnixNano())
				if err != nil {
					g.logLine("error", "where", "retry query", "err", err)
					return
				}
				var ids []string
				for rows.Next() {
					var id string
					if rows.Scan(&id) == nil {
						ids = append(ids, id)
					}
				}
				rows.Close()
				for _, id := range ids {
					m, ok, err := g.store.Message(id)
					if err == nil && ok && m.ExpiresAt.After(g.store.now()) {
						g.dispatch(ctx, m)
					}
				}
			}()
		}
	}
}
