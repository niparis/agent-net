package daemon

import (
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

const herdrInstallNote = "Herdr was not found or failed; install: https://herdr.dev/tui/docs/install/."

var shellEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// herdrServer selects the requested server, or starts the default headless
// server. An explicitly requested socket must never be silently replaced.
func herdrServer(bin, socket string) bool {
	if _, err := herdrPanes(bin, socket); err == nil {
		return true
	}
	if socket != "" {
		return false
	}
	cmd := exec.Command(bin, "server")
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HERDR_SESSION=") && !strings.HasPrefix(kv, "HERDR_SOCKET_PATH=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	if err := cmd.Start(); err != nil {
		return false
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
			_, err := herdrPanes(bin, "")
			return err == nil
		case <-deadline.C:
			return false
		case <-ticker.C:
			if _, err := herdrPanes(bin, ""); err == nil {
				return true
			}
		}
	}
}

func herdrFallback(plan protocol.EnterPlan) protocol.EnterPlan {
	if plan.Note == "" {
		plan.Note = herdrInstallNote
	} else {
		plan.Note += " " + herdrInstallNote
	}
	return plan
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

// openInHerdr opens a plan in a pane or tab. It serializes the lookup and
// creation so concurrent Enter calls cannot create duplicate clients. When the
// server is unavailable the original plan remains the fallback.
func (d *Daemon) openInHerdr(key, name, socket, callerPane string, plan protocol.EnterPlan) protocol.EnterPlan {
	bin := d.herdrBinary("")
	if bin == "" {
		return herdrFallback(plan)
	}
	if len(plan.Argv) == 0 {
		return plan
	}
	if !filepath.IsAbs(socket) {
		socket = ""
	}
	if socket == "" || !herdrIDRe.MatchString(callerPane) {
		callerPane = ""
	}
	d.herdrOpenMu.Lock()
	defer d.herdrOpenMu.Unlock()
	if old, ok := d.herdrOpened[key]; key != "" && ok {
		if panes, err := herdrPanes(old.bin, old.socket); err == nil {
			for _, pane := range panes {
				if pane.PaneID == old.pane {
					// Immediately after pane run, the shell may not have started
					// its child yet. Later, an idle shell means the client exited.
					if active, known := herdrPaneActive(old.bin, old.socket, old.pane); known && !active && time.Since(old.started) > time.Second {
						break
					}
					if _, err := herdrRun(old.bin, old.socket, "tab", "focus", old.tab); err == nil {
						_, _ = herdrRun(old.bin, old.socket, "agent", "focus", old.pane)
						return herdrOpenNotice(openedHerdrPlan(plan.Host, name, old.pane), plan)
					}
				}
			}
		}
		delete(d.herdrOpened, key)
	}
	if !herdrServer(bin, socket) {
		return herdrFallback(plan)
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
	if callerPane != "" {
		args = []string{"pane", "split", "--pane", callerPane, "--direction", "right", "--focus"}
	}
	if plan.Dir != "" {
		args = append(args, "--cwd", plan.Dir)
	}
	out, err := herdrRun(bin, socket, args...)
	if err != nil {
		_ = os.Remove(file)
		return protocol.EnterPlan{Host: plan.Host, Note: "Herdr could not create a pane; check its server before retrying."}
	}
	var created struct {
		Result struct {
			Tab struct {
				ID string `json:"tab_id"`
			} `json:"tab"`
			Pane struct {
				ID string `json:"pane_id"`
			} `json:"root_pane"`
			Split struct {
				ID  string `json:"pane_id"`
				Tab string `json:"tab_id"`
			} `json:"pane"`
		} `json:"result"`
	}
	decodeErr := json.Unmarshal(out, &created)
	if callerPane != "" {
		created.Result.Pane.ID, created.Result.Tab.ID = created.Result.Split.ID, created.Result.Split.Tab
	}
	if decodeErr != nil || !herdrIDRe.MatchString(created.Result.Pane.ID) || !herdrIDRe.MatchString(created.Result.Tab.ID) {
		_ = os.Remove(file)
		return protocol.EnterPlan{Host: plan.Host, Note: "Herdr created a tab but did not return its pane; check herdr before retrying."}
	}
	if callerPane != "" {
		if _, err := herdrRun(bin, socket, "pane", "rename", created.Result.Pane.ID, cleanLine(name)); err != nil {
			_ = os.Remove(file)
			return protocol.EnterPlan{Host: plan.Host, Note: "Herdr created a pane but could not name it; check herdr before retrying."}
		}
	}
	quoted := make([]string, len(plan.Argv))
	for i, arg := range plan.Argv {
		quoted[i] = shellQuote(arg)
	}
	command := ". " + shellQuote(file) + "; rm -f " + shellQuote(file) + "; exec " + strings.Join(quoted, " ")
	// The pane may use fish or another non-POSIX login shell. Keep the
	// assignment and argv quoting inside a POSIX shell.
	command = "sh -c " + shellQuote(command)
	if _, err := herdrRun(bin, socket, "pane", "run", created.Result.Pane.ID, command); err != nil {
		_ = os.Remove(file)
		return protocol.EnterPlan{Host: plan.Host, Note: "Herdr created a tab but could not run the command; check herdr before retrying."}
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
func herdrPaneActive(bin, socket, pane string) (active, known bool) {
	out, err := herdrRun(bin, socket, "pane", "process-info", "--pane", pane)
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

func (d *Daemon) notifySessionStart(r protocol.StartedSession) {
	if r.EnterPlan != nil && r.EnterPlan.OpenedHerdr {
		d.broadcastCTOUI(protocol.UIRequest{Text: r.EnterPlan.Note, HerdrOpened: true})
	} else if r.EnterNote != "" {
		d.broadcastCTOUI(protocol.UIRequest{Text: r.EnterNote, DaemonNotice: true})
	} else if r.EnterPlan != nil && r.EnterPlan.Note != "" {
		text := r.EnterPlan.Note
		if len(r.EnterPlan.Argv) != 0 {
			text = "The session needs a terminal to start. " + herdrInstallNote
		}
		d.broadcastCTOUI(protocol.UIRequest{Text: text, DaemonNotice: true})
	}
}

// A background session's attach client is a different process from the
// background job. Herdr can report its native session ID, including after an
// agent-net daemon restart, so check that before creating another client.
func (d *Daemon) focusHerdrSession(socket, harness, native, host, name string) (protocol.EnterPlan, bool) {
	if native == "" {
		return protocol.EnterPlan{}, false
	}
	bin := d.herdrBinary("")
	if bin == "" {
		return protocol.EnterPlan{}, false
	}
	if !filepath.IsAbs(socket) {
		socket = ""
	}
	panes, err := herdrPanes(bin, socket)
	if err != nil {
		return protocol.EnterPlan{}, false
	}
	for _, pane := range panes {
		if pane.Session == nil || pane.Session.Kind != "id" || pane.Session.Agent != harness || pane.Session.Value != native || !herdrIDRe.MatchString(pane.PaneID) {
			continue
		}
		if _, err := herdrRun(bin, socket, "agent", "focus", pane.PaneID); err != nil {
			return protocol.EnterPlan{Host: host, Note: "cannot focus " + cleanLine(name) + " in herdr; open pane " + pane.PaneID}, true
		}
		return openedHerdrPlan(host, name, pane.PaneID), true
	}
	return protocol.EnterPlan{}, false
}
