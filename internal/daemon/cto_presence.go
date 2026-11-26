package daemon

import (
	"context"
	"syscall"
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

// ctoStop is the part of a CTO stop that calls other processes: `claude
// stop`, a kill and the wait for the exit, closing a connection. A stop first
// detaches the run from memory under cto.mu, so every reader sees it stopped
// at once, and runs its ctoStop after cto.mu is released (B12). Each step
// acts only on the run it detached: its own process, job and connection.
type ctoStop []func()

// add appends the steps of another stop.
func (s *ctoStop) add(other ctoStop) { *s = append(*s, other...) }

// run runs the steps in order. The caller does not hold cto.mu.
func (s ctoStop) run() {
	for _, step := range s {
		step()
	}
}

// detachOpenCodeCTOLocked forgets the private OpenCode server under cto.mu
// and returns the kill and the wait for its exit. A process that already
// exited logs its own crash.
func (d *Daemon) detachOpenCodeCTOLocked(reason string) ctoStop {
	c := &d.cto
	c.gen++
	if c.cmd == nil {
		return nil
	}
	intentional := false
	select {
	case <-c.exited:
	default:
		intentional = true
		c.run.expected.Store(true)
	}
	cmd, exited, run := c.cmd, c.exited, c.run
	c.clearLocked()
	return ctoStop{func() {
		select {
		case <-exited:
		default:
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-exited
		}
		if intentional && run.started.Load() {
			d.Logf("cto.stop harness=opencode reason=%s", reason)
		}
		d.scheduleCTOPresence()
	}}
}

// stopOpenCodeCTO stops the private OpenCode server. The caller does not hold
// cto.mu.
func (d *Daemon) stopOpenCodeCTO(reason string) {
	d.cto.mu.Lock()
	stop := d.detachOpenCodeCTOLocked(reason)
	d.cto.mu.Unlock()
	stop.run()
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
				var stop ctoStop
				d.cto.mu.Lock()
				root = c.root.Load()
				if c.ref == ref && root != nil && root.pid == pid && root.token == token {
					stop = d.detachClaudeCTOLocked(claudeCTOEndReason(state))
				}
				d.cto.mu.Unlock()
				stop.run()
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
