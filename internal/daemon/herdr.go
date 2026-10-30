package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Entering a session that runs in herdr (decision 0002, W16; herdr is a
// terminal multiplexer for coding agents).
//
// herdr is client/server. The panes live in the server, and the owner's
// terminal is a client. Every process in a pane has HERDR_ENV=1, HERDR_PANE_ID
// and HERDR_SOCKET_PATH (the server) in its environment. `herdr agent focus`
// only changes the server's idea of the focused pane: a client that is already
// attached keeps showing the tab it is on (checked on herdr 0.8.0), so it
// cannot take the owner to the pane. `herdr agent attach <pane>` gives this
// terminal a direct view of the pane instead, and ctrl+b q gives it back. That
// works the same from outside herdr, from another herdr server and from a pane
// of the same server, so there is one plan.
//
// Two direct attaches cannot share a terminal. Without --takeover the second
// one fails with a clear error and the first stays. With --takeover the first
// one is thrown out. The owner's normal herdr client is not a direct attach,
// so it is not touched either way. The plan never takes over: it must not
// drop an attach the owner opened on purpose.

// herdrPane is where a session's process runs in herdr.
type herdrPane struct {
	id, socket, bin string
}

var herdrIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_.-]{0,63}$`)

// findHerdrPane reads the herdr variables of pid, or of the closest parent that
// has them (a launcher may have cleaned the environment of the child).
func findHerdrPane(pid int) (herdrPane, bool) {
	for p, depth := pid, 0; p > 1 && depth < 64; depth++ {
		var h herdrPane
		isHerdr := false
		for _, kv := range processEnviron(p) {
			k, v, _ := strings.Cut(kv, "=")
			switch k {
			case "HERDR_ENV":
				isHerdr = v == "1"
			case "HERDR_PANE_ID":
				h.id = v
			case "HERDR_SOCKET_PATH":
				h.socket = v
			case "HERDR_BIN_PATH":
				h.bin = v
			}
		}
		if isHerdr && herdrIDRe.MatchString(h.id) {
			if h.socket != "" && !filepath.IsAbs(h.socket) {
				h.socket = ""
			}
			return h, true
		}
		parent, err := parentPID(p)
		if err != nil {
			break
		}
		p = parent
	}
	return herdrPane{}, false
}

// herdrBinary finds the herdr program: the pane's own HERDR_BIN_PATH, the
// configured path, PATH, then ~/.local/bin (a service may not have it in PATH).
func (d *Daemon) herdrBinary(hint string) string {
	for _, c := range []string{hint, d.cfg.HarnessPaths["herdr"]} {
		if c != "" && filepath.IsAbs(c) && isExecutable(c) {
			return c
		}
	}
	if p, err := exec.LookPath("herdr"); err == nil {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		if c := filepath.Join(home, ".local", "bin", "herdr"); isExecutable(c) {
			return c
		}
	}
	return ""
}

func isExecutable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0
}

// herdrRun runs a herdr command against one server and returns its output.
func herdrRun(bin, socket string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HERDR_SESSION=") && !strings.HasPrefix(kv, "HERDR_SOCKET_PATH=") {
			env = append(env, kv)
		}
	}
	if socket != "" {
		env = append(env, "HERDR_SOCKET_PATH="+socket)
	}
	cmd.Env = env
	return cmd.Output()
}

type herdrPaneInfo struct {
	PaneID     string `json:"pane_id"`
	TerminalID string `json:"terminal_id"`
	Agent      string `json:"agent"`
}

// herdrPanes lists the panes of a server.
func herdrPanes(bin, socket string) ([]herdrPaneInfo, error) {
	out, err := herdrRun(bin, socket, "pane", "list")
	if err != nil {
		return nil, err
	}
	var r struct {
		Result struct {
			Panes []herdrPaneInfo `json:"panes"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	return r.Result.Panes, nil
}

// herdrHolds says whether the pane runs one of the pids (its shell, or a
// process of its foreground group).
func herdrHolds(bin, socket, pane string, pids map[int]bool) bool {
	out, err := herdrRun(bin, socket, "pane", "process-info", "--pane", pane)
	if err != nil {
		return false
	}
	var r struct {
		Result struct {
			Info struct {
				ShellPID int `json:"shell_pid"`
				Procs    []struct {
					PID int `json:"pid"`
				} `json:"foreground_processes"`
			} `json:"process_info"`
		} `json:"result"`
	}
	if json.Unmarshal(out, &r) != nil {
		return false
	}
	if r.Result.Info.ShellPID > 1 && pids[r.Result.Info.ShellPID] {
		return true
	}
	for _, p := range r.Result.Info.Procs {
		if p.PID > 1 && pids[p.PID] {
			return true
		}
	}
	return false
}

// pidChain is pid and its parents.
func pidChain(pid int) map[int]bool {
	chain := map[int]bool{}
	for p, depth := pid, 0; p > 1 && depth < 64; depth++ {
		chain[p] = true
		parent, err := parentPID(p)
		if err != nil {
			break
		}
		p = parent
	}
	return chain
}

// herdrPlan takes the owner to the herdr pane of a session. ok is false when
// the session does not run in herdr: the caller then tries tmux. When ok is
// true the plan is final, with a command or with a note that says why not.
// viaGateway is true when another machine asked: whether its terminal is
// inside herdr is not known, so the note names both detach keys.
func (d *Daemon) herdrPlan(info protocol.SessionInfo, q protocol.EnterRequest, plan protocol.EnterPlan, name string, viaGateway bool) (protocol.EnterPlan, bool) {
	h, found := findHerdrPane(info.PID)
	if !found {
		return protocol.EnterPlan{}, false
	}
	where := firstNonEmpty(plan.Host, "its machine")
	note := func(format string, a ...any) (protocol.EnterPlan, bool) {
		return protocol.EnterPlan{Host: plan.Host, Note: fmt.Sprintf(format, a...)}, true
	}
	bin := d.herdrBinary(h.bin)
	if bin == "" {
		return note("%s runs in herdr, but herdr is not installed where agent-net can find it: open its window on %s", name, where)
	}
	panes, err := herdrPanes(bin, h.socket)
	if err != nil {
		return note("%s runs in herdr, but the herdr server does not answer: open its window on %s", name, where)
	}
	// The pane in the environment is the one the process started in. A moved
	// pane gets a new ID, so check that the pane still holds the process, and
	// look at the other panes if it does not.
	pids := pidChain(info.PID)
	var pane *herdrPaneInfo
	for i := range panes {
		if panes[i].PaneID == h.id && herdrHolds(bin, h.socket, h.id, pids) {
			pane = &panes[i]
			break
		}
	}
	for i := 0; pane == nil && i < len(panes); i++ {
		if panes[i].PaneID != h.id && herdrHolds(bin, h.socket, panes[i].PaneID, pids) {
			pane = &panes[i]
		}
	}
	if pane == nil {
		return note("%s was in herdr pane %s, which has closed, and no other pane of that herdr server holds it: open its window on %s", name, h.id, where)
	}
	argv := []string{"env", "-u", "HERDR_SESSION"}
	if h.socket != "" {
		argv = append(argv, "HERDR_SOCKET_PATH="+h.socket)
	}
	switch {
	case pane.Agent != "" && herdrIDRe.MatchString(pane.PaneID):
		argv = append(argv, bin, "agent", "attach", pane.PaneID)
	case herdrIDRe.MatchString(pane.TerminalID): // herdr does not know an agent there: attach the terminal
		argv = append(argv, bin, "terminal", "attach", pane.TerminalID)
	default:
		return note("%s runs in herdr pane %s, which cannot be attached: open its window on %s", name, pane.PaneID, where)
	}
	detach := "ctrl+b q"
	switch {
	case viaGateway:
		detach = "ctrl+b q (ctrl+b ctrl+b q if your terminal is itself in herdr)"
	case q.HerdrSocket != "":
		detach = "ctrl+b ctrl+b q (the first ctrl+b goes to the herdr this terminal runs in)"
	}
	return protocol.EnterPlan{
		Argv: argv, Host: plan.Host,
		Note: fmt.Sprintf("Attached to the herdr pane of %s. Press %s to come back here; it keeps running. If herdr says the terminal already has an attached client, close that other attach first.", name, detach),
	}, true
}
