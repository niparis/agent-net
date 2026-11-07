package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func (d *Daemon) launchOwnerPi(r protocol.StartedSession, message, resume, socket, pane string, notify bool) (protocol.StartedSession, error) {
	if r.Harness == "" {
		r.Harness = "pi"
	}
	bin, err := d.harnessBinary(r.Harness)
	if err != nil {
		return protocol.StartedSession{}, err
	}
	var piArgs []string
	if resume == "" {
		native, err := newUUID()
		if err != nil {
			return protocol.StartedSession{}, err
		}
		if r.AgentID != "" {
			if err := d.reserveStartAgent(d.ctoCtx, "pi", native, r.AgentID, r.ID, r.Path, r.Project); err != nil {
				return protocol.StartedSession{}, err
			}
		}
		r.NativeID = native
		piArgs = []string{bin, "--session-id", native, "--name", r.Project}
		if r.Model != "" {
			piArgs = append(piArgs, "--model", r.Model)
		}
	} else {
		r.NativeID = resume
		piArgs = []string{bin, "--session", resume}
		if r.Harness == "codex" {
			piArgs = []string{bin, "resume", resume}
		}
	}
	timeout := d.workerSettings("").StartTimeout
	plan := protocol.EnterPlan{Argv: piArgs, Dir: r.Path, Host: r.Hostname}
	opened := d.openInHerdr(r.Harness+":"+r.NativeID, r.Project, socket, pane, plan)
	if !opened.OpenedHerdr {
		return protocol.StartedSession{}, fmt.Errorf("cannot start %s in herdr on %s: %s", r.Harness, d.hostName(), opened.Note)
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
		cctx, cancel := context.WithTimeout(d.ctoCtx, timeout)
		defer cancel()
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		var startErr error
		for r.SessionID == "" {
			d.mu.Lock()
			if ls := d.sessions[key(r.Harness, r.NativeID)]; ls != nil {
				r.SessionID = ls.sessionID
			}
			d.mu.Unlock()
			if r.SessionID != "" {
				break
			}
			select {
			case <-cctx.Done():
				startErr = fmt.Errorf("the daemon did not register the %s session: %w", r.Harness, cctx.Err())
			case <-ticker.C:
			}
			if startErr != nil {
				break
			}
		}
		if startErr == nil {
			startErr = d.checkStartAgent(cctx, r)
		}
		if startErr == nil && message != "" {
			sender := d.ownerSessionID()
			if sender == "" {
				startErr = errors.New("the owner session is not registered yet")
			} else if _, err := d.send(cctx, protocol.MsgSend{Sender: sender, Recipient: r.SessionID,
				Type: protocol.TypeRequest, Body: message}); err != nil {
				startErr = fmt.Errorf("could not send the first message to Pi: %w", err)
			}
		}
		if startErr != nil {
			r.Status, r.Error = "failed", startErr.Error()
			d.failStartAgent(r)
		} else {
			r.Status = "started"
		}
		d.mu.Lock()
		d.startedSessions[r.ID] = r
		d.mu.Unlock()
		if notify && r.Status == "started" {
			d.notifySessionStart(r)
		}
	}(r)
	return r, nil
}
