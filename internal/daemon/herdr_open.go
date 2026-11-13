package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// herdrOpenedPane tracks the UI client agent-net opened, whose process need
// not be the background session process that discovery reports.
type herdrOpenedPane struct {
	bin, socket, pane, tab string
	started                time.Time
}

const herdrInstallNote = "Herdr was not found; install: https://herdr.dev/tui/docs/install/."

var shellEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// herdrEnvironment writes a one-use shell fragment instead of putting private
// worker credentials in Herdr's command arguments or the pane's input text.
func herdrEnvironment(env map[string]string) (string, error) {
	file, err := os.CreateTemp("", "agent-net-herdr-env-*")
	if err != nil {
		return "", err
	}
	path := file.Name()
	var body strings.Builder
	body.WriteString("unset CLAUDE_CODE_DISABLE_MOUSE\n")
	keys := make([]string, 0, len(env))
	for key := range env {
		if key != "CLAUDE_CODE_DISABLE_MOUSE" {
			if !shellEnvName.MatchString(key) {
				file.Close()
				os.Remove(path)
				return "", fmt.Errorf("invalid environment name")
			}
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		body.WriteString("export " + key + "=" + shellQuote(env[key]) + "\n")
	}
	_, err = file.WriteString(body.String())
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// viewPlan returns the plan the asking terminal runs: the TUI hands over its
// own terminal or herdr pane, and ctrl+] returns. Only when host is set does
// the daemon host the command itself, in agent-net's herdr server: a Pi
// session, which needs a terminal of its own, and a view or sign-in asked
// from another machine, which that machine opens with herdr --remote. It
// never opens anything in the owner's herdr (decision 0154).
func (d *Daemon) viewPlan(key, name string, host bool, plan protocol.EnterPlan) protocol.EnterPlan {
	if !host {
		return claudePlanEnv(plan)
	}
	return d.openInHerdr(key, name, plan)
}

// openInHerdr hosts a plan in a tab of agent-net's herdr server. It
// serializes the lookup and creation so concurrent calls cannot create
// duplicate tabs. When herdr is unavailable the original plan remains the
// fallback.
func (d *Daemon) openInHerdr(key, name string, plan protocol.EnterPlan) (opened protocol.EnterPlan) {
	bin, lookupErr := d.lookupHerdrBinary()
	if lookupErr != nil {
		plan.Note = strings.TrimSpace(plan.Note + " " + lookupErr.Error())
		return plan
	}
	if len(plan.Argv) == 0 {
		return plan
	}
	plan = claudePlanEnv(plan)
	d.herdrOpenMu.Lock()
	defer d.herdrOpenMu.Unlock()
	// A server that runs without agent-net's config is moved under it here,
	// before the owner attaches (herdr_handoff.go).
	sidebarNote := ""
	defer func() { opened.Note = strings.TrimSpace(sidebarNote + " " + opened.Note) }()
	if old, ok := d.herdrOpened[key]; key != "" && ok {
		sidebarNote = d.adoptAgentNetHerdr(old.bin, old.socket)
		if panes, err := d.herdrPanes(old.bin, old.socket); err == nil {
			for _, pane := range panes {
				if pane.PaneID == old.pane {
					// Immediately after pane run, the shell may not have started
					// its child yet. Later, an idle shell means the client exited.
					if active, known := d.herdrPaneActive(old.bin, old.socket, old.pane); known && !active && time.Since(old.started) > time.Second {
						break
					}
					if d.focusHerdrView(old.bin, old.socket, old.tab, old.pane) {
						return herdrOpenNotice(hostedHerdrPlan(plan.Host, name, old.pane), plan)
					}
				}
			}
		}
		delete(d.herdrOpened, key)
	}
	socket, err := d.agentNetHerdr(bin)
	if err != nil {
		plan.Note = strings.TrimSpace(plan.Note + " " + fmt.Sprintf("herdr at %s failed: %v", bin, err))
		return plan
	}
	if sidebarNote == "" {
		sidebarNote = d.adoptAgentNetHerdr(bin, socket)
	}
	// The memory above is empty after a daemon restart. Herdr still shows the
	// tab, so ask it before creating a second one.
	if key != "" {
		if pane, tab, ok := d.findHerdrView(bin, socket, herdrViewID(key)); ok && d.focusHerdrView(bin, socket, tab, pane) {
			if d.herdrOpened == nil {
				d.herdrOpened = make(map[string]herdrOpenedPane)
			}
			d.herdrOpened[key] = herdrOpenedPane{bin: bin, socket: socket, pane: pane, tab: tab}
			return herdrOpenNotice(hostedHerdrPlan(plan.Host, name, pane), plan)
		}
	}
	file, err := herdrEnvironment(plan.Env)
	if err != nil {
		return protocol.EnterPlan{Host: plan.Host, Note: "Could not prepare herdr environment; check the temporary directory."}
	}
	defer func() {
		// The shell removes the file after reading it. The delayed removal also
		// covers a shell that never reaches the command.
		time.AfterFunc(time.Minute, func() { _ = os.Remove(file) })
	}()
	// A new server has no workspace, and herdr makes a tab only inside one.
	create := "tab"
	if out, err := d.herdrRun(bin, socket, "workspace", "list"); err == nil {
		var ws struct {
			Result struct {
				Workspaces []json.RawMessage `json:"workspaces"`
			} `json:"result"`
		}
		if json.Unmarshal(out, &ws) == nil && len(ws.Result.Workspaces) == 0 {
			create = "workspace"
		}
	}
	args := []string{create, "create", "--label", cleanLine(name), "--focus"}
	if plan.Dir != "" {
		args = append(args, "--cwd", plan.Dir)
	}
	out, err := d.herdrRun(bin, socket, args...)
	if err != nil {
		_ = os.Remove(file)
		return protocol.EnterPlan{Host: plan.Host, Note: fmt.Sprintf("herdr at %s failed: could not create a tab: %v", bin, err)}
	}
	var created struct {
		Result struct {
			Tab struct {
				ID string `json:"tab_id"`
			} `json:"tab"`
			Pane struct {
				ID string `json:"pane_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	decodeErr := json.Unmarshal(out, &created)
	if decodeErr != nil || !herdrIDRe.MatchString(created.Result.Pane.ID) || !herdrIDRe.MatchString(created.Result.Tab.ID) {
		_ = os.Remove(file)
		return protocol.EnterPlan{Host: plan.Host, Note: "Herdr created a tab but did not return its pane; check herdr before retrying."}
	}
	quoted := make([]string, len(plan.Argv))
	for i, arg := range plan.Argv {
		quoted[i] = shellQuote(arg)
	}
	// The viewer is not exec'd: the shell stays to close the pane when it
	// exits, so no idle login shell is left in the tab. The INT handler keeps
	// Ctrl-C from killing the shell before it closes (a handler, not an
	// ignore, so the viewer still gets the default action).
	closeCmd := shellQuote(bin) + " pane close " + shellQuote(created.Result.Pane.ID) + " >/dev/null 2>&1"
	if socket != "" {
		closeCmd = "HERDR_SOCKET_PATH=" + shellQuote(socket) + " " + closeCmd
	}
	command := "trap : INT; . " + shellQuote(file) + "; rm -f " + shellQuote(file) + "; " + strings.Join(quoted, " ") + "; " + closeCmd
	// The pane may use fish or another non-POSIX login shell. Keep the
	// assignment and argv quoting inside a POSIX shell. The trailing words
	// become $0 and $1 of that shell; they mark the view for findHerdrView.
	command = "sh -c " + shellQuote(command)
	if key != "" {
		command += " " + herdrViewMarker + " " + herdrViewID(key)
	}
	if _, err := d.herdrRun(bin, socket, "pane", "run", created.Result.Pane.ID, command); err != nil {
		_ = os.Remove(file)
		return protocol.EnterPlan{Host: plan.Host, Note: fmt.Sprintf("herdr at %s failed: could not run the command: %v", bin, err)}
	}
	if key != "" {
		if d.herdrOpened == nil {
			d.herdrOpened = make(map[string]herdrOpenedPane)
		}
		d.herdrOpened[key] = herdrOpenedPane{bin: bin, socket: socket, pane: created.Result.Pane.ID, tab: created.Result.Tab.ID, started: time.Now()}
	}
	return herdrOpenNotice(hostedHerdrPlan(plan.Host, name, created.Result.Pane.ID), plan)
}

// hostedSocket is the herdr server of the pane openInHerdr made for key.
func (d *Daemon) hostedSocket(key string) string {
	d.herdrOpenMu.Lock()
	defer d.herdrOpenMu.Unlock()
	return d.herdrOpened[key].socket
}

const herdrViewMarker = "agent-net-view"

// herdrViewID names a view in the argv of the shell that runs it. It is a
// hash because keys can hold NUL bytes and whole argv lists.
func herdrViewID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

func (d *Daemon) focusHerdrView(bin, socket, tab, pane string) bool {
	if _, err := d.herdrRun(bin, socket, "tab", "focus", tab); err != nil {
		return false
	}
	_, _ = d.herdrRun(bin, socket, "agent", "focus", pane)
	return true
}

// findHerdrView finds the pane whose shell was started with the view marker
// of id, by reading the argv of each pane's foreground processes.
func (d *Daemon) findHerdrView(bin, socket, id string) (pane, tab string, ok bool) {
	panes, err := d.herdrPanes(bin, socket)
	if err != nil {
		return "", "", false
	}
	for _, p := range panes {
		out, err := d.herdrRun(bin, socket, "pane", "process-info", "--pane", p.PaneID)
		if err != nil {
			continue
		}
		var r struct {
			Result struct {
				Info struct {
					Processes []struct {
						Argv []string `json:"argv"`
					} `json:"foreground_processes"`
				} `json:"process_info"`
			} `json:"result"`
		}
		if json.Unmarshal(out, &r) != nil {
			continue
		}
		for _, proc := range r.Result.Info.Processes {
			if n := len(proc.Argv); n >= 2 && proc.Argv[n-2] == herdrViewMarker && proc.Argv[n-1] == id {
				return p.PaneID, p.TabID, true
			}
		}
	}
	return "", "", false
}

func herdrOpenNotice(opened, previous protocol.EnterPlan) protocol.EnterPlan {
	if strings.HasPrefix(previous.Note, claudeCTORestartNotice) {
		opened.Note += " " + previous.Note
	}
	return opened
}

// herdrPaneActive distinguishes an exited client from its still-open shell.
// Older or inaccessible servers may not supply process information.
func (d *Daemon) herdrPaneActive(bin, socket, pane string) (active, known bool) {
	out, err := d.herdrRun(bin, socket, "pane", "process-info", "--pane", pane)
	if err != nil {
		return false, false
	}
	var result struct {
		Result struct {
			Info struct {
				Shell     int `json:"shell_pid"`
				Processes []struct {
					PID int `json:"pid"`
				} `json:"foreground_processes"`
			} `json:"process_info"`
		} `json:"result"`
	}
	if json.Unmarshal(out, &result) != nil || result.Result.Info.Shell <= 1 {
		return false, false
	}
	for _, proc := range result.Result.Info.Processes {
		if proc.PID > 1 && proc.PID != result.Result.Info.Shell {
			return true, true
		}
	}
	return false, true
}

// hostedHerdrPlan says that the daemon runs the command in a pane of
// agent-net's herdr server. Nothing is open where the owner looks: Enter
// opens it, and another machine opens it with herdr --remote.
func hostedHerdrPlan(host, name, pane string) protocol.EnterPlan {
	return protocol.EnterPlan{Host: host, Note: "Started " + cleanLine(name) + " in agent-net's herdr. Enter opens it.", HerdrPane: pane, HerdrSession: agentNetHerdrSession, OpenedHerdr: true}
}

func (d *Daemon) notifySessionStart(r protocol.StartedSession) {
	if r.EnterPlan != nil && r.EnterPlan.OpenedHerdr {
		d.broadcastCTOUI(protocol.UIRequest{Text: r.EnterPlan.Note, HerdrOpened: true})
	} else if r.EnterNote != "" {
		d.broadcastCTOUI(protocol.UIRequest{Text: r.EnterNote, DaemonNotice: true})
	} else if r.EnterPlan != nil && r.EnterPlan.Note != "" {
		text := r.EnterPlan.Note
		if len(r.EnterPlan.Argv) != 0 {
			text = "The session needs a terminal to start. " + r.EnterPlan.Note
		}
		d.broadcastCTOUI(protocol.UIRequest{Text: text, DaemonNotice: true})
	}
}

// notifySessionStartFailed tells the owner's TUI that a start answered as
// starting did not start, and why. It uses the channel of a successful start.
func (d *Daemon) notifySessionStartFailed(r protocol.StartedSession) {
	name := cleanLine(r.Project)
	if host := cleanLine(r.Hostname); host != "" {
		name += " on " + host
	}
	reason := firstNonEmpty(cleanLine(r.Error), "no reason was reported")
	d.broadcastCTOUI(protocol.UIRequest{Text: "Session not started: " + name + ". " + reason, DaemonNotice: true})
}
