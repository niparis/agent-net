package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// AgyStartEnv carries the ID of an agent-net start into the agy process its
// pane opened. The SessionStart hook of that process reports it, so the daemon
// can claim the conversation agy created for this start.
const AgyStartEnv = "AGENT_NET_START_ID"

// agyStartWait is a start whose conversation ID agy creates. The first
// SessionStart hook from the pane the daemon opened claims that conversation.
type agyStartWait struct {
	path   string
	native string
}

func (d *Daemon) registerAgyStart(id, path string) {
	if id == "" {
		return
	}
	d.mu.Lock()
	if d.agyStarts == nil {
		d.agyStarts = map[string]*agyStartWait{}
	}
	d.agyStarts[id] = &agyStartWait{path: path}
	d.mu.Unlock()
}

// claimAgyStart binds a conversation to the start that opened its pane. A
// hook with a start ID and another folder cannot claim it.
func (d *Daemon) claimAgyStart(id, native, cwd string) {
	if id == "" || native == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.agyStarts[id]
	if s == nil || s.native != "" {
		return
	}
	if s.path != "" && !sameDir(cwd, s.path) {
		return
	}
	s.native = native
}

func (d *Daemon) agyStartNative(id string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if s := d.agyStarts[id]; s != nil {
		return s.native
	}
	return ""
}

func (d *Daemon) forgetAgyStart(id string) {
	d.mu.Lock()
	delete(d.agyStarts, id)
	d.mu.Unlock()
}

// launchOwnerAgy starts or resumes an Antigravity session in a pane of
// agent-net's herdr. agy has no attach client and creates the conversation ID
// itself, so a start waits for the SessionStart hook that names it. A resume
// already knows the ID and runs agy --conversation <id>.
func (d *Daemon) launchOwnerAgy(r protocol.StartedSession, first startFirstMessage, resume, socket, pane string, notify bool) (protocol.StartedSession, error) {
	bin, err := d.harnessBinary("agy")
	if err != nil {
		return protocol.StartedSession{}, err
	}
	var args []string
	if resume == "" {
		// The hook of this pane claims the start by the environment variable
		// the pane carries.
		d.registerAgyStart(r.ID, r.Path)
		args = []string{bin}
		if r.Model != "" {
			selection := protocol.ParseModelSelection("agy", "", r.Model, "")
			modelArgs, err := selection.Arguments([]string{selection.ModelID()})
			if err != nil {
				d.forgetAgyStart(r.ID)
				return protocol.StartedSession{}, err
			}
			args = append(args, modelArgs...)
		}
	} else {
		r.NativeID = resume
		args = []string{bin, "--conversation", resume}
	}
	plan := protocol.EnterPlan{Argv: args, Dir: r.Path, Host: r.Hostname}
	if resume == "" {
		plan.Env = map[string]string{AgyStartEnv: r.ID}
	}
	opened := d.viewPlan("agy:"+firstNonEmpty(resume, r.ID), r.Project, true, plan)
	if !opened.OpenedHerdr {
		d.forgetAgyStart(r.ID)
		return protocol.StartedSession{}, fmt.Errorf("cannot start Antigravity in herdr on %s: %s", d.hostName(), opened.Note)
	}
	r.EnterPlan = &opened
	d.mu.Lock()
	if d.startedSessions == nil {
		d.startedSessions = make(map[string]protocol.StartedSession)
	}
	d.startedSessions[r.ID] = r
	d.mu.Unlock()
	// The launch belongs to the daemon, not the requesting socket.
	d.wg.Add(1)
	go func(r protocol.StartedSession) {
		defer d.wg.Done()
		if resume == "" {
			defer d.forgetAgyStart(r.ID)
		}
		cctx, cancel := context.WithTimeout(d.ctoCtx, d.workerSettings("").StartTimeout)
		defer cancel()
		var startErr error
		if resume == "" {
			startErr = d.waitAgyStartClaim(cctx, &r)
		}
		if startErr == nil {
			startErr = d.waitAgySession(cctx, &r)
		}
		if startErr == nil {
			d.noteRequestedModel("agy", r.NativeID, r.Model)
			startErr = d.checkStartAgent(cctx, r)
		}
		if startErr == nil {
			if startErr = d.sendFirstMessage(cctx, r.SessionID, first); startErr != nil {
				keepUnsentFirstMessage(&r, first)
			}
		}
		if startErr != nil {
			r.Status, r.Error = "failed", startErr.Error()
			d.releaseRepoStart(r)
			d.failStartAgent(r)
		} else {
			r.Status = "started"
		}
		// Report a failure before the failed state is visible; see launchOwnerClaude.
		if r.Status == "failed" {
			d.startFailed(r, notify)
		}
		d.mu.Lock()
		d.startedSessions[r.ID] = r
		d.mu.Unlock()
		if r.Status == "started" && notify {
			d.notifySessionStart(r)
		}
	}(r)
	return r, nil
}

// waitAgyStartClaim waits for the SessionStart hook of the pane this start
// opened to name the conversation, then binds the lasting Agent and Repo start
// to that ID.
func (d *Daemon) waitAgyStartClaim(ctx context.Context, r *protocol.StartedSession) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		native := d.agyStartNative(r.ID)
		if native != "" {
			r.NativeID = native
			if r.AgentID != "" {
				if err := d.reserveStartAgent(ctx, "agy", native, r.AgentID, r.ID, r.Path, r.Project); err != nil {
					return err
				}
			}
			return d.bindRepoStart(ctx, *r)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the daemon did not see the Antigravity session start: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// waitAgySession waits for the daemon to register the discovered conversation
// and learn its gateway session ID.
func (d *Daemon) waitAgySession(ctx context.Context, r *protocol.StartedSession) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		d.mu.Lock()
		if ls := d.sessions[key("agy", r.NativeID)]; ls != nil {
			r.SessionID = ls.sessionID
		}
		d.mu.Unlock()
		if r.SessionID != "" {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the daemon did not register the Antigravity session: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
