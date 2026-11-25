package setup

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/niparis/agent-net/internal/agentfiles"
)

// The Claude Code "agent-net workers" plugin. Setup writes the plugin files
// from this binary in agent-net's own folder and puts one link to them in the
// skills folder of Claude Code's config directory. Claude Code loads a plugin
// there with no marketplace and no settings entry, so the window survives a
// settings file that another tool owns. The daemon refreshes the files and puts
// a missing link back on every start, so the plugin follows the binary after an
// upgrade that did not run setup.

// Values of Options.WorkersMod.
const (
	WorkersModYes = "yes" // --claude-workers-mod
	WorkersModNo  = "no"  // --no-claude-workers-mod
)

const workersModStepName = "claude workers mod"

type workersModState struct {
	Asked     bool   `json:"asked"`
	Installed bool   `json:"installed"` // the owner said yes; the daemon keeps the link
	Dir       string `json:"dir,omitempty"`
}

func workersModStatePath() string {
	return agentfiles.ClaudeWorkersStatePath()
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
	st := loadWorkersModState()
	live := agentfiles.ClaudeWorkersStatusNow()

	if (r.want != nil && !r.want.WorkersMod && !r.keepCurrent) || r.WorkersMod == WorkersModNo {
		r.removeClaudeWorkersMod()
		return
	}

	install := false
	switch {
	case r.want != nil:
		install = r.want.WorkersMod
	case r.WorkersMod == WorkersModYes:
		install = true
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
		// A run without an answer keeps what this machine has. It must not undo
		// a disable the owner chose in Claude Code, and it records no answer.
		if r.keepCurrent {
			r.step(workersModStepName, claudeWorkersLiveText(live), nil)
			return
		}
		if !st.Asked {
			if err := saveWorkersModState(workersModState{Asked: true}); err != nil {
				r.step(workersModStepName, "", err)
				return
			}
		}
		r.step(workersModStepName, "off", nil)
		return
	}

	changed, err := agentfiles.WriteClaudeWorkersAt(r.Exe)
	if err != nil {
		r.step(workersModStepName, "", err)
		return
	}
	_, foreign, err := agentfiles.EnsureClaudeWorkersLink()
	if err != nil {
		r.step(workersModStepName, "", err)
		return
	}
	if foreign != "" {
		_ = saveWorkersModState(workersModState{Asked: true, Installed: true, Dir: agentfiles.ClaudeWorkersDir()})
		r.step(workersModStepName, "off (a foreign entry has the name)", nil)
		r.hint("hint: %s is not agent-net's link. Move it away, then run agent-net setup --claude-workers-mod", displayPath(foreign))
		return
	}
	migrateNote := agentfiles.MigrateClaudeWorkers()
	live = agentfiles.ClaudeWorkersStatusNow()
	if err := saveWorkersModState(workersModState{Asked: true, Installed: true, Dir: agentfiles.ClaudeWorkersDir()}); err != nil {
		r.step(workersModStepName, "", err)
		return
	}
	if migrateNote != "" {
		r.hint("hint: %s", migrateNote)
	}
	switch {
	case live.OldRuns():
		r.step(workersModStepName, "on (old marketplace copy, and the skills link is ready)", nil)
	case live.Disabled:
		r.step(workersModStepName, "off (disabled in Claude Code; the skills link is in place)", nil)
	case live.BlockedByInstalledCopy():
		r.step(workersModStepName, "off (the old installed copy blocks the skills plugin)", nil)
		r.hint("hint: run claude plugin uninstall %s -s user", agentfiles.ClaudeWorkersOldID)
	case !st.Installed:
		r.step(workersModStepName, "on (plugin agent-net-workers in the Claude skills folder)", nil)
	case changed:
		r.step(workersModStepName, "updated (plugin agent-net-workers in the Claude skills folder)", nil)
		r.hint("hint: open Claude Code sessions: run /reload-plugins or restart them to load the new panel")
	default:
		r.step(workersModStepName, "ok (unchanged)", nil)
	}
}

// claudeWorkersLiveText says what Claude Code can load now, without changing
// anything.
func claudeWorkersLiveText(s agentfiles.ClaudeWorkersStatus) string {
	switch {
	case s.Foreign != "":
		return "off (a foreign entry has the name)"
	case s.OldRuns():
		return "on (old marketplace copy)"
	case s.Disabled:
		return "off (disabled in Claude Code)"
	case s.BlockedByInstalledCopy():
		return "off (the old installed copy blocks the skills plugin)"
	case s.LinkOK:
		return "on (plugin agent-net-workers in the Claude skills folder)"
	default:
		return "off"
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
	fmt.Fprintln(r.Out, "ends. Setup puts one link to "+displayPath(agentfiles.ClaudeWorkersPluginDir())+" in")
	fmt.Fprintln(r.Out, "Claude Code's skills folder. It writes no plugin entry to your settings file.")
	fmt.Fprint(r.Out, "Install the plugin? [y/N] ")
	line, _ := bufio.NewReader(tty).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, true
	}
	return false, true
}

// removeClaudeWorkersMod turns the plugin off in agent-net: it removes only
// agent-net's own link and, when the settings file is agent-net's to write,
// the old marketplace entry. A foreign entry and a disable the owner chose in
// Claude Code are left alone.
func (r *runner) removeClaudeWorkersMod() {
	st := loadWorkersModState()
	removed, foreign, err := agentfiles.RemoveClaudeWorkersLink()
	if err != nil {
		r.step(workersModStepName, "", err)
		return
	}
	oldNote := agentfiles.RemoveOldClaudeWorkers()
	os.Remove(workersModStatePath())
	agentfiles.RemoveClaudeWorkersRecords()
	status := agentfiles.ClaudeWorkersStatusNow()
	switch {
	case foreign != "":
		r.step(workersModStepName, "off (a foreign entry has the name; it was left alone)", nil)
		r.hint("hint: %s is not agent-net's link", displayPath(foreign))
	case oldNote != "":
		r.step(workersModStepName, "off (removed; "+oldNote+")", nil)
	case status.Disabled:
		r.step(workersModStepName, "off (the owner turned it off in Claude Code; that choice was kept)", nil)
	case removed || st.Installed:
		r.step(workersModStepName, "off (removed agent-net's link)", nil)
	default:
		r.step(workersModStepName, "off", nil)
	}
}

// removeClaudeWorkersModFiles is uninstall: the link, the plugin files and the
// old marketplace registration, when the settings file is agent-net's to write.
func (r *runner) removeClaudeWorkersModFiles() {
	dir := agentfiles.ClaudeWorkersDir()
	st := loadWorkersModState()
	folder := exists(dir)
	if !folder && !st.Installed {
		r.step(workersModStepName, "skipped (not installed)", nil)
		return
	}
	_, foreign, err := agentfiles.RemoveClaudeWorkersLink()
	if err != nil {
		r.step(workersModStepName, "", err)
		return
	}
	oldNote := agentfiles.RemoveOldClaudeWorkers()
	if err := os.RemoveAll(dir); err != nil {
		r.step(workersModStepName, "", err)
		return
	}
	os.Remove(workersModStatePath())
	agentfiles.RemoveClaudeWorkersRecords()
	switch {
	case foreign != "":
		r.step(workersModStepName, "ok (removed agent-net's files; a foreign entry with the name was left alone)", nil)
	case oldNote != "":
		r.step(workersModStepName, "ok (removed; "+oldNote+")", nil)
	default:
		r.step(workersModStepName, "ok (removed)", nil)
	}
}
