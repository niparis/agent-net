package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/agentfiles"
	"github.com/niparis/agent-net/internal/daemon/opencodeapi"
	"github.com/niparis/agent-net/internal/protocol"
)

// sessionStop is owner-gated by dispatchOperator. It never signals processes
// or kills a terminal: only the harness's background home may end a session.
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
	displayName := name
	if folder := firstNonEmpty(info.Cwd, info.Repo.Worktree, info.Repo.Root); folder != "" {
		displayName = fmt.Sprintf("%s (%s)", name, agentfiles.DisplayPath(folder))
	}
	terminal := fmt.Errorf("%s runs in your terminal on %s: end it there", displayName, d.hostName())
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	switch info.Harness {
	case "claude":
		// Serialize an explicit stop with automatic wake validation and launch.
		d.claudeStartMu.Lock()
		defer d.claudeStartMu.Unlock()
		p, ok := d.adapters["claude"].(EnterRefProvider)
		if !ok {
			return no(terminal)
		}
		kind, job := p.EnterRef(info.NativeID)
		if kind != "bg" {
			return no(terminal)
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
		service, err := d.ownerOpenCodeService()
		if err != nil {
			return no(fmt.Errorf("the OpenCode service is not running on %s", d.hostName()))
		}
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
		if err != nil {
			return no(errors.New("could not abort the OpenCode turn"))
		}
		if status < 200 || status >= 300 {
			return no(fmt.Errorf("OpenCode refused to abort the turn (HTTP %d)", status))
		}
		result.Note = name + ": turn aborted; the conversation stays in the service. An idle service session costs nothing."
	case "pi":
		h, found := findHerdrPane(info.PID)
		fromServer := false
		if !found {
			h, found = findHerdrServer(info.PID)
			fromServer = found
		}
		if !found {
			return no(terminal)
		}
		bin := d.herdrBinary(h.bin)
		if bin == "" {
			return no(terminal)
		}
		var panes []herdrPaneInfo
		if fromServer {
			var ok bool
			panes, h.socket, ok = d.herdrPanesFromServer(bin, h)
			if !ok {
				return no(terminal)
			}
		} else {
			var err error
			panes, err = d.herdrPanes(bin, h.socket)
			if err != nil {
				return no(terminal)
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
		return no(terminal)
	default:
		return no(terminal)
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
