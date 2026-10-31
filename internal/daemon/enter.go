package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Enter a session (decision 0002, W16). The owner presses a key on a session in
// the TUI. The daemon says what to run to take over the terminal for it, and
// the TUI runs that and comes back when it ends. The TUI builds no command, so
// the phone app can show the same plan.
//
//	Claude Code background session   claude attach <id>
//	Claude Code, Pi in a terminal    its herdr pane (herdr agent attach), else
//	                                 its tmux pane, when there is one
//	OpenCode                         opencode --session <id> (in the folder)
//	Codex (not a worker)             codex resume <thread>, on the shared app server
//	a worker of OpenCode or Codex    their private server, with its password
//	                                 in Env; only on the machine that has it
//
// A session on another machine is planned by that machine (the gateway relays
// the question), and wrapped in `ssh -t` here unless enter_ssh is false in
// node.json. Without it the plan has no argv and the note gives the command to
// run there. The ssh plan also carries that command as Fallback, for when ssh
// cannot connect (exit status 255).
//
// Same gate as session_text: the owner's CLI or TUI, never an agent session.

// EnterRefProvider is implemented by the Claude adapter: the kind of a session
// ("interactive", "bg", ...) and its background job ID, from its session file.
type EnterRefProvider interface {
	EnterRef(nativeID string) (kind, job string)
}

// enterPlan answers session_enter for a session on any machine.
func (d *Daemon) enterPlan(ctx context.Context, q protocol.EnterRequest) (protocol.EnterPlan, error) {
	if d.sessionByID(q.SessionID) != nil {
		return d.localEnter(ctx, q, false)
	}
	// Another machine: it knows its own processes. Our terminal means nothing
	// to it, so the socket is not sent.
	data, err := d.call(ctx, "query.session_enter", protocol.EnterRequest{SessionID: q.SessionID})
	if err != nil {
		return protocol.EnterPlan{}, err
	}
	var plan protocol.EnterPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return protocol.EnterPlan{}, err
	}
	// The other machine's words are shown on this terminal: no control codes.
	plan.Note = cleanLine(plan.Note)
	plan.Host, plan.Env = cleanLine(plan.Host), nil // a secret never crosses the gateway; ignore one
	if len(plan.Argv) == 0 {
		return protocol.EnterPlan{Host: plan.Host, Note: plan.Note}, nil
	}
	// The machine to ssh to is the one the gateway says holds the session, not
	// the name in the answer.
	host := d.hostOfSession(ctx, q.SessionID)
	run := remoteCommand(plan.Dir, plan.Argv)
	where := firstNonEmpty(host, "the other machine")
	fallback := fmt.Sprintf("On %s run: %s", where, run)
	if host == "" {
		return protocol.EnterPlan{Host: host, Note: fallback}, nil
	}
	if !d.cfg.EnterSSH {
		return protocol.EnterPlan{Host: host, Note: fallback + "   (enter_ssh is off in node.json)"}, nil
	}
	target := host
	if t := d.cfg.EnterSSHHosts[host]; t != "" {
		target = t
	}
	if !sshTargetOK(target) {
		return protocol.EnterPlan{Host: host, Note: fallback + "   (enter_ssh_hosts gives a bad ssh target)"}, nil
	}
	return protocol.EnterPlan{Argv: []string{"ssh", "-t", "-o", "ConnectTimeout=5", "--", target, run}, Host: host,
		Note: "Through ssh to " + target + ". " + plan.Note, Fallback: fallback}, nil
}

// hostOfSession is the host name the gateway lists for a session, or "".
func (d *Daemon) hostOfSession(ctx context.Context, id string) string {
	data, err := d.call(ctx, "query.agents", protocol.QueryAgents{IncludeOwners: true})
	if err != nil {
		return ""
	}
	var r protocol.AgentsResult
	if json.Unmarshal(data, &r) != nil {
		return ""
	}
	for _, a := range r.Agents {
		if a.SessionID == id {
			return cleanLine(a.Hostname)
		}
	}
	return ""
}

var sshTargetRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.@:-]*$`)

// sshTargetOK keeps a target that could be read as an ssh option, or that has
// spaces or shell characters, out of the command.
func sshTargetOK(s string) bool { return len(s) <= 253 && sshTargetRe.MatchString(s) }

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`;&|<>(){}[]*?#~!") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// remoteCommand is the line ssh runs on the other machine.
func remoteCommand(dir string, argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = shellQuote(a)
	}
	line := "exec " + strings.Join(q, " ")
	if dir != "" {
		line = "cd " + shellQuote(dir) + " && " + line
	}
	return line
}

// localEnter plans the entry of a session on this machine. viaGateway is true
// when the question came from another machine: a plan that needs a secret in
// its environment is refused then, because the answer crosses the gateway.
func (d *Daemon) localEnter(ctx context.Context, q protocol.EnterRequest, viaGateway bool) (protocol.EnterPlan, error) {
	ls := d.sessionByID(q.SessionID)
	if ls == nil {
		return protocol.EnterPlan{}, errors.New("no such session on this machine")
	}
	info := d.infoOf(ls)
	plan := protocol.EnterPlan{Host: d.hostName()}
	name := firstNonEmpty(info.Name, info.NativeID)
	if st, err := os.Stat(info.Cwd); err == nil && st.IsDir() {
		plan.Dir = info.Cwd
	}
	w := d.workerBySession(q.SessionID)
	no := func(format string, a ...any) (protocol.EnterPlan, error) {
		return protocol.EnterPlan{Host: plan.Host, Note: fmt.Sprintf(format, a...)}, nil
	}
	switch info.Harness {
	case protocol.OwnerHarness:
		return no("that is you")
	case "claude":
		kind, job := "", ""
		if p, ok := d.adapters["claude"].(EnterRefProvider); ok {
			kind, job = p.EnterRef(info.NativeID)
		}
		if kind != "bg" {
			return d.terminalPlan(info, q, plan, name, viaGateway)
		}
		id := job
		if w != nil && w.ref != "" {
			id = w.ref
		}
		if id == "" && len(info.NativeID) >= 8 {
			id = info.NativeID[:8] // the job ID is the first 8 characters of the session ID
		}
		bin, err := d.harnessBinary("claude")
		if err != nil || id == "" {
			return no("cannot attach to %s: %v", name, firstNonNil(err, errors.New("no background id")))
		}
		plan.Argv = []string{bin, "attach", id}
		plan.Note = fmt.Sprintf("Press ctrl+] to come back here; %s keeps running.", name)
		return plan, nil
	case "opencode":
		if w == nil { // a TUI the owner started in a herdr pane is the screen to show
			if p, ok := d.herdrPlan(info, q, plan, name, viaGateway); ok {
				return p, nil
			}
		}
		bin, err := d.harnessBinary("opencode")
		if err != nil {
			return no("%v", err)
		}
		if w == nil {
			plan.Argv = []string{bin, "--session", info.NativeID}
			plan.Note = fmt.Sprintf("Press ctrl+] to come back here; %s keeps running.", name)
			return plan, nil
		}
		// A worker lives in its own private server, with a password that the
		// daemon keeps in memory.
		d.wmu.Lock()
		base, pass := w.apiBase, w.apiPass
		d.wmu.Unlock()
		if base == "" || pass == "" {
			return no("the worker's server is not ready")
		}
		if viaGateway {
			return no("%s is a worker; its private server can only be entered on its own machine, from agent-net tui there", name)
		}
		plan.Argv = []string{bin, "--server", base, "--session", info.NativeID}
		plan.Env = map[string]string{"OPENCODE_SERVER_PASSWORD": pass}
		plan.Note = fmt.Sprintf("Press ctrl+] to come back here; %s keeps running. What you type here is your own input.", name)
		return plan, nil
	case "codex":
		if w == nil {
			// A thread of the shared app server (codex_peers.go): `codex resume`
			// joins that server, so the owner gets the same thread in a window.
			bin, err := d.harnessBinary("codex")
			if err != nil {
				return no("%v", err)
			}
			plan.Argv = []string{bin, "resume", info.NativeID}
			if home, def := d.cfg.CodexHome, defaultCodexHome(); home != "" && home != def {
				// The thread was found in another Codex home, so say which.
				plan.Argv = append([]string{"env", "CODEX_HOME=" + home}, plan.Argv...)
			}
			plan.Note = fmt.Sprintf("Press ctrl+] to come back here; %s keeps running.", name)
			return plan, nil
		}
		// A worker is in a private app server (worker_codex.go).
		if w.ref == "" {
			return no("the Codex worker's app server is not ready")
		}
		if viaGateway {
			return no("%s is a worker; its private app server can only be entered on its own machine, from agent-net tui there", name)
		}
		tok, err := os.ReadFile(filepath.Join(d.setWorkerDir(w), "token"))
		bin, err2 := d.harnessBinary("codex")
		if err != nil || err2 != nil {
			return no("the connection details of the Codex worker are gone")
		}
		plan.Argv = []string{bin, "resume", info.NativeID, "--remote", "ws://" + w.ref, "--remote-auth-token-env", "AGENT_NET_CODEX_TOKEN"}
		plan.Env = map[string]string{"AGENT_NET_CODEX_TOKEN": strings.TrimSpace(string(tok))}
		plan.Note = fmt.Sprintf("Press ctrl+] to come back here; %s keeps running. The daemon also drives this thread.", name)
		return plan, nil
	case "pi":
		return d.terminalPlan(info, q, plan, name, viaGateway)
	}
	return no("entering %s sessions is not supported", info.Harness)
}

func firstNonNil(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// tmuxPane is the tmux pane a process runs in.
type tmuxPane struct {
	id, session, socket string
}

// terminalPlan takes the owner to the terminal that shows a session with no
// attach command of its own: a herdr pane first (a herdr pane is not a tmux
// pane), then a tmux pane.
func (d *Daemon) terminalPlan(info protocol.SessionInfo, q protocol.EnterRequest, plan protocol.EnterPlan, name string, viaGateway bool) (protocol.EnterPlan, error) {
	if p, ok := d.herdrPlan(info, q, plan, name, viaGateway); ok {
		return p, nil
	}
	return d.tmuxPlan(info, q, plan, name)
}

// tmuxPlan takes the owner to the tmux pane of a session that has no attach
// command of its own, or says that it cannot.
func (d *Daemon) tmuxPlan(info protocol.SessionInfo, q protocol.EnterRequest, plan protocol.EnterPlan, name string) (protocol.EnterPlan, error) {
	pane, ok := findTmuxPane(info.PID)
	if !ok {
		return protocol.EnterPlan{Host: plan.Host, Note: fmt.Sprintf("%s is not in a herdr or tmux pane agent-net can reach (no herdr variables and no herdr server above the process): open its window on %s", name, firstNonEmpty(plan.Host, "its machine"))}, nil
	}
	tmux := []string{"tmux"}
	if pane.socket != "" {
		tmux = append(tmux, "-S", pane.socket)
	}
	out := protocol.EnterPlan{Host: plan.Host}
	out.Argv = append(tmux, "select-window", "-t", pane.id, ";", "select-pane", "-t", pane.id, ";", "attach-session", "-t", pane.session)
	if q.TmuxSocket != "" { // allow an attach from inside tmux
		out.Argv = append([]string{"env", "-u", "TMUX"}, out.Argv...)
	}
	out.Note = fmt.Sprintf("Press ctrl+] to come back here; %s keeps running.", name)
	return out, nil
}

// findTmuxPane finds the pane that holds pid or one of its parents. It asks the
// tmux server named in the process's own $TMUX, else the default server.
func findTmuxPane(pid int) (tmuxPane, bool) {
	if pid <= 1 {
		return tmuxPane{}, false
	}
	bin, err := exec.LookPath("tmux")
	if err != nil {
		return tmuxPane{}, false
	}
	sock, _, _ := strings.Cut(processEnvVar(pid, "TMUX"), ",")
	args := []string{"list-panes", "-a", "-F", "#{pane_pid}|#{pane_id}|#{session_name}"}
	if sock != "" {
		args = append([]string{"-S", sock}, args...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, args...).Output()
	if err != nil {
		return tmuxPane{}, false
	}
	panes := map[int]tmuxPane{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.SplitN(strings.TrimSpace(line), "|", 3)
		if len(f) != 3 {
			continue
		}
		var pp int
		if _, err := fmt.Sscanf(f[0], "%d", &pp); err == nil && pp > 1 {
			panes[pp] = tmuxPane{id: f[1], session: f[2], socket: sock}
		}
	}
	for p, depth := pid, 0; p > 1 && depth < 64; depth++ {
		if pane, ok := panes[p]; ok {
			return pane, true
		}
		parent, err := processParentPID(p)
		if err != nil {
			break
		}
		p = parent
	}
	return tmuxPane{}, false
}
