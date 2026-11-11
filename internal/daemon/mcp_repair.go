package daemon

// A Claude session whose `agent-net mcp` process never said hello cannot use
// the agent-net tools, even though the session itself looks online. Claude Code
// does not retry an MCP server that failed to start, and the daemon cannot make
// it reconnect. So the daemon restarts the session once, using the owner's own
// stop and resume path, while it is idle. Only if that restart fails, or the
// restarted session still has no MCP hello after the same wait, the owner gets
// one Desk item. Nothing here asks the owner to take a manual step first.

import (
	"context"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

const (
	// mcpRepairAfterDefault is how long a Claude session may go without an MCP
	// hello before the daemon restarts it, and how long it then waits for a
	// hello before telling the owner.
	mcpRepairAfterDefault = 5 * time.Minute
	// mcpRepairTimeout bounds one stop and resume.
	mcpRepairTimeout = 2 * time.Minute
	// mcpDeskRetry is how soon a failed ask.open is tried again.
	mcpDeskRetry = time.Minute
)

// mcpRepairState is the one restart and the one report of a session.
type mcpRepairState struct {
	restartedAt time.Time // when the one restart attempt was made
	restarting  bool      // the attempt is running now
	failed      bool      // the attempt failed; report now
	lastError   string
	posted      bool      // the gateway accepted the Desk item
	lastPost    time.Time // when the item was last queued
}

// mcpRepairRequest is one restart waiting for the discovery lock to be
// released.
type mcpRepairRequest struct {
	native    string
	sessionID string
	name      string
}

// mcpDeskAsk is one Desk item waiting for the discovery lock to be released.
type mcpDeskAsk struct {
	native string
	ask    protocol.AskOpen
}

func (d *Daemon) mcpRepairAfter() time.Duration {
	if d.cfg.testMCPRepairAfter > 0 {
		return d.cfg.testMCPRepairAfter
	}
	return mcpRepairAfterDefault
}

// noteMCPRepairLocked is called from session discovery for a Claude session
// that has no MCP link. It restarts the session once, and only an idle one, so
// a turn is never cut. The caller holds d.mu; startMCPRepairs and sendMCPDesk
// do the work after the lock is released. A repeated poll adds nothing.
func (d *Daemon) noteMCPRepairLocked(in protocol.SessionInfo, sessionID string, since, now time.Time) {
	if now.Sub(since) < d.mcpRepairAfter() {
		return
	}
	if _, ok := d.mcpHello[in.NativeID]; ok {
		return
	}
	if d.mcpRepair == nil {
		d.mcpRepair = map[string]*mcpRepairState{}
	}
	repair := d.mcpRepair[in.NativeID]
	if repair == nil {
		repair = &mcpRepairState{}
		d.mcpRepair[in.NativeID] = repair
	}
	if repair.restarting {
		return
	}
	name := mcpSessionName(in)
	if repair.restartedAt.IsZero() {
		// The first failure: restart once, and only while the session is idle.
		// A busy session is tried again on the next check.
		if in.Status != "idle" || sessionID == "" {
			return
		}
		repair.restarting = true
		d.pendingMCPRepairs = append(d.pendingMCPRepairs, mcpRepairRequest{native: in.NativeID, sessionID: sessionID, name: name})
		return
	}
	// The one restart happened. A failure is reported at once; otherwise wait
	// for the session's MCP hello and report only if it does not come.
	if repair.failed {
		d.queueMCPDeskLocked(repair, in.NativeID, name, repair.lastError, now)
		return
	}
	if now.Sub(repair.restartedAt) >= d.mcpRepairAfter() {
		d.queueMCPDeskLocked(repair, in.NativeID, name, "", now)
	}
}

func mcpSessionName(in protocol.SessionInfo) string {
	if in.Name != "" {
		return in.Name
	}
	return in.NativeID
}

// startMCPRepairs starts the restarts queued under d.mu. They run outside the
// discovery lock and are cancelled when the daemon stops.
func (d *Daemon) startMCPRepairs() {
	d.mu.Lock()
	pending := d.pendingMCPRepairs
	d.pendingMCPRepairs = nil
	d.mu.Unlock()
	for _, req := range pending {
		req := req
		d.goBackground(func() { d.runMCPRepair(req) })
	}
}

// runMCPRepair restarts one session. It checks the session again at the moment
// of the stop: a turn may have started since the discovery poll, and a turn is
// never cut.
func (d *Daemon) runMCPRepair(req mcpRepairRequest) {
	ctx, cancel := context.WithTimeout(d.ctoCtx, mcpRepairTimeout)
	defer cancel()
	view, err := d.agentView(ctx, req.sessionID)
	if err != nil {
		d.mcpRepairFailed(req, err)
		return
	}
	if !view.Online || view.Status != "idle" {
		d.mcpRepairLater(req.native)
		return
	}
	at := time.Now()
	d.mu.Lock()
	if repair := d.mcpRepair[req.native]; repair != nil {
		repair.restartedAt = at
	}
	d.mu.Unlock()
	// Record the attempt before it starts, so a daemon that dies during it does
	// not try a second time.
	if d.st != nil {
		if err := d.st.setMCPRestarted(req.native, at); err != nil {
			d.Logf("mcp repair: remember the restart: %v", err)
		}
	}
	if err := d.mcpRestart(ctx, req.sessionID); err != nil {
		d.mcpRepairFailed(req, err)
		return
	}
	d.mu.Lock()
	if repair := d.mcpRepair[req.native]; repair != nil {
		repair.restarting = false
	}
	d.mu.Unlock()
	d.Logf("mcp repair: restarted %s", req.name)
}

// mcpRestart uses the one restart path: stop the online session, wait until it
// is reported ended, then resume it in the background.
func (d *Daemon) mcpRestart(ctx context.Context, sessionID string) error {
	if d.cfg.testMCPRestart != nil {
		return d.cfg.testMCPRestart(ctx, sessionID)
	}
	_, err := d.restartSession(ctx, sessionID, true)
	return err
}

// mcpRepairLater releases an attempt that found the session busy. The next
// discovery check may try again.
func (d *Daemon) mcpRepairLater(native string) {
	d.mu.Lock()
	if repair := d.mcpRepair[native]; repair != nil {
		repair.restarting = false
		repair.restartedAt = time.Time{}
	}
	d.mu.Unlock()
}

// mcpRepairFailed reports the one failed restart and stops further attempts.
func (d *Daemon) mcpRepairFailed(req mcpRepairRequest, err error) {
	d.Logf("mcp repair: restart %s: %v", req.name, err)
	d.mu.Lock()
	repair := d.mcpRepair[req.native]
	if repair == nil {
		repair = &mcpRepairState{}
		d.mcpRepair[req.native] = repair
	}
	repair.restarting = false
	repair.failed = true
	repair.lastError = err.Error()
	d.queueMCPDeskLocked(repair, req.native, req.name, repair.lastError, time.Now())
	d.mu.Unlock()
	d.sendMCPDesk()
}

// queueMCPDeskLocked queues the one Desk item of a session, unless it was
// already sent or is waiting to be retried.
func (d *Daemon) queueMCPDeskLocked(repair *mcpRepairState, native, name, detail string, now time.Time) {
	if repair.posted || now.Before(repair.lastPost.Add(mcpDeskRetry)) {
		return
	}
	repair.lastPost = now
	question := "The agent-net tools could not be connected in session " + name + " after a restart."
	d.pendingMCPDesk = append(d.pendingMCPDesk, mcpDeskAsk{native: native, ask: protocol.AskOpen{
		Kind: protocol.AskKindCheck, Ref: "mcp:" + native, From: "agent-net",
		Question: question, Detail: detail,
	}})
}

// sendMCPDesk sends the items queued under d.mu, outside the discovery lock.
// An item is not sent if the session said hello after it was queued.
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
			repair := d.mcpRepair[p.native]
			d.mu.Unlock()
			if helloed {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := d.call(ctx, "ask.open", p.ask); err != nil {
				d.Logf("mcp repair: %v", err)
				return
			}
			if d.st != nil {
				if err := d.st.setMCPRepairPosted(p.native, time.Now()); err != nil {
					d.Logf("mcp repair: remember the item: %v", err)
				}
			}
			d.mu.Lock()
			if repair != nil {
				repair.posted = true
			}
			d.mu.Unlock()
		})
	}
}
