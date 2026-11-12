package daemon

import (
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
// never opens anything in the owner's herdr (decision 0149).
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
func (d *Daemon) openInHerdr(key, name string, plan protocol.EnterPlan) protocol.EnterPlan {
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
	if old, ok := d.herdrOpened[key]; key != "" && ok {
		if panes, err := d.herdrPanes(old.bin, old.socket); err == nil {
			for _, pane := range panes {
				if pane.PaneID == old.pane {
					// Immediately after pane run, the shell may not have started
					// its child yet. Later, an idle shell means the client exited.
					if active, known := d.herdrPaneActive(old.bin, old.socket, old.pane); known && !active && time.Since(old.started) > time.Second {
						break
					}
					if _, err := d.herdrRun(old.bin, old.socket, "tab", "focus", old.tab); err == nil {
						_, _ = d.herdrRun(old.bin, old.socket, "agent", "focus", old.pane)
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
	command := ". " + shellQuote(file) + "; rm -f " + shellQuote(file) + "; exec " + strings.Join(quoted, " ")
	// The pane may use fish or another non-POSIX login shell. Keep the
	// assignment and argv quoting inside a POSIX shell.
	command = "sh -c " + shellQuote(command)
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
