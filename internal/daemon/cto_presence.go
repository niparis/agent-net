package daemon

import (
	"context"
	"time"
)

// ctoRunning reports whether this daemon currently supervises a live runtime.
func (d *Daemon) ctoRunning() bool {
	d.cto.mu.Lock()
	if d.cto.cmd != nil && !d.ctoHandoverPendingLocked("opencode") {
		select {
		case <-d.cto.exited:
		default:
			d.cto.mu.Unlock()
			return true
		}
	}
	if d.codexCTORunningLocked() && !d.ctoHandoverPendingLocked("codex") {
		d.cto.mu.Unlock()
		return true
	}
	c := &d.claudeCTO
	ref := c.ref
	handover := d.ctoHandoverPendingLocked("claude")
	root := c.root.Load()
	d.cto.mu.Unlock()
	if ref == "" || handover || root == nil {
		return false
	}
	token, alive := procToken(root.pid)
	state, _ := claudeCTOJobState(d.cfg.ClaudeSessionsDir, ref)
	return alive && token == root.token && !claudeJobEnded(state)
}

// A status report can only update the CTO already hosted by this node. It
// cannot register or move the network identity. Serialize reports and read
// the current state inside the lock, so a delayed report cannot undo a newer
// transition.
func (d *Daemon) publishCTOPresence() {
	d.ctoPresenceMu.Lock()
	defer d.ctoPresenceMu.Unlock()
	id := d.ctoSessionID()
	if id == "" {
		return
	}
	status := "dormant"
	if d.ctoRunning() {
		status = "idle"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = d.call(ctx, "cto.presence", map[string]string{"session_id": id, "status": status})
}

func (d *Daemon) scheduleCTOPresence() {
	d.goBackground(d.publishCTOPresence)
}

// Caller holds cto.mu. A process that already exited logs its own crash.
func (d *Daemon) stopOpenCodeCTOLocked(reason string) {
	c := &d.cto
	c.gen++
	if c.cmd == nil {
		return
	}
	intentional := false
	select {
	case <-c.exited:
	default:
		intentional = true
		c.run.expected.Store(true)
	}
	run := c.run
	c.stopLocked()
	if intentional && run.started.Load() {
		d.Logf("cto.stop harness=opencode reason=%s", reason)
	}
	d.scheduleCTOPresence()
}

// Claude's background process is not our child, so watch its recorded job and
// process identity. The lock makes a crash and an intentional stop exclusive.
func (d *Daemon) monitorClaudeCTO(ctx context.Context, ref string, pid int, token string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.cto.mu.Lock()
			c := &d.claudeCTO
			root := c.root.Load()
			same := c.ref == ref && root != nil && root.pid == pid && root.token == token
			d.cto.mu.Unlock()
			if !same {
				return
			}
			actual, alive := procToken(pid)
			state, _ := claudeCTOJobState(d.cfg.ClaudeSessionsDir, ref)
			if !alive || actual != token || claudeJobEnded(state) {
				d.cto.mu.Lock()
				root = c.root.Load()
				if c.ref == ref && root != nil && root.pid == pid && root.token == token {
					d.stopClaudeCTOLocked(claudeCTOEndReason(state))
				}
				d.cto.mu.Unlock()
				return
			}
		}
	}
}

func claudeCTOEndReason(state string) string {
	switch state {
	case "done", "stopped", "killed":
		return "job_" + state
	default:
		return "crash"
	}
}
