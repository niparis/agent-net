package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// How long a restart waits for the stopped session to be reported ended, and
// how long a resume waits for its background launch to finish.
const (
	ctoRestartEndedWait = 30 * time.Second
	ctoResumeStartWait  = 60 * time.Second
	ctoControlPoll      = 100 * time.Millisecond
	// ctoStopSaveWait is all the time the owner stop gives the save of a
	// Codex or OpenCode conversation before it stops the process anyway.
	ctoStopSaveWait = 3 * time.Second
)

// errCTOSelfControl refuses the CTO a stop, resume or restart of its own
// session (decision 0157). Only the owner does that.
var errCTOSelfControl = errors.New("the CTO cannot stop, resume or restart its own session; only the owner can")

// refuseCTOSelfControl is the one check of errCTOSelfControl. Every path of the
// CTO's tools to its own session (session_change, the settings_change operator
// methods session_stop and session_resume, cto_op, a stored CTO proposal, a
// restart) carries an actor, "cto" or the approver of a CTO proposal, and
// reaches the CTO's branch of sessionStopLocal or resumeAgent, which call this
// first. Only those paths set an actor: the owner's own stop and start, and
// the gateway's relay of them, carry none.
func refuseCTOSelfControl(ctx context.Context) error {
	if proposalActor(ctx) != "" {
		return errCTOSelfControl
	}
	return nil
}

// ctoSessionControlResult is what the CTO sees after stop, resume or restart.
type ctoSessionControlResult struct {
	Action    string                   `json:"action"`
	SessionID string                   `json:"session_id"`
	Name      string                   `json:"name,omitempty"`
	Note      string                   `json:"note,omitempty"`
	Start     *protocol.StartedSession `json:"start,omitempty"`
}

// ctoSessionControl stops, resumes or restarts an owner or agent session for
// the CTO. It uses the owner's own paths: the guarded stop of session_stop and
// the background resume of session_resume, on any machine through the gateway.
// It never opens a view; the owner opens the session with Enter (decision 0046).
func (d *Daemon) ctoSessionControl(ctx context.Context, concrete string, raw json.RawMessage) (*ctoSessionControlResult, error) {
	if proposalActor(ctx) == "" {
		return nil, errors.New("session control requires a CTO actor")
	}
	var q protocol.SessionResumeRequest
	if err := json.Unmarshal(raw, &q); err != nil || strings.TrimSpace(q.SessionID) == "" {
		return nil, fmt.Errorf("%s needs session_id", concrete)
	}
	// Only the session is taken from the CTO. A herdr location would open a view.
	id := strings.TrimSpace(q.SessionID)
	action := strings.TrimPrefix(concrete, "session_")
	view, err := d.agentView(ctx, id)
	if err != nil {
		return nil, errors.New("session is not known on this machine or through the gateway")
	}
	if view.Worker != nil || view.Type == "worker" {
		return nil, errors.New("use worker_change stop for a worker")
	}
	if action != "stop" && view.Harness == "pi" {
		return nil, errors.New("Pi cannot reopen an old session, so it cannot be resumed or restarted. Stop it and start a new Pi session")
	}
	out := &ctoSessionControlResult{Action: action, SessionID: id, Name: cleanLine(firstNonEmpty(view.Profile.Name, view.Title, view.Repo.Project, view.NativeID))}
	switch action {
	case "stop":
		res, err := d.sessionStop(ctx, id)
		if err != nil {
			return nil, err
		}
		out.Note = res.Note
	case "resume":
		start, err := d.ctoResume(ctx, id)
		if err != nil {
			return nil, err
		}
		out.Start = &start
		out.Note = resumeNote("Resumed", out.Name, start)
	case "restart":
		start, err := d.restartSession(ctx, id, view.Online)
		if err != nil {
			return nil, err
		}
		out.Start = &start
		out.Note = resumeNote("Restarted", out.Name, start)
	default:
		return nil, fmt.Errorf("unknown session control %q", concrete)
	}
	return out, nil
}

// resumeNote is the CTO's line after a resume or restart. A session that was
// already running here and was registered again is reported as that, not as a
// start.
func resumeNote(verb, name string, start protocol.StartedSession) string {
	if start.Attached {
		return name + " was already running on this machine. Nothing was started: agent-net registered it with the gateway again, and its queued messages are delivered now."
	}
	return verb + " " + name + " in the background. Press Enter on it in the Sessions tab to open it."
}

// restartSession stops a live session, waits until it is reported ended, then
// resumes it in the background. The CTO restart action and the MCP repair share
// this one path.
func (d *Daemon) restartSession(ctx context.Context, id string, online bool) (protocol.StartedSession, error) {
	if online {
		if _, err := d.sessionStop(ctx, id); err != nil {
			return protocol.StartedSession{}, err
		}
		if err := d.waitSessionEnded(ctx, id); err != nil {
			return protocol.StartedSession{}, err
		}
	}
	return d.ctoResume(ctx, id)
}

// ctoResume resumes the session with no herdr location, so no view opens, and
// waits for a background launch to finish so the CTO reports the real outcome.
func (d *Daemon) ctoResume(ctx context.Context, id string) (protocol.StartedSession, error) {
	start, err := d.sessionResume(ctx, protocol.SessionResumeRequest{SessionID: id})
	if err != nil {
		return start, err
	}
	deadline := time.Now().Add(ctoResumeStartWait)
	for start.Status == "starting" && start.ID != "" && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return start, ctx.Err()
		case <-time.After(ctoControlPoll):
		}
		next, err := d.sessionStartGet(ctx, start.ID)
		if err != nil {
			return start, err
		}
		start = next
	}
	if start.Status == "failed" {
		return start, fmt.Errorf("the resume failed: %s", cleanLine(firstNonEmpty(start.Error, "unknown error")))
	}
	return start, nil
}

// waitSessionEnded polls the gateway until the stopped session is reported offline.
func (d *Daemon) waitSessionEnded(ctx context.Context, id string) error {
	deadline := time.Now().Add(ctoRestartEndedWait)
	for {
		view, err := d.agentView(ctx, id)
		if err != nil {
			return err
		}
		// A stopped CTO keeps its network identity and is reported dormant.
		if !view.Online || (view.Type == "cto" && view.Status == "dormant") {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("the session did not end after the stop, so it was not resumed")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(ctoControlPoll):
		}
	}
}

// stopCTOSession is session_stop for the CTO on this machine. The CTO keeps its
// session ID and conversation; the next Enter, resume or message starts it.
func (d *Daemon) stopCTOSession(ctx context.Context) (protocol.SessionStopResult, error) {
	if err := d.ctoDispatchMu.lock(ctx); err != nil {
		return protocol.SessionStopResult{}, err
	}
	defer d.ctoDispatchMu.unlock()
	d.saveCTOBeforeStop(ctx)
	d.cto.mu.Lock()
	d.stopClaudeCTOLocked("owner_stop")
	d.stopOpenCodeCTOLocked("owner_stop")
	d.stopCodexCTOLocked("owner_stop")
	d.cto.mu.Unlock()
	if err := d.setCTOStoppedByOwner(true); err != nil {
		d.Logf("cto.stop owner flag: %v", err)
	}
	d.scheduleCTOPresence()
	return protocol.SessionStopResult{Note: "Stopped the CTO. The conversation is kept."}, nil
}

// saveCTOBeforeStop saves the conversation of a running Codex or OpenCode
// CTO, which lives in its process, before the owner stop ends that process.
// The reads run in their own goroutines and share one deadline, so a harness
// that hangs, or a lock a hung call holds, delays the stop by at most
// ctoStopSaveWait. Only this function writes, and only a read that finished in
// time, so a save left running can neither block the stop nor replace the
// earlier save with part of the conversation. The reads never start a process.
// A save that fails is logged and the stop goes on.
func (d *Daemon) saveCTOBeforeStop(ctx context.Context) {
	type read struct {
		harness string
		events  protocol.CTOEvents
		err     error
	}
	ctx, cancel := context.WithTimeout(ctx, ctoStopSaveWait)
	defer cancel()
	harnesses := []string{"opencode", "codex"}
	reads := make(chan read, len(harnesses))
	for _, harness := range harnesses {
		go func() {
			var r read
			r.harness = harness
			if harness == "codex" {
				r.events, r.err = d.codexCTORunningEvents(ctx)
			} else {
				r.events, r.err = d.openCodeCTORunningEvents(ctx)
			}
			reads <- r
		}()
	}
	for range harnesses {
		select {
		case r := <-reads:
			if errors.Is(r.err, errCTONotRunning) {
				continue
			}
			if r.err == nil {
				r.err = d.saveCTOHistory(r.harness, r.events)
			}
			if r.err != nil {
				d.Logf("cto.stop snapshot harness=%s: %v", r.harness, r.err)
			}
		case <-ctx.Done():
			d.Logf("cto.stop snapshot: not done after %s, the earlier save stays: %v", ctoStopSaveWait, ctx.Err())
			return
		}
	}
}

// resumeCTOSession is session_resume for the CTO on this machine: the CTO's
// own start, which resumes its conversation, or starts a new one when Claude
// says the old one is gone. It opens no view.
func (d *Daemon) resumeCTOSession(ctx context.Context, a protocol.AgentView) (protocol.StartedSession, error) {
	if d.ctoRunning() {
		return protocol.StartedSession{}, errors.New("session is already online")
	}
	if _, err := d.ctoEnterFor(ctx, "resume"); err != nil {
		return protocol.StartedSession{}, err
	}
	return protocol.StartedSession{ID: protocol.NewID("start"), Status: "started", Resumes: a.SessionID, SessionID: a.SessionID,
		NodeID: d.cfg.NodeID, Hostname: d.hostName(), Harness: a.Harness, Project: ctoViewName, StartedAt: time.Now().Unix()}, nil
}
