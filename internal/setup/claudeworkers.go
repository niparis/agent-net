package setup

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/agentfiles"
	"github.com/niparis/agent-net/internal/daemon"
)

// The Claude Code "agent-net workers" plugin. Setup writes a local marketplace
// with the plugin files embedded in this binary, then registers both with the
// claude CLI (user scope, after the owner's consent). Uninstall removes them.
// The daemon refreshes the same folder on every start, so the plugin follows
// the binary after an upgrade, including one that did not run setup.

// Values of Options.WorkersMod.
const (
	WorkersModYes = "yes" // --claude-workers-mod
	WorkersModNo  = "no"  // --no-claude-workers-mod
)

const workersModStepName = "claude workers mod"

type workersModState struct {
	Asked     bool   `json:"asked"`
	Installed bool   `json:"installed"` // agent-net registered the plugin
	Dir       string `json:"dir,omitempty"`
}

func workersModStatePath() string {
	return filepath.Join(daemon.ConfigDir(), "claude-workers-mod.json")
}

func loadWorkersModState() workersModState {
	var st workersModState
	if b, err := os.ReadFile(workersModStatePath()); err == nil {
		json.Unmarshal(b, &st)
	}
	return st
}

func saveWorkersModState(st workersModState) error {
	b, _ := json.MarshalIndent(st, "", "  ")
	return atomicWrite(workersModStatePath(), append(b, '\n'), 0o600)
}

func (r *runner) claudeWorkersMod() {
	if claudePath() == "" {
		r.step(workersModStepName, "skipped (claude not found)", nil)
		return
	}
	if r.want != nil && !r.want.WorkersMod {
		if !loadWorkersModState().Installed && !exists(agentfiles.ClaudeWorkersDir()) {
			r.step(workersModStepName, "off", nil)
			return
		}
		r.removeClaudeWorkersMod()
		return
	}
	st := loadWorkersModState()
	install := false
	switch {
	case r.want != nil:
		install = r.want.WorkersMod
	case r.WorkersMod == WorkersModYes:
		install = true
	case r.WorkersMod == WorkersModNo:
	case st.Installed:
		install = true // the owner agreed before: keep the plugin current
	case st.Asked:
	default:
		ans, ok := r.askWorkersMod()
		if !ok {
			r.step(workersModStepName, "off (default)", nil)
			r.hint("no terminal to ask. To turn it on later: agent-net setup --claude-workers-mod")
			return
		}
		install = ans
	}
	if !install {
		if !st.Asked {
			if err := saveWorkersModState(workersModState{Asked: true}); err != nil {
				r.step(workersModStepName, "", err)
				return
			}
		}
		r.step(workersModStepName, "off", nil)
		return
	}

	dir := agentfiles.ClaudeWorkersDir()
	changed, err := agentfiles.WriteClaudeWorkers()
	if err != nil {
		r.step(workersModStepName, "", err)
		return
	}
	claude := claudePath()
	if out, err := runCmd(home(), claude, "plugin", "marketplace", "add", dir); err != nil {
		r.softStep(workersModStepName, "files written, not registered (claude plugin marketplace add: "+firstLine(out, err)+")")
		return
	}
	if out, err := runCmd(home(), claude, "plugin", "install", "agent-net-workers@agent-net", "-s", "user"); err != nil {
		r.softStep(workersModStepName, "files written, not registered (claude plugin install: "+firstLine(out, err)+")")
		return
	}
	if st.Installed && changed {
		// Claude Code reads the plugin from this folder. Refresh its catalog,
		// then let it record the new version, so a session reloads these files.
		if out, err := runCmd(home(), claude, "plugin", "marketplace", "update", agentfiles.ClaudeWorkersMarketplace); err != nil {
			r.hint("hint: claude plugin marketplace update failed: %s", firstLine(out, err))
		}
		if out, err := runCmd(home(), claude, "plugin", "update", "agent-net-workers@agent-net", "-s", "user"); err != nil {
			r.hint("hint: claude plugin update failed: %s", firstLine(out, err))
		}
	}
	if err := saveWorkersModState(workersModState{Asked: true, Installed: true, Dir: dir}); err != nil {
		r.step(workersModStepName, "", err)
		return
	}
	switch {
	case !st.Installed:
		r.step(workersModStepName, "on (plugin agent-net-workers@agent-net, user scope)", nil)
	case changed:
		r.step(workersModStepName, "updated (plugin agent-net-workers@agent-net)", nil)
		r.hint("hint: open Claude Code sessions: run /reload-plugins")
	default:
		r.step(workersModStepName, "ok (unchanged)", nil)
	}
}

// askWorkersMod asks on the controlling terminal. ok is false when there is no
// terminal.
func (r *runner) askWorkersMod() (yes, ok bool) {
	tty, err := openTTY()
	if err != nil {
		return false, false
	}
	defer tty.Close()
	fmt.Fprintln(r.Out, "agent-net can install a Claude Code plugin, agent-net workers. In every")
	fmt.Fprintln(r.Out, "Claude Code session on this machine it shows the workers that session started")
	fmt.Fprintln(r.Out, "in a band above the prompt and on the status line, and a line when a worker")
	fmt.Fprintln(r.Out, "ends. Setup registers a local marketplace at "+displayPath(agentfiles.ClaudeWorkersDir())+".")
	fmt.Fprint(r.Out, "Install the plugin? [y/N] ")
	line, _ := bufio.NewReader(tty).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, true
	}
	return false, true
}

func (r *runner) removeClaudeWorkersMod() {
	dir := agentfiles.ClaudeWorkersDir()
	st := loadWorkersModState()
	folder := exists(dir)
	if !folder && !st.Installed {
		r.step(workersModStepName, "skipped (not installed)", nil)
		return
	}
	claude := claudePath()
	cliNote := ""
	if claude != "" {
		if out, err := runCmd(home(), claude, "plugin", "uninstall", "agent-net-workers@agent-net", "-s", "user"); err != nil {
			cliNote = firstLine(out, err)
		}
		if out, err := runCmd(home(), claude, "plugin", "marketplace", "remove", agentfiles.ClaudeWorkersMarketplace); err != nil && cliNote == "" {
			cliNote = firstLine(out, err)
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		r.step(workersModStepName, "", err)
		return
	}
	os.Remove(workersModStatePath())
	switch {
	case claude == "":
		r.step(workersModStepName, "ok (removed the folder; claude not found, so the plugin entries were left in Claude Code)", nil)
	case cliNote != "":
		r.step(workersModStepName, "ok (removed the folder; the claude CLI said: "+cliNote+")", nil)
	default:
		r.step(workersModStepName, "ok (removed)", nil)
	}
}

// softStep prints a failed step but does not fail setup: a missing or failing
// claude CLI must not stop the rest of setup.
func (r *runner) softStep(name, result string) {
	fmt.Fprintf(r.Out, "%-21s %s\n", name, result)
}
