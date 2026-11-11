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
)

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
	if view.Type == "cto" || id == d.ctoSessionID() {
		return nil, errors.New("the CTO cannot " + action + " its own session")
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
		out.Note = "Resumed " + out.Name + " in the background. Press Enter on it in the Sessions tab to open it."
	case "restart":
		start, err := d.restartSession(ctx, id, view.Online)
		if err != nil {
			return nil, err
		}
		out.Start = &start
		out.Note = "Restarted " + out.Name + " in the background. Press Enter on it in the Sessions tab to open it."
	default:
		return nil, fmt.Errorf("unknown session control %q", concrete)
	}
	return out, nil
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
		if !view.Online {
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
