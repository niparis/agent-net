package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// Enter a session through the harness client. The TUI hands over its own
// terminal or herdr pane to the command, and ctrl+] returns; the daemon never
// opens a tab in the owner's herdr (decision 0154). Pi stays in the herdr
// terminal it runs in because it has no attach client. Remote views use
// herdr's client when it is available on the asking machine.
//
// Same gate as session_text: the owner's CLI or TUI, never an agent session.

// EnterRefProvider is implemented by the Claude adapter: the kind of a session
// ("interactive", "bg", ...) and its background job ID, from its session file.
type EnterRefProvider interface {
	EnterRef(nativeID string) (kind, job string)
}

// enterPlan answers session_enter for a session on any machine.
func (d *Daemon) enterPlan(ctx context.Context, q protocol.EnterRequest) (protocol.EnterPlan, error) {
	if handled, plan, err := d.ctoEnterQuery(ctx, q, false); handled {
		return plan, err
	}
	if d.sessionByID(q.SessionID) != nil {
		return d.localEnter(ctx, q, false)
	}
	// Another machine: it knows its own processes. Our terminal means nothing
	// to it, so the socket is not sent. It is told whether a herdr client is
	// here, because that decides between herdr's remote client and an attach
	// over ssh. The remote client runs in the TUI's own terminal, so it is used
	// whether or not the TUI runs in herdr: it is what carries the clipboard
	// for image paste.
	localHerdr := d.herdrBinary("")
	data, err := d.call(ctx, "query.session_enter", protocol.EnterRequest{SessionID: q.SessionID, HerdrClient: localHerdr != ""})
	if err != nil {
		return protocol.EnterPlan{}, err
	}
	var plan protocol.EnterPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return protocol.EnterPlan{}, err
	}
	// The other machine puts its own name in the plan. Only an older one leaves
	// it empty, so the gateway is asked only then: that round trip is on the
	// path the owner waits on.
	host := plan.Host
	if host == "" {
		host = d.hostOfSession(ctx, q.SessionID)
	}
	name := firstNonEmpty(plan.Name, q.SessionID)
	remote := d.wrapRemoteEnter(host, plan)
	return d.viewPlan("remote:"+q.SessionID, name, false, remote), nil
}

// wrapRemoteEnter turns the other machine's local plan into the command this
// terminal runs: ssh, or herdr's remote client, or a note with the command.
// The other machine's words are shown here, so control codes are removed.
func (d *Daemon) wrapRemoteEnter(host string, plan protocol.EnterPlan) protocol.EnterPlan {
	plan.Note = cleanLine(plan.Note)
	plan.Host, plan.Env = cleanLine(plan.Host), nil // a secret never crosses the gateway; ignore one
	plan.HerdrPane = cleanLine(plan.HerdrPane)
	if !herdrSessionRe.MatchString(plan.HerdrSession) {
		plan.HerdrSession = ""
	}
	if len(plan.Argv) == 0 {
		return protocol.EnterPlan{Host: plan.Host, Note: plan.Note}
	}
	run := remoteCommand(plan.Dir, plan.Argv)
	where := firstNonEmpty(host, "the other machine")
	fallback := fmt.Sprintf("On %s run: %s", where, run)
	if host == "" {
		return protocol.EnterPlan{Host: host, Note: fallback}
	}
	if !d.cfg.EnterSSH {
		return protocol.EnterPlan{Host: host, Note: fallback + "   (enter_ssh is off in node.json)"}
	}
	target := host
	if t := d.cfg.EnterSSHHosts[host]; t != "" {
		target = t
	}
	if !sshTargetOK(target) {
		return protocol.EnterPlan{Host: host, Note: fallback + "   (enter_ssh_hosts gives a bad ssh target)"}
	}
	// The session runs in herdr and this machine has a herdr client: use it.
	// herdr's remote client carries the clipboard, so an image copied here can
	// be pasted into the session there. The session names the herdr session
	// that holds the pane: agent-net's own, or one the owner runs there.
	if localHerdr := d.herdrBinary(""); localHerdr != "" {
		if !herdrIDRe.MatchString(plan.HerdrPane) {
			return protocol.EnterPlan{Host: host, Note: "The remote session could not be opened in herdr. " + plan.Note}
		}
		return protocol.EnterPlan{
			Argv: herdrRemoteArgv(localHerdr, target, plan.HerdrSession), Host: host,
			Note: fmt.Sprintf("Opens herdr on %s; the session is pane %s. Detach with ctrl+b then q (herdr's prefix + q) to come back here; the session keeps running.", host, plan.HerdrPane),
		}
	}
	return protocol.EnterPlan{Argv: []string{"ssh", "-t", "-o", "ConnectTimeout=5", "--", target, run}, Host: host,
		Note: "Through ssh to " + target + ". " + plan.Note, Fallback: fallback}
}

// remoteEnterNote is the result-line sentence after a start with a first
// message on another machine: Enter reaches it, and how.
func (d *Daemon) remoteEnterNote(host string) string {
	where := firstNonEmpty(host, "the other machine")
	if !d.cfg.EnterSSH {
		return "Enter shows the command to run on " + where + " (enter_ssh is off in node.json)."
	}
	target := host
	if t := d.cfg.EnterSSHHosts[host]; t != "" {
		target = t
	}
	if !sshTargetOK(target) {
		return "Enter shows the command to run on " + where + " (enter_ssh_hosts gives a bad ssh target)."
	}
	if d.herdrBinary("") != "" {
		return "Enter opens it through herdr on " + target + "."
	}
	return "Enter opens it through ssh to " + target + "."
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
	// A remote owner with a Herdr client can open a private worker on the
	// worker's own machine, without sending its credentials through the gateway.
	var focused *string
	if viaGateway && q.HerdrClient {
		focused = new(string)
	}
	base, err := d.localEnterBase(ctx, q, viaGateway && !q.HerdrClient, focused)
	if err != nil {
		return base, err
	}
	if base.MoveToBackground {
		if viaGateway {
			return protocol.EnterPlan{Host: base.Host, Note: base.Note + " Open agent-net on " + firstNonEmpty(base.Host, "its machine") + " and press Enter on it."}, nil
		}
		return base, nil
	}
	base = claudePlanEnv(base)
	// The TUI on this machine hands over its terminal, in herdr or not.
	if !viaGateway {
		return d.sessionView(q, base.Name, false, base), nil
	}
	if focused != nil && *focused != "" {
		// localEnterBase already found the pane. Do not walk the same herdr
		// server a second time.
		ls := d.sessionByID(q.SessionID)
		info := d.infoOf(ls)
		name := firstNonEmpty(info.Name, info.NativeID)
		if info.Worker != nil {
			if a, err := d.agentView(ctx, q.SessionID); err == nil && a.Profile.Name != "" {
				name = a.Profile.Name
			}
		}
		base.Name = name
		return base, nil
	}
	ls := d.sessionByID(q.SessionID)
	if ls == nil {
		return base, nil
	}
	info := d.infoOf(ls)
	if info.Harness == "claude" && info.Status == "exited" {
		return base, nil // Do not focus or reopen a stale attach pane either.
	}
	if info.Harness == protocol.OwnerHarness {
		return base, nil
	}
	name := firstNonEmpty(info.Name, info.NativeID)
	if info.Worker != nil {
		if a, err := d.agentView(ctx, q.SessionID); err == nil && a.Profile.Name != "" {
			name = a.Profile.Name
		}
	}
	return d.sessionView(q, name, true, base), nil
}

// claudeAttachPlan is the Enter plan of a Claude background job: plan carries
// the host and folder. Every Claude session uses it, the CTO too.
func claudeAttachPlan(plan protocol.EnterPlan, bin, job, name string) protocol.EnterPlan {
	// Claude Code ignores the mouse variable on the attach client; the
	// user's Claude settings carry it (decision 0128).
	plan.Argv = []string{bin, "attach", job}
	plan = claudePlanEnvAlways(plan)
	plan.Note = fmt.Sprintf("Press ctrl+] to come back here; %s keeps running.", name)
	return plan
}

// sessionView turns a session's own plan into the view the asking TUI runs.
// The TUI on this machine hands over its terminal. A TUI on another machine
// with a herdr client gets a pane in agent-net's herdr here, which it opens
// with herdr --remote (decision 0046).
func (d *Daemon) sessionView(q protocol.EnterRequest, name string, viaGateway bool, base protocol.EnterPlan) protocol.EnterPlan {
	if !viaGateway {
		return d.viewPlan(q.SessionID, name, false, base)
	}
	base.Name = name
	private := base.Env["OPENCODE_SERVER_PASSWORD"] != "" || base.Env["AGENT_NET_CODEX_TOKEN"] != ""
	if !q.HerdrClient || len(base.Argv) == 0 {
		return base
	}
	// The asking machine opens the view with herdr --remote, so this machine
	// hosts it in agent-net's herdr server.
	opened := d.viewPlan(q.SessionID, name, true, base)
	if opened.OpenedHerdr {
		if private {
			return protocol.EnterPlan{Argv: herdrSessionAttach(opened.HerdrSession, opened.HerdrPane), Host: base.Host, Name: name, HerdrPane: opened.HerdrPane, HerdrSession: opened.HerdrSession}
		}
		base.HerdrPane, base.HerdrSession = opened.HerdrPane, opened.HerdrSession
		return base
	}
	if private {
		return protocol.EnterPlan{Host: base.Host, Name: name, Note: "Herdr could not open this private worker on its own machine. " + opened.Note}
	}
	return opened
}

// focused receives the herdr pane of a Pi session for a machine that asks
// with a herdr client. The caller passes nil otherwise.
func (d *Daemon) localEnterBase(ctx context.Context, q protocol.EnterRequest, viaGateway bool, focused *string) (protocol.EnterPlan, error) {
	herdrQuery := q
	if focused != nil {
		herdrQuery.HerdrClient = true
	}
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
		if info.Status == "exited" {
			return no("%s exited; on %s run: %s", name, plan.Host, remoteCommand(info.Cwd, []string{"claude", "--resume", info.NativeID}))
		}
		kind, job := "", ""
		if p, ok := d.adapters["claude"].(EnterRefProvider); ok {
			kind, job = p.EnterRef(info.NativeID)
		}
		if w != nil {
			// Claude workers always run as background jobs.
			d.wmu.Lock()
			kind, job = "bg", firstNonEmpty(w.ref, job)
			d.wmu.Unlock()
		}
		if kind != "bg" {
			// A session the owner started in a terminal has no background job
			// to attach to. A second `claude --resume` client would fight the
			// first, so the front end offers to move it to the background.
			if info.NativeID == "" {
				return no("cannot move %s to the background: no session id", name)
			}
			plan.MoveToBackground = true
			plan.Note = fmt.Sprintf("%s runs in a terminal outside agent-net. Move it to the background to open it here.", name)
			return plan, nil
		}
		id := job
		if id == "" && len(info.NativeID) >= 8 {
			id = info.NativeID[:8] // the job ID is the first 8 characters of the session ID
		}
		bin, err := d.harnessBinary("claude")
		if err != nil || id == "" {
			return no("cannot attach to %s: %v", name, firstNonNil(err, errors.New("no background id")))
		}
		return claudeAttachPlan(plan, bin, id, name), nil
	case "opencode":
		reopen := func() error {
			if _, err := d.call(ctx, "session.remove", protocol.SessionRemove{SessionID: q.SessionID, Reopen: true}); err != nil {
				return err
			}
			if ls := d.sessionByID(q.SessionID); ls != nil {
				d.pushSession(ls)
			}
			return nil
		}
		bin, err := d.harnessBinary("opencode")
		if err != nil {
			return no("%v", err)
		}
		if w == nil {
			if info.NativeID == "" {
				return no("%s has no OpenCode session ID to open it with", name)
			}
			// The service owns every owner OpenCode session. Without it there is
			// nothing to attach to, and a bare `opencode --session` would host
			// the session in a terminal of its own.
			service, serviceErr := d.ownerOpenCodeService()
			if serviceErr != nil {
				return no("the OpenCode service is not running on %s: start it with `opencode service start`, then press Enter again", plan.Host)
			}
			if viaGateway && service.Password != "" {
				return no("%s uses a private OpenCode service and can only be entered on its own machine", name)
			}
			if err := reopen(); err != nil {
				return no("could not reopen session: %v", err)
			}
			plan.Argv = []string{bin, "--server", service.URL, "--session", info.NativeID}
			if service.Password != "" {
				plan.Env = map[string]string{"OPENCODE_SERVER_PASSWORD": service.Password}
			}
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
	case "agy":
		if info.NativeID == "" {
			return no("%s has no Antigravity conversation ID", name)
		}
		bin, err := d.harnessBinary("agy")
		if err != nil {
			return no("%v", err)
		}
		plan.Argv = []string{bin, "--conversation", info.NativeID}
		plan.Note = fmt.Sprintf("Press ctrl+] to come back here; %s keeps running.", name)
		return plan, nil
	case "codex":
		if w == nil {
			// A thread of the shared app server (codex_peers.go): `codex resume`
			// joins that server, so the owner gets the same thread in a window.
			bin, err := d.harnessBinary("codex")
			if err != nil {
				return no("%v", err)
			}
			plan.Argv = []string{bin, "resume", "--remote", "unix://", info.NativeID}
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
		if w != nil {
			return no("Pi workers run in RPC mode without a terminal; read this session with agent-net show %s", cleanLine(q.SessionID))
		}
		p, err := d.terminalPlan(info, herdrQuery, plan, name)
		if focused != nil && p.HerdrPane != "" {
			*focused = p.HerdrPane
		}
		return p, err
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

// terminalPlan finds the herdr pane holding a session without an attach
// command, which is how a Pi session is viewed.
func (d *Daemon) terminalPlan(info protocol.SessionInfo, q protocol.EnterRequest, plan protocol.EnterPlan, name string) (protocol.EnterPlan, error) {
	if p, ok := d.herdrPlan(info, q, plan, name); ok {
		return p, nil
	}
	return protocol.EnterPlan{Host: plan.Host, Note: fmt.Sprintf("%s is not in a herdr pane agent-net can reach (no herdr variables and no herdr server above the process): open its window on %s", name, firstNonEmpty(plan.Host, "its machine"))}, nil
}
