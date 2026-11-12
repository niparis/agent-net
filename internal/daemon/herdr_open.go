package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// herdrServer selects the requested server, or starts the default headless
// server. An explicitly requested socket must never be silently replaced.
func (d *Daemon) herdrServer(bin, socket string) error {
	if _, err := d.herdrPanes(bin, socket); err == nil {
		return nil
	} else if socket != "" {
		return err
	}
	cmd := exec.Command(bin, "server")
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HERDR_SESSION=") && !strings.HasPrefix(kv, "HERDR_SOCKET_PATH=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			_, err := d.herdrPanes(bin, "")
			return err
		case <-deadline.C:
			return fmt.Errorf("server did not become ready within 3 seconds")
		case <-ticker.C:
			if _, err := d.herdrPanes(bin, ""); err == nil {
				return nil
			}
		}
	}
}

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

// callerInHerdr says whether the asking TUI runs inside herdr. Only then can
// it see a herdr pane; outside herdr a pane would open where the owner cannot
// see it, so the terminal handover plan is used instead.
func callerInHerdr(socket, pane string) bool {
	return filepath.IsAbs(socket) && herdrIDRe.MatchString(pane)
}

// viewPlan chooses terminal handover or a full herdr tab. Host is set when
// the command must run in a headless herdr server for a remote viewer or Pi.
func (d *Daemon) viewPlan(key, name, socket, pane string, host bool, plan protocol.EnterPlan) protocol.EnterPlan {
	if !host && !callerInHerdr(socket, pane) {
		return claudePlanEnv(plan)
	}
	return d.openInHerdr(key, name, socket, plan)
}

// openInHerdr opens a plan in a full tab. It serializes the lookup and
// creation so concurrent Enter calls cannot create duplicate clients. When the
// server is unavailable the original plan remains the fallback.
func (d *Daemon) openInHerdr(key, name, socket string, plan protocol.EnterPlan) protocol.EnterPlan {
	bin, lookupErr := d.lookupHerdrBinary()
	if lookupErr != nil {
		plan.Note = strings.TrimSpace(plan.Note + " " + lookupErr.Error())
		return plan
	}
	if len(plan.Argv) == 0 {
		return plan
	}
	plan = claudePlanEnv(plan)
	if !filepath.IsAbs(socket) {
		socket = ""
	}
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
						return herdrOpenNotice(openedHerdrPlan(plan.Host, name, old.pane), plan)
					}
				}
			}
		}
		delete(d.herdrOpened, key)
	}
	if err := d.herdrServer(bin, socket); err != nil {
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
	args := []string{"tab", "create", "--label", cleanLine(name), "--focus"}
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
	return herdrOpenNotice(openedHerdrPlan(plan.Host, name, created.Result.Pane.ID), plan)
}

func herdrOpenNotice(opened, previous protocol.EnterPlan) protocol.EnterPlan {
	if strings.HasPrefix(previous.Note, claudeCTORestartNotice) {
		opened.Note += ". " + previous.Note
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

func openedHerdrPlan(host, name, pane string) protocol.EnterPlan {
	return protocol.EnterPlan{Host: host, Note: "opened " + cleanLine(name) + " in herdr", HerdrPane: pane, OpenedHerdr: true}
}

// Only the approving TUI gets a command to run. Broadcasting it would start
// the same session once per open TUI, and a CLI has no terminal push handler.
func (d *Daemon) notifyApprovedSessionStart(ctx context.Context, r protocol.StartedSession) bool {
	if r.Harness == "codex" && r.EnterPlan != nil && !r.EnterPlan.OpenedHerdr && len(r.EnterPlan.Argv) != 0 {
		loc, _ := ctx.Value(proposalHerdrLocationKey{}).(proposalHerdrLocation)
		if c := loc.approver; c != nil {
			name := cleanLine(r.Project)
			if host := cleanLine(r.Hostname); host != "" {
				name += " (" + host + ")"
			}
			q := protocol.UIRequest{StartPlan: r.EnterPlan, StartName: name, StartProposalID: proposalIDFromContext(ctx)}
			if c.writeJSON(map[string]any{"method": "cto_ui", "params": q}) == nil {
				return true
			}
		}
	}
	d.notifySessionStart(r)
	return false
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
