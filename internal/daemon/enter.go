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

// Enter a session (decisions 0032 and 0043). The daemon opens or focuses a
// Herdr pane when available. Otherwise it returns the terminal handover plan;
// the TUI builds no command itself.
//
//	Claude Code background session   claude attach <id>
//	Claude Code, Pi in a terminal    its herdr pane, else
//	                                 a note to open its existing window
//	OpenCode                         its herdr pane, else opencode --session <id>
//	                                 (in the folder)
//	Codex (not a worker)             codex resume <thread>, on the shared app server
//	a worker of OpenCode or Codex    their private server, with its password
//	                                 kept on the machine that has it
//
// A session on another machine is planned by that machine (the gateway relays
// the question), and wrapped in herdr's remote client or `ssh -t` unless enter_ssh is false in
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
	return d.openForCaller("remote:"+q.SessionID, name, q.HerdrSocket, q.HerdrPaneID, remote), nil
}

// wrapRemoteEnter turns the other machine's local plan into the command this
// terminal runs: ssh, or herdr's remote client, or a note with the command.
// The other machine's words are shown here, so control codes are removed.
func (d *Daemon) wrapRemoteEnter(host string, plan protocol.EnterPlan) protocol.EnterPlan {
	plan.Note = cleanLine(plan.Note)
	plan.Host, plan.Env = cleanLine(plan.Host), nil // a secret never crosses the gateway; ignore one
	plan.HerdrPane = cleanLine(plan.HerdrPane)
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
	// be pasted into the session there. The other machine focused the pane
	// before answering.
	if localHerdr := d.herdrBinary(""); localHerdr != "" && herdrIDRe.MatchString(plan.HerdrPane) {
		return protocol.EnterPlan{
			Argv: []string{localHerdr, "--remote", target}, Host: host,
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
	insideHerdr := !viaGateway && callerInHerdr(q.HerdrSocket, q.HerdrPaneID)
	var focused *string
	if insideHerdr || q.HerdrClient {
		focused = new(string)
	}
	base, err := d.localEnterBase(ctx, q, viaGateway && !q.HerdrClient, focused)
	if err != nil {
		return base, err
	}
	// A TUI outside herdr cannot see a herdr pane: hand over its terminal.
	if !viaGateway && !insideHerdr {
		return base, nil
	}
	if focused != nil && *focused != "" {
		// localEnterBase already found and focused the pane. Do not walk the
		// same herdr server a second time.
		ls := d.sessionByID(q.SessionID)
		info := d.infoOf(ls)
		name := firstNonEmpty(info.Name, info.NativeID)
		if info.Worker != nil {
			if a, err := d.agentView(ctx, q.SessionID); err == nil && a.Profile.Name != "" {
				name = a.Profile.Name
			}
		}
		base.Name = name
		if viaGateway {
			return base, nil
		}
		return openedHerdrPlan(base.Host, name, *focused), nil
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
	base.Name = name
	if info.Harness == "pi" && d.workerBySession(q.SessionID) != nil {
		// An RPC worker has no terminal, even if it inherited Herdr variables
		// from the daemon. Do not mistake the daemon's pane for the worker.
		if viaGateway {
			return base, nil
		}
		return d.openInHerdr(q.SessionID, name, q.HerdrSocket, q.HerdrPaneID, base), nil
	}
	private := viaGateway && (base.Env["OPENCODE_SERVER_PASSWORD"] != "" || base.Env["AGENT_NET_CODEX_TOKEN"] != "")
	remotePane := func(pane string) protocol.EnterPlan {
		if private {
			return protocol.EnterPlan{Argv: []string{"herdr", "agent", "attach", pane}, Host: base.Host, Name: name, HerdrPane: pane}
		}
		base.HerdrPane = pane
		return base
	}
	// A gateway caller without a Herdr client needs the old SSH attach plan.
	if viaGateway && !q.HerdrClient {
		return base, nil
	}
	// A private worker's connection details are never sent via the gateway.
	if viaGateway && len(base.Argv) == 0 {
		return base, nil
	}
	probe := q
	probe.HerdrClient = true
	if p, found := d.herdrPlan(info, probe, protocol.EnterPlan{Host: base.Host}, name, viaGateway); found {
		if p.HerdrPane != "" {
			p.Name = name
			if viaGateway {
				return p, nil
			}
			return openedHerdrPlan(base.Host, name, p.HerdrPane), nil
		}
		if strings.HasPrefix(p.Note, "cannot focus ") {
			return p, nil
		}
	}
	p, found := d.focusHerdrSession(q.HerdrSocket, info.Harness, info.NativeID, base.Host, name)
	if !found && info.Harness == "claude" && len(base.Argv) == 3 && base.Argv[1] == "attach" {
		p, found = d.focusHerdrAttachClient(q.HerdrSocket, base.Argv[2], base.Host, name)
	}
	if found {
		if viaGateway && p.OpenedHerdr {
			return remotePane(p.HerdrPane), nil
		}
		return p, nil
	}
	opened := d.openInHerdr(q.SessionID, name, q.HerdrSocket, q.HerdrPaneID, base)
	if viaGateway && opened.OpenedHerdr {
		// Keep the attach command for old clients and the remote wrapper; the
		// pane ID lets the asking machine use Herdr's remote client.
		return remotePane(opened.HerdrPane), nil
	}
	if private {
		return protocol.EnterPlan{Host: base.Host, Name: name, Note: "Herdr could not open this private worker on its own machine. " + opened.Note}, nil
	}
	return opened, nil
}

// focused receives the pane when this machine already has a herdr client that
// can show it. The caller passes nil when the asking terminal cannot see a
// herdr pane, so no focus command is sent.
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
			p, err := d.terminalPlan(info, herdrQuery, plan, name, viaGateway)
			if focused != nil && p.HerdrPane != "" {
				*focused = p.HerdrPane
			}
			return p, err
		}
		id := job
		if id == "" && len(info.NativeID) >= 8 {
			id = info.NativeID[:8] // the job ID is the first 8 characters of the session ID
		}
		bin, err := d.harnessBinary("claude")
		if err != nil || id == "" {
			return no("cannot attach to %s: %v", name, firstNonNil(err, errors.New("no background id")))
		}
		// Claude Code ignores the mouse variable on the attach client; the
		// session's launch settings carry it (decision 0128).
		plan.Argv = []string{bin, "attach", id}
		plan.Note = fmt.Sprintf("Press ctrl+] to come back here; %s keeps running.", name)
		return plan, nil
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
		var herdrNote string
		if w == nil { // a TUI the owner started in a herdr pane is the screen to show
			if p, ok := d.herdrPlan(info, herdrQuery, plan, name, viaGateway); ok {
				if focused != nil && p.HerdrPane != "" {
					*focused = p.HerdrPane
				}
				if len(p.Argv) > 0 {
					if err := reopen(); err != nil {
						return no("could not reopen session: %v", err)
					}
					return p, nil
				}
				// The pane has closed, or herdr cannot attach it. The session
				// itself still lives in the OpenCode service, so open it there.
				herdrNote = p.Note
			}
		}
		bin, err := d.harnessBinary("opencode")
		if err != nil {
			if herdrNote != "" {
				return no("%s It cannot be opened from the OpenCode service either: %v", herdrNote, err)
			}
			return no("%v", err)
		}
		if w == nil {
			if info.NativeID == "" {
				if herdrNote != "" {
					return no("%s It also has no OpenCode session ID to open it with", herdrNote)
				}
				return no("%s has no OpenCode session ID to open it with", name)
			}
			if err := reopen(); err != nil {
				return no("could not reopen session: %v", err)
			}
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
		if w != nil {
			return no("Pi workers run in RPC mode without a terminal; read this session with agent-net show %s", cleanLine(q.SessionID))
		}
		p, err := d.terminalPlan(info, herdrQuery, plan, name, viaGateway)
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

// terminalPlan finds the herdr pane holding a session without an attach command.
func (d *Daemon) terminalPlan(info protocol.SessionInfo, q protocol.EnterRequest, plan protocol.EnterPlan, name string, viaGateway bool) (protocol.EnterPlan, error) {
	if p, ok := d.herdrPlan(info, q, plan, name, viaGateway); ok {
		return p, nil
	}
	return protocol.EnterPlan{Host: plan.Host, Note: fmt.Sprintf("%s is not in a herdr pane agent-net can reach (no herdr variables and no herdr server above the process): open its window on %s", name, firstNonEmpty(plan.Host, "its machine"))}, nil
}
