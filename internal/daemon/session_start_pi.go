package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func (d *Daemon) launchOwnerPi(r protocol.StartedSession, message, resume, socket, pane string, notify bool) (protocol.StartedSession, error) {
	bin, err := d.harnessBinary("pi")
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
		if err := d.bindRepoStart(d.ctoCtx, r); err != nil {
			return protocol.StartedSession{}, err
		}
		piArgs = []string{bin, "--session-id", native, "--name", r.Project}
		if r.Model != "" {
			piArgs = append(piArgs, "--model", r.Model)
		}
	} else {
		r.NativeID = resume
		piArgs = []string{bin, "--session", resume}
	}
	// Pi 1.5 defaults to tuiMode fullscreen, which turns mouse capture on and
	// stops the terminal's own text selection, so copy-paste fails. Regular mode
	// leaves the mouse to the terminal. Claude sessions get the same choice
	// from the user's Claude settings (decision 0128); Pi has no env var for it.
	piArgs = append(piArgs, "--tui-mode", "regular")
	timeout := d.workerSettings("").StartTimeout
	plan := protocol.EnterPlan{Argv: piArgs, Dir: r.Path, Host: r.Hostname}
	opened := d.viewPlan("pi:"+r.NativeID, r.Project, socket, pane, true, plan)
	if !opened.OpenedHerdr {
		return protocol.StartedSession{}, fmt.Errorf("cannot start Pi in herdr on %s: %s", d.hostName(), opened.Note)
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
			if ls := d.sessions[key("pi", r.NativeID)]; ls != nil {
				r.SessionID = ls.sessionID
			}
			d.mu.Unlock()
			if r.SessionID != "" {
				break
			}
			select {
			case <-cctx.Done():
				startErr = fmt.Errorf("the daemon did not register the Pi session: %w", cctx.Err())
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
