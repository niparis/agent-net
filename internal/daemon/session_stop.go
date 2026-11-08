package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/niparis/agent-net/internal/daemon/opencodeapi"
	"github.com/niparis/agent-net/internal/protocol"
)

// sessionStop is owner-gated by dispatchOperator. Terminal sessions are ended
// through herdr or by signalling only their registered harness process.
// A session on another machine is relayed through the gateway.
func (d *Daemon) sessionStop(ctx context.Context, id string) (protocol.SessionStopResult, error) {
	no := func(err error) (protocol.SessionStopResult, error) { return protocol.SessionStopResult{}, err }
	if d.sessionByID(id) == nil {
		a, err := d.agentView(ctx, id)
		if err != nil {
			return no(errors.New("session is not known on this machine or through the gateway"))
		}
		if a.NodeID != "" && a.NodeID != d.cfg.NodeID {
			return d.relaySessionStop(ctx, a.NodeID, id)
		}
	}
	return d.sessionStopLocal(ctx, id)
}

// relaySessionStop asks the gateway to stop a session on another machine.
func (d *Daemon) relaySessionStop(ctx context.Context, nodeID, id string) (protocol.SessionStopResult, error) {
	owner := d.ownerSessionID()
	if owner == "" {
		return protocol.SessionStopResult{}, errors.New("the owner session is not registered yet (is the gateway reachable?)")
	}
	data, err := d.call(ctx, protocol.FrameSessionStop, protocol.SessionRelay{Owner: owner, NodeID: nodeID, SessionID: id})
	if err != nil {
		return protocol.SessionStopResult{}, tooOldGateway(err)
	}
	var res protocol.SessionStopResult
	if err := json.Unmarshal(data, &res); err != nil {
		return protocol.SessionStopResult{}, err
	}
	return res, nil
}

// sessionStopLocal stops a session on this machine. It is also the gateway's
// path, so it refuses a session that is not here instead of relaying again.
func (d *Daemon) sessionStopLocal(ctx context.Context, id string) (protocol.SessionStopResult, error) {
	no := func(err error) (protocol.SessionStopResult, error) { return protocol.SessionStopResult{}, err }
	ls := d.sessionByID(id)
	if ls == nil {
		return no(errors.New("session is not known on this machine"))
	}
	info := d.infoOf(ls)
	if info.Worker != nil {
		return no(errors.New("use worker_stop for a worker"))
	}
	name := ""
	if view, err := d.agentView(ctx, id); err == nil && view.Profile.Name != "" {
		name = cleanLine(view.Profile.Name)
	}
	if name == "" {
		name = cleanLine(firstNonEmpty(info.Name, info.Repo.Project))
	}
	if name == "" && strings.TrimSpace(info.Cwd) != "" && filepath.Clean(info.Cwd) != "/" && filepath.Clean(info.Cwd) != "." {
		name = cleanLine(filepath.Base(filepath.Clean(info.Cwd)))
	}
	if name == "" {
		name = cleanLine(info.NativeID)
	}
	result := protocol.SessionStopResult{Note: "Stopped " + name + ". The conversation is kept."}
	terminalStop := func() (protocol.SessionStopResult, error) {
		pid, token, err := d.stopTarget(ls, info)
		if err != nil {
			return no(err)
		}
		d.markOwnerStop(ls, true)
		fail := func(err error) (protocol.SessionStopResult, error) { d.markOwnerStop(ls, false); return no(err) }
		if h, found := findHerdrPane(pid); found {
			bin := d.herdrBinary(h.bin)
			if bin != "" {
				panes, err := d.herdrPanes(bin, h.socket)
				if err != nil {
					return fail(err)
				}
				for _, pane := range panes {
					if herdrIDRe.MatchString(pane.PaneID) && d.herdrHolds(bin, h.socket, pane.PaneID, pidChain(pid)) {
						if _, err := d.herdrRun(bin, h.socket, "pane", "close", pane.PaneID); err != nil {
							return fail(err)
						}
						return result, nil
					}
				}
			}
		}
		if err := stopSessionProcess(ctx, pid, token, 5*time.Second); err != nil {
			return fail(err)
		}
		return result, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	switch info.Harness {
	case "claude":
		// Serialize an explicit stop with automatic wake validation and launch.
		d.claudeStartMu.Lock()
		defer d.claudeStartMu.Unlock()
		p, ok := d.adapters["claude"].(EnterRefProvider)
		if !ok {
			return terminalStop()
		}
		kind, job := p.EnterRef(info.NativeID)
		if kind != "bg" {
			return terminalStop()
		}
		if job == "" && len(info.NativeID) >= 8 {
			job = info.NativeID[:8]
		}
		if job == "" || strings.HasPrefix(job, "-") {
			return no(errors.New("Claude background job ID is missing or invalid"))
		}
		bin, err := d.harnessBinary("claude")
		if err != nil {
			return no(err)
		}
		d.markOwnerStop(ls, true)
		if err := exec.CommandContext(ctx, bin, "stop", job).Run(); err != nil {
			d.markOwnerStop(ls, false)
			return no(fmt.Errorf("Claude could not stop %s: %w", name, err))
		}
	case "opencode":
		if info.Mode == protocol.ModeForeground {
			return terminalStop()
		}
		service, err := d.ownerOpenCodeService()
		if err != nil {
			return no(fmt.Errorf("the OpenCode service is not running on %s", d.hostName()))
		}
		if info.Status == "busy" {
			if info.NativeID == "" {
				return no(errors.New("OpenCode session ID is missing"))
			}
			routes, err := d.discoverOpenCodeRoutes(ctx, service.URL, service.Password, "", service.identity)
			if err != nil {
				return no(fmt.Errorf("could not discover OpenCode routes: %w", err))
			}
			path, err := routes.Path(opencodeapi.Interrupt, info.NativeID)
			if err != nil {
				return no(err)
			}
			api := openCodeWorkerAPI(ctx, service.URL, service.Password)
			directory := strings.ReplaceAll(url.QueryEscape(info.Cwd), "+", "%20")
			_, status, err := api(opencodeapi.Interrupt.Method(), path, nil, "x-opencode-directory", directory)
			if err == nil && status == http.StatusNotFound {
				if fallback, ok := routes.InterruptFallback(info.NativeID); ok {
					_, status, err = api(opencodeapi.Interrupt.Method(), fallback, nil, "x-opencode-directory", directory)
				}
			}
			if err != nil {
				return no(fmt.Errorf("could not interrupt the OpenCode turn: %w", err))
			}
			if status < 200 || status >= 300 {
				return no(routes.HTTPError(opencodeapi.Interrupt, status))
			}
		}
		d.mu.Lock()
		ls.info.OpenCodeActivity = 0
		ls.info.Status = "idle"
		d.mu.Unlock()
		if _, err := d.call(ctx, "session.remove", protocol.SessionRemove{SessionID: id, Closed: true}); err != nil {
			return no(err)
		}
		result.Note = "Closed " + name + ". The conversation is kept; press v to see ended sessions and Enter to reopen it."
	case "pi":
		h, found := findHerdrPane(info.PID)
		fromServer := false
		if !found {
			h, found = findHerdrServer(info.PID)
			fromServer = found
		}
		if !found {
			return terminalStop()
		}
		bin := d.herdrBinary(h.bin)
		if bin == "" {
			return terminalStop()
		}
		var panes []herdrPaneInfo
		if fromServer {
			var ok bool
			panes, h.socket, ok = d.herdrPanesFromServer(bin, h)
			if !ok {
				return terminalStop()
			}
		} else {
			var err error
			panes, err = d.herdrPanes(bin, h.socket)
			if err != nil {
				return terminalStop()
			}
		}
		for _, pane := range panes {
			if herdrIDRe.MatchString(pane.PaneID) && d.herdrHolds(bin, h.socket, pane.PaneID, pidChain(info.PID)) {
				d.markOwnerStop(ls, true)
				if _, err := d.herdrRun(bin, h.socket, "pane", "close", pane.PaneID); err != nil {
					d.markOwnerStop(ls, false)
					return no(fmt.Errorf("herdr could not stop %s: %w", name, err))
				}
				return result, nil
			}
		}
		return terminalStop()
	default:
		return terminalStop()
	}
	return result, nil
}

// Mark the request before the harness exits, so discovery does not mistake an
// owner stop for an unexpected disappearance.
func (d *Daemon) markOwnerStop(ls *localSession, stopped bool) {
	d.mu.Lock()
	ls.ownerStopped = stopped
	d.mu.Unlock()
}

// stopSessionProcess never signals a parent shell or a process that replaced
// the registered harness. Recheck identity before escalation as well.
func stopSessionProcess(ctx context.Context, pid int, token string, grace time.Duration) error {
	matches := func() bool { now, live := procToken(pid); return pid > 1 && token != "" && live && now == token }
	if !matches() {
		return errors.New("session process no longer matches its registered process")
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return err
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		if !matches() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		case <-timer.C:
			if matches() {
				return syscall.Kill(pid, syscall.SIGKILL)
			}
			return nil
		}
	}
}
