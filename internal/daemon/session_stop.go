package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// sessionStop is owner-gated by dispatchOperator. It never signals processes
// or kills a terminal: only the harness's background home may end a session.
func (d *Daemon) sessionStop(ctx context.Context, id string) (protocol.SessionStopResult, error) {
	no := func(err error) (protocol.SessionStopResult, error) { return protocol.SessionStopResult{}, err }
	ls := d.sessionByID(id)
	if ls == nil {
		return no(errors.New("session_stop supports local sessions only for now"))
	}
	info := d.infoOf(ls)
	if info.Worker != nil {
		return no(errors.New("use worker_stop for a worker"))
	}
	name := cleanLine(firstNonEmpty(info.Name, info.NativeID))
	result := protocol.SessionStopResult{Note: "Stopped " + name + ". The conversation is kept."}
	terminal := fmt.Errorf("%s runs in your terminal on %s: end it there", name, d.hostName())
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	switch info.Harness {
	case "claude":
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
		if err := exec.CommandContext(ctx, bin, "stop", job).Run(); err != nil {
			return no(fmt.Errorf("Claude could not stop %s: %w", name, err))
		}
	case "opencode":
		service, err := ownerOpenCodeService()
		if err != nil {
			return no(fmt.Errorf("the OpenCode service is not running on %s", d.hostName()))
		}
		if info.NativeID == "" {
			return no(errors.New("OpenCode session ID is missing"))
		}
		api := openCodeWorkerAPI(ctx, service.URL, service.Password)
		directory := strings.ReplaceAll(url.QueryEscape(info.Cwd), "+", "%20")
		_, status, err := api("POST", "/api/session/"+url.PathEscape(info.NativeID)+"/abort", nil, "x-opencode-directory", directory)
		if err != nil {
			return no(errors.New("could not abort the OpenCode turn"))
		}
		if status < 200 || status >= 300 {
			return no(fmt.Errorf("OpenCode refused to abort the turn (HTTP %d)", status))
		}
		result.Note = name + ": turn aborted; the conversation stays in the service. An idle service session costs nothing."
	case "pi":
		bin, err := exec.LookPath("tmux")
		if err != nil {
			return no(terminal)
		}
		// Query only our private server. A similarly named socket in the process's
		// environment is not evidence that it is safe to kill the owner's window.
		pane, ok := findTmuxPaneWithArgs(info.PID, bin, "", []string{"-L", piTmuxServer, "list-panes", "-a", "-F", "#{pane_pid}|#{pane_id}|#{session_name}"})
		if !ok || !strings.HasPrefix(pane.id, "%") {
			return no(terminal)
		}
		if err := exec.CommandContext(ctx, bin, "-L", piTmuxServer, "kill-window", "-t", pane.id).Run(); err != nil {
			return no(fmt.Errorf("tmux could not stop %s: %w", name, err))
		}
	default:
		return no(terminal)
	}
	return result, nil
}
