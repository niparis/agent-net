package daemon

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/redact"
)

// Pi has no background mode with a screen. agent-net hosts it in Herdr, or in
// its own tmux server when Herdr is unavailable. Both outlive the daemon.
const piTmuxServer = "agent-net"

// piTmuxStartArgv is the exact command that hosts a new Pi session. It is
// built here so a test can check every word.
func piTmuxStartArgv(tmux, folder, bin, session, project, model string) []string {
	argv := []string{tmux, "-L", piTmuxServer, "new-session", "-d", "-c", folder, "--",
		bin, "--session-id", session, "--name", project}
	if model != "" {
		argv = append(argv, "--model", model)
	}
	return argv
}

// piTmuxResumeArgv opens an ended conversation again. A new window keeps the
// owner's other Pi sessions on the same server; a new session starts the
// server when they are all gone.
func piTmuxResumeArgv(tmux, folder, bin, session string, newSession bool) []string {
	sub := "new-window"
	if newSession {
		sub = "new-session"
	}
	return []string{tmux, "-L", piTmuxServer, sub, "-d", "-c", folder, "--", bin, "--session", session}
}

// launchOwnerPi starts Pi in Herdr or the private tmux server and waits for the
// extension to register the session. A nonempty message is sent afterwards as
// an owner message, the same path as a worker's first task. resume is the
// native ID of an ended session, or "" for a new one.
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
		r.NativeID = native
		piArgs = []string{bin, "--session-id", native, "--name", r.Project}
		if r.Model != "" {
			piArgs = append(piArgs, "--model", r.Model)
		}
	} else {
		r.NativeID = resume
		piArgs = []string{bin, "--session", resume}
	}
	timeout := d.cfg.Workers.filled().StartTimeout
	plan := protocol.EnterPlan{Argv: piArgs, Dir: r.Path, Host: r.Hostname}
	opened := d.openForCaller("pi:"+r.NativeID, r.Project, socket, pane, plan)
	if opened.OpenedHerdr {
		r.EnterPlan = &opened
	} else {
		if len(opened.Argv) == 0 {
			return protocol.StartedSession{}, errors.New(opened.Note)
		}
		tmux, err := exec.LookPath("tmux")
		if err != nil {
			return protocol.StartedSession{}, fmt.Errorf("pi needs tmux on %s to run in the background. %s", d.hostName(), herdrInstallNote)
		}
		var argv []string
		if resume == "" {
			argv = piTmuxStartArgv(tmux, r.Path, bin, r.NativeID, r.Project, r.Model)
		} else {
			argv = piTmuxResumeArgv(tmux, r.Path, bin, resume, !piTmuxHasSession(tmux))
		}
		startCtx, cancel := context.WithTimeout(d.ctoCtx, timeout)
		out, err := exec.CommandContext(startCtx, argv[0], argv[1:]...).CombinedOutput()
		cancel()
		if err != nil {
			return protocol.StartedSession{}, fmt.Errorf("tmux could not start the Pi session: %s", tmuxFailure(out, err))
		}
		r.EnterNote = opened.Note
	}
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

// piTmuxHasSession says whether the private server already has a session to
// put a window in.
func piTmuxHasSession(tmux string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, tmux, "-L", piTmuxServer, "has-session").Run() == nil
}

// tmuxFailure is one clean line for a failed tmux command.
func tmuxFailure(out []byte, err error) string {
	text := strings.Join(strings.Fields(redact.Secrets(string(out))), " ")
	if text == "" {
		text = err.Error()
	}
	return redact.Cut(text, 300)
}
