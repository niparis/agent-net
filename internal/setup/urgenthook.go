package setup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/daemon"
)

// The Claude Code urgent hook (decision 0180). After every tool call, Claude
// Code runs `agent-net urgent-hook`. When an urgent message waits for the
// session, the hook prints it as additionalContext, so the model reads it
// before its next step instead of at the end of its turn. Most calls print
// nothing and take one local round trip. The hook never blocks or fails a
// call. Setup adds it to ~/.claude/settings.json without a question, like the
// tool-decide hook, because it only delivers a message a peer already sent to
// the session, sooner; uninstall removes it.

const urgentStepName = "claude urgent hook"

// urgentTimeoutSeconds is the hook timeout in seconds. The command itself
// gives up after about 1.8 seconds.
const urgentTimeoutSeconds = 3

// urgentEvents is every tool call, whether it succeeded or failed: an empty
// matcher covers all tools.
var urgentEvents = []struct{ event, matcher string }{
	{"PostToolUse", ""},
	{"PostToolUseFailure", ""},
}

type urgentState struct {
	Installed bool   `json:"installed"` // agent-net added the hook
	Settings  string `json:"settings_path,omitempty"`
}

func urgentStatePath() string {
	return filepath.Join(daemon.ConfigDir(), "claude-urgent-hook.json")
}

func loadUrgentState() urgentState {
	var st urgentState
	if b, err := os.ReadFile(urgentStatePath()); err == nil {
		json.Unmarshal(b, &st)
	}
	return st
}

func saveUrgentState(st urgentState) error {
	b, _ := json.MarshalIndent(st, "", "  ")
	return atomicWrite(urgentStatePath(), append(b, '\n'), 0o600)
}

func urgentCommand(exe string) string {
	if strings.ContainsAny(exe, " \t'\"") {
		return `"` + strings.ReplaceAll(exe, `"`, `\"`) + `" urgent-hook`
	}
	return exe + " urgent-hook"
}

func isUrgentCommand(cmd string) bool { return isOurCommand(cmd, "urgent-hook") }

// ensureUrgentHook puts the hook for exe in the settings file. It writes
// only when the file differs. errNotJSON is returned for a file it must leave
// alone.
func ensureUrgentHook(exe string) (path string, changed bool, err error) {
	path = claudeSettingsPath()
	doc, err := readSettings(path)
	if err != nil {
		return path, false, err
	}
	changed, err = withHooks(doc, urgentCommand(exe), isUrgentCommand, urgentEvents, urgentTimeoutSeconds)
	if err != nil {
		return path, false, err
	}
	if changed {
		if err = atomicWrite(path, doc.encode(), 0o644); err != nil {
			return path, false, err
		}
	}
	return path, changed, saveUrgentState(urgentState{Installed: true, Settings: path})
}

// refreshUrgentHook keeps the hook current for the running binary, so a
// machine set up before this version gets it, and a moved binary is followed.
// It logs only when it wrote. A binary whose name does not say agent-net is
// left alone: its hook could not be told from the owner's.
func refreshUrgentHook(logf func(format string, args ...any)) {
	if claudePath() == "" {
		return
	}
	exe, err := refreshExecutable()
	if err != nil || !isUrgentCommand(urgentCommand(exe)) {
		return
	}
	path, changed, err := ensureUrgentHook(exe)
	if logf == nil {
		return
	}
	switch {
	case err != nil && !errors.Is(err, errNotJSON):
		logf("%s: could not update the hook in %s: %v", urgentStepName, displayPath(path), err)
	case changed:
		logf("%s: updated (hook in %s)", urgentStepName, displayPath(path))
	}
}

func (r *runner) claudeUrgentHook() {
	if claudePath() == "" {
		r.step(urgentStepName, "skipped (claude not found)", nil)
		return
	}
	path, changed, err := ensureUrgentHook(r.Exe)
	switch {
	case errors.Is(err, errNotJSON):
		r.step(urgentStepName, "skipped ("+displayPath(path)+" is not valid JSON, left untouched)", nil)
	case err != nil:
		r.step(urgentStepName, "", err)
	case changed:
		r.step(urgentStepName, "on (hook in "+displayPath(path)+")", nil)
	default:
		r.step(urgentStepName, "on (already in "+displayPath(path)+")", nil)
	}
}

func (r *runner) removeUrgentHook() {
	st := loadUrgentState()
	if !st.Installed {
		r.step(urgentStepName, "skipped (not added by agent-net)", nil)
		return
	}
	path := st.Settings
	if path == "" {
		path = claudeSettingsPath()
	}
	doc, err := readSettings(path)
	if err != nil {
		if errors.Is(err, errNotJSON) {
			r.step(urgentStepName, "skipped ("+displayPath(path)+" is not valid JSON, left untouched)", nil)
		} else {
			r.step(urgentStepName, "", err)
		}
		return
	}
	changed, err := withoutHooks(doc, isUrgentCommand)
	if err != nil {
		r.step(urgentStepName, "", err)
		return
	}
	if changed {
		if err := atomicWrite(path, doc.encode(), 0o644); err != nil {
			r.step(urgentStepName, "", err)
			return
		}
	}
	os.Remove(urgentStatePath())
	if changed {
		r.step(urgentStepName, "ok (removed the hook)", nil)
	} else {
		r.step(urgentStepName, "skipped (hook already gone)", nil)
	}
}
