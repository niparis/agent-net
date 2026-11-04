package setup

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/daemon"
)

// The Claude Code "needs you" hooks (decision 0016). When a tool needs
// approval, or a notification says Claude waits for its user, Claude Code runs
// `agent-net needs-hook`, which tells the local daemon that the session waits.
// A prompt or a tool that runs clears it. Setup adds the hooks to
// ~/.claude/settings.json only with the owner's consent, and uninstall removes
// them.

// Values of Options.NeedsHook.
const (
	NeedsHookYes = "yes" // --claude-needs-hook
	NeedsHookNo  = "no"  // --no-claude-needs-hook
)

const needsStepName = "claude needs hook"

// needsTimeoutSeconds is the hook timeout in seconds. The command itself gives
// up after about 300 ms.
const needsTimeoutSeconds = 2

// needsEvents: the events that mean "a person is needed", and the events that
// show the session moved on. PermissionRequest fires as soon as a tool needs
// approval and names it, where permission_prompt waits about six seconds and
// says less. PreToolUse clears the wait when an approved tool runs, even when
// the session was busy all along. Claude Code's idle_prompt is not here: it
// fires about 60 seconds after any turn ends and means the session finished,
// not that the owner is needed. An older install that still matches it is
// harmless, because `agent-net needs-hook` ignores that notification type.
// AskUserQuestion and ExitPlanMode always stop for the user: their PreToolUse
// starts the wait with the question, and their result (PostToolUse, or
// PostToolUseFailure for a refused call) ends it, even when the model then
// ends its turn without another tool (decision 0057).
var needsEvents = []struct{ event, matcher string }{
	{"PermissionRequest", ""},
	{"Notification", "agent_needs_input|elicitation_dialog|elicitation_url_dialog"},
	{"PreToolUse", ""},
	{"UserPromptSubmit", ""},
	{"PostToolUse", needsDialogTools},
	{"PostToolUseFailure", needsDialogTools},
}

// needsDialogTools are the Claude Code tools that always stop for the user.
const needsDialogTools = "AskUserQuestion|ExitPlanMode"

// refreshExecutable is the binary the refreshed hooks run. Tests replace it.
var refreshExecutable = os.Executable

// refreshNeedsHook brings the hooks up to date when the owner turned them on
// and agent-net added them. The daemon applies unchanged answers without a
// setup run, so a new agent-net version that adds an event reaches machines
// set up before. It writes and logs only when the hooks differ. A binary whose
// name does not say agent-net is left alone: its hooks could not be told from
// the owner's, and every daemon start would add them again.
func refreshNeedsHook(want bool, logf func(format string, args ...any)) {
	st := loadNeedsState()
	if !want || !st.Installed || claudePath() == "" {
		return
	}
	exe, err := refreshExecutable()
	if err != nil || !isNeedsCommand(needsCommand(exe)) {
		return
	}
	path := st.Settings
	if path == "" {
		path = claudeSettingsPath()
	}
	doc, err := readSettings(path)
	if err != nil {
		return
	}
	changed, err := withHooks(doc, needsCommand(exe), isNeedsCommand, needsEvents, needsTimeoutSeconds)
	if err != nil || !changed {
		return
	}
	if err := atomicWrite(path, doc.encode(), 0o644); err != nil {
		if logf != nil {
			logf("%s: could not update the hooks in %s: %v", needsStepName, displayPath(path), err)
		}
		return
	}
	if logf != nil {
		logf("%s: updated (hooks in %s)", needsStepName, displayPath(path))
	}
}

type needsState struct {
	Asked     bool   `json:"asked"`
	Installed bool   `json:"installed"` // agent-net added the hook
	Settings  string `json:"settings_path,omitempty"`
}

func needsStatePath() string { return filepath.Join(daemon.ConfigDir(), "claude-needs-hook.json") }

func loadNeedsState() needsState {
	var st needsState
	if b, err := os.ReadFile(needsStatePath()); err == nil {
		json.Unmarshal(b, &st)
	}
	return st
}

func saveNeedsState(st needsState) error {
	b, _ := json.MarshalIndent(st, "", "  ")
	return atomicWrite(needsStatePath(), append(b, '\n'), 0o600)
}

func needsCommand(exe string) string {
	if strings.ContainsAny(exe, " \t'\"") {
		return `"` + strings.ReplaceAll(exe, `"`, `\"`) + `" needs-hook`
	}
	return exe + " needs-hook"
}

func isNeedsCommand(cmd string) bool { return isOurCommand(cmd, "needs-hook") }

func (r *runner) claudeNeedsHook() {
	if claudePath() == "" {
		r.step(needsStepName, "skipped (claude not found)", nil)
		return
	}
	if r.want != nil && !r.want.NeedsHook {
		if !loadNeedsState().Installed {
			r.step(needsStepName, "off", nil)
			return
		}
		r.removeNeedsHook()
		return
	}
	path := claudeSettingsPath()
	doc, err := readSettings(path)
	if err != nil {
		if errors.Is(err, errNotJSON) {
			r.step(needsStepName, "skipped ("+displayPath(path)+" is not valid JSON, left untouched)", nil)
		} else {
			r.step(needsStepName, "", err)
		}
		return
	}
	st := loadNeedsState()
	install := false
	switch {
	case r.want != nil:
		install = r.want.NeedsHook
	case r.NeedsHook == NeedsHookYes:
		install = true
	case r.NeedsHook == NeedsHookNo:
	case st.Installed:
		install = true // the owner agreed before: keep the command current
	case st.Asked:
	default:
		ans, ok := r.askNeedsHook()
		if !ok {
			r.step(needsStepName, "off (default)", nil)
			r.hint("no terminal to ask. To turn it on later: agent-net setup --claude-needs-hook")
			return
		}
		install = ans
	}
	if !install {
		if !st.Asked {
			if err := saveNeedsState(needsState{Asked: true}); err != nil {
				r.step(needsStepName, "", err)
				return
			}
		}
		r.step(needsStepName, "off", nil)
		return
	}
	changed, err := withHooks(doc, needsCommand(r.Exe), isNeedsCommand, needsEvents, needsTimeoutSeconds)
	if err != nil {
		r.step(needsStepName, "", err)
		return
	}
	if changed {
		if err := atomicWrite(path, doc.encode(), 0o644); err != nil {
			r.step(needsStepName, "", err)
			return
		}
	}
	if err := saveNeedsState(needsState{Asked: true, Installed: true, Settings: path}); err != nil {
		r.step(needsStepName, "", err)
		return
	}
	r.step(needsStepName, "on (hooks in "+displayPath(path)+")", nil)
}

func (r *runner) askNeedsHook() (yes, ok bool) {
	tty, err := openTTY()
	if err != nil {
		return false, false
	}
	defer tty.Close()
	fmt.Fprintln(r.Out, "agent-net can add Claude Code hooks to ~/.claude/settings.json. They tell")
	fmt.Fprintln(r.Out, "the agent-net TUI when Claude waits for your permission or input, so the")
	fmt.Fprintln(r.Out, "session appears under \"Needs you\" with what it asks. A prompt clears that.")
	fmt.Fprintln(r.Out, "Only the local daemon is called. No model is called.")
	fmt.Fprint(r.Out, "Add the hooks? [y/N] ")
	line, _ := bufio.NewReader(tty).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, true
	}
	return false, true
}

func (r *runner) removeNeedsHook() {
	st := loadNeedsState()
	if !st.Installed {
		r.step(needsStepName, "skipped (not added by agent-net)", nil)
		return
	}
	path := st.Settings
	if path == "" {
		path = claudeSettingsPath()
	}
	doc, err := readSettings(path)
	if err != nil {
		if errors.Is(err, errNotJSON) {
			r.step(needsStepName, "skipped ("+displayPath(path)+" is not valid JSON, left untouched)", nil)
		} else {
			r.step(needsStepName, "", err)
		}
		return
	}
	changed, err := withoutHooks(doc, isNeedsCommand)
	if err != nil {
		r.step(needsStepName, "", err)
		return
	}
	if changed {
		if err := atomicWrite(path, doc.encode(), 0o644); err != nil {
			r.step(needsStepName, "", err)
			return
		}
	}
	os.Remove(needsStatePath())
	if changed {
		r.step(needsStepName, "ok (removed the hooks)", nil)
	} else {
		r.step(needsStepName, "skipped (hooks already gone)", nil)
	}
}
