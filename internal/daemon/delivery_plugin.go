package daemon

import (
	"context"

	"github.com/niparis/agent-net/internal/protocol"
)

// The delivery check of an ordinary OpenCode session. Its plugin confirms that
// a message is queued in the session, which does not mean a turn runs for it:
// on 2026-11-20 a session took messages for 80 minutes and ran none. The check
// is the one the CTO has (delivery_confirm.go). The plugin reports each turn
// start as a busy status, so the check needs no read of the harness. When no
// turn starts, the message is delivered once more. When none starts after that,
// the sender is told the message was not delivered.

// pluginDeliveryNeedsCheck tells whether a delivery to a plugin session gets the
// check. Only OpenCode sessions do; Pi confirms the turn it starts.
func pluginDeliveryNeedsCheck(harness string) bool { return harness == "opencode" }

// notePluginDelivered remembers a message the plugin took, and starts its
// check. The failed turn that follows reaches the sender through the same list.
func (d *Daemon) notePluginDelivered(ls *localSession, m protocol.Message) {
	d.mu.Lock()
	ls.noteOwedLocked(m.ID)
	started := ls.turnStarts
	d.mu.Unlock()
	d.confirmDelivery("session\x00"+m.To, deliveryWatch{
		label: "session",
		msgID: m.ID,
		gone: func() bool {
			d.mu.Lock()
			defer d.mu.Unlock()
			return ls.conn == nil || d.sessions[key(ls.info.Harness, ls.info.NativeID)] != ls
		},
		busy: func(ctx context.Context) bool {
			d.mu.Lock()
			defer d.mu.Unlock()
			return ls.info.Status == "busy"
		},
		taken: func(ctx context.Context) bool {
			d.mu.Lock()
			defer d.mu.Unlock()
			return ls.turnStarts > started
		},
		redeliver: func(ctx context.Context) bool {
			d.mu.Lock()
			conn := ls.conn
			d.mu.Unlock()
			if conn == nil {
				return false
			}
			if res := conn.push(ctx, m, d.cfg.PluginAckTimeout); !res.Delivered {
				d.Logf("session.delivery_retry_failed msg=%s detail=%q", m.ID, res.Detail)
			}
			return true
		},
		unconfirmed: func(ctx context.Context) {
			d.mu.Lock()
			owed := ls.takeOwedMessagesLocked()
			d.mu.Unlock()
			d.reportNotDelivered(owed, "the session started no turn for it")
		},
	})
}
