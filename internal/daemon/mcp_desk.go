package daemon

// A Claude session whose `agent-net mcp` process never said hello cannot use
// the agent-net tools, even though the session itself looks online. Claude Code
// does not retry an MCP server that failed to start, and the daemon cannot make
// it reconnect, so the owner is the only one who can fix it: type /mcp in that
// session. The daemon raises one Desk item per such session, after a wait long
// enough that a normal start is never reported.

import (
	"context"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

const (
	// mcpDeskAfterDefault is how long a Claude session may go without an MCP
	// hello before the owner is told.
	mcpDeskAfterDefault = 5 * time.Minute
	// mcpDeskRetry is how soon a failed ask.open is tried again.
	mcpDeskRetry = time.Minute
)

// mcpDeskReport is the state of the one Desk item of a session.
type mcpDeskReport struct {
	posted  bool      // the gateway accepted the item
	lastTry time.Time // when the item was last queued
}

// mcpDeskAsk is one item waiting for the discovery lock to be released.
type mcpDeskAsk struct {
	native string
	ask    protocol.AskOpen
}

func (d *Daemon) mcpDeskAfter() time.Duration {
	if d.cfg.testMCPDeskAfter > 0 {
		return d.cfg.testMCPDeskAfter
	}
	return mcpDeskAfterDefault
}

// noteMCPDeskLocked queues the one Desk item for a Claude session that has had
// no MCP hello after the wait. The caller holds d.mu; sendMCPDesk sends the
// items after the lock is released. A session is reported once, and a repeated
// discovery poll adds nothing.
func (d *Daemon) noteMCPDeskLocked(in protocol.SessionInfo, since, now time.Time) {
	if now.Sub(since) < d.mcpDeskAfter() {
		return
	}
	if _, ok := d.mcpHello[in.NativeID]; ok {
		return
	}
	if d.mcpDesk == nil {
		d.mcpDesk = map[string]*mcpDeskReport{}
	}
	report := d.mcpDesk[in.NativeID]
	if report == nil {
		report = &mcpDeskReport{}
		d.mcpDesk[in.NativeID] = report
	}
	if report.posted || now.Before(report.lastTry.Add(mcpDeskRetry)) {
		return
	}
	report.lastTry = now
	name := in.Name
	if name == "" {
		name = in.NativeID
	}
	d.pendingMCPDesk = append(d.pendingMCPDesk, mcpDeskAsk{native: in.NativeID, ask: protocol.AskOpen{
		Kind: protocol.AskKindCheck, Ref: "mcp:" + in.NativeID, From: "agent-net",
		Question: "The agent-net tools are not connected in session " + name + ": type /mcp there to connect them.",
	}})
}

// sendMCPDesk sends the items queued under d.mu. It runs outside the discovery
// lock, so a slow gateway link cannot hold the session poll. An item is not
// sent if the session said hello after it was queued.
func (d *Daemon) sendMCPDesk() {
	d.mu.Lock()
	pending := d.pendingMCPDesk
	d.pendingMCPDesk = nil
	d.mu.Unlock()
	for _, p := range pending {
		p := p
		d.goBackground(func() {
			d.mu.Lock()
			_, helloed := d.mcpHello[p.native]
			report := d.mcpDesk[p.native]
			d.mu.Unlock()
			if helloed {
				return
			}
			// The owner may have seen and dismissed the item before a daemon
			// restart. The state database remembers it across restarts.
			if d.st != nil && d.st.mcpDeskPosted(p.native) {
				d.mu.Lock()
				if report != nil {
					report.posted = true
				}
				d.mu.Unlock()
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := d.call(ctx, "ask.open", p.ask); err != nil {
				d.Logf("mcp desk: %v", err)
				return
			}
			if d.st != nil {
				if err := d.st.setMCPDeskPosted(p.native, time.Now()); err != nil {
					d.Logf("mcp desk: remember the item: %v", err)
				}
			}
			d.mu.Lock()
			if report != nil {
				report.posted = true
			}
			d.mu.Unlock()
		})
	}
}
