package setup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/daemon"
)

// The Claude Code tool-decide hook (decision 0166). Before a Bash call or an
// agent-net tool call, Claude Code runs `agent-net tool-decide`. When a scope
// the owner delegated to the session's role covers the call, the hook prints
// permissionDecision allow, so the session is not stopped to ask. In every
// other case it prints nothing and Claude Code asks as before. The hook never
// blocks. Setup adds it to ~/.claude/settings.json without a question, like
// the other settings agent-net keeps there, because it can only remove a
// prompt the owner already delegated; uninstall removes it.

const toolDecideStepName = "claude tool-decide hook"

// toolDecideTimeoutSeconds is the hook timeout in seconds. The command itself
// gives up after about 400 ms.
const toolDecideTimeoutSeconds = 2

// toolDecideMatcher covers the shell and the tools of agent-net's MCP server.
// The CTO's own server (mcp__agent-net-cto__) has its hook in its launch
// settings.
const toolDecideMatcher = "Bash|mcp__agent-net__.*"

var toolDecideEvents = []struct{ event, matcher string }{
	{"PreToolUse", toolDecideMatcher},
}

type toolDecideState struct {
	Installed bool   `json:"installed"` // agent-net added the hook
	Settings  string `json:"settings_path,omitempty"`
}

func toolDecideStatePath() string {
	return filepath.Join(daemon.ConfigDir(), "claude-tool-decide-hook.json")
}

func loadToolDecideState() toolDecideState {
	var st toolDecideState
	if b, err := os.ReadFile(toolDecideStatePath()); err == nil {
		json.Unmarshal(b, &st)
	}
	return st
}

func saveToolDecideState(st toolDecideState) error {
	b, _ := json.MarshalIndent(st, "", "  ")
	return atomicWrite(toolDecideStatePath(), append(b, '\n'), 0o600)
}

func toolDecideCommand(exe string) string {
	if strings.ContainsAny(exe, " \t'\"") {
		return `"` + strings.ReplaceAll(exe, `"`, `\"`) + `" tool-decide`
	}
	return exe + " tool-decide"
}

func isToolDecideCommand(cmd string) bool { return isOurCommand(cmd, "tool-decide") }

// ensureToolDecideHook puts the hook for exe in the settings file. It writes
// only when the file differs. errNotJSON is returned for a file it must leave
// alone.
func ensureToolDecideHook(exe string) (path string, changed bool, err error) {
	path = claudeSettingsPath()
	doc, err := readSettings(path)
	if err != nil {
		return path, false, err
	}
	changed, err = withHooks(doc, toolDecideCommand(exe), isToolDecideCommand, toolDecideEvents, toolDecideTimeoutSeconds)
	if err != nil {
		return path, false, err
	}
	if changed {
		if err = atomicWrite(path, doc.encode(), 0o644); err != nil {
			return path, false, err
		}
	}
	return path, changed, saveToolDecideState(toolDecideState{Installed: true, Settings: path})
}

// refreshToolDecideHook keeps the hook current for the running binary, so a
// machine set up before this version gets it, and a moved binary is followed.
// It logs only when it wrote. A binary whose name does not say agent-net is
// left alone: its hook could not be told from the owner's.
func refreshToolDecideHook(logf func(format string, args ...any)) {
	if claudePath() == "" {
		return
	}
	exe, err := refreshExecutable()
	if err != nil || !isToolDecideCommand(toolDecideCommand(exe)) {
		return
	}
	path, changed, err := ensureToolDecideHook(exe)
	if logf == nil {
		return
	}
	switch {
	case err != nil && !errors.Is(err, errNotJSON):
		logf("%s: could not update the hook in %s: %v", toolDecideStepName, displayPath(path), err)
	case changed:
		logf("%s: updated (hook in %s)", toolDecideStepName, displayPath(path))
	}
}

func (r *runner) claudeToolDecideHook() {
	if claudePath() == "" {
		r.step(toolDecideStepName, "skipped (claude not found)", nil)
		return
	}
	path, changed, err := ensureToolDecideHook(r.Exe)
	switch {
	case errors.Is(err, errNotJSON):
		r.step(toolDecideStepName, "skipped ("+displayPath(path)+" is not valid JSON, left untouched)", nil)
	case err != nil:
		r.step(toolDecideStepName, "", err)
	case changed:
		r.step(toolDecideStepName, "on (hook in "+displayPath(path)+")", nil)
	default:
		r.step(toolDecideStepName, "on (already in "+displayPath(path)+")", nil)
	}
}

func (r *runner) removeToolDecideHook() {
	st := loadToolDecideState()
	if !st.Installed {
		r.step(toolDecideStepName, "skipped (not added by agent-net)", nil)
		return
	}
	path := st.Settings
	if path == "" {
		path = claudeSettingsPath()
	}
	doc, err := readSettings(path)
	if err != nil {
		if errors.Is(err, errNotJSON) {
			r.step(toolDecideStepName, "skipped ("+displayPath(path)+" is not valid JSON, left untouched)", nil)
		} else {
			r.step(toolDecideStepName, "", err)
		}
		return
	}
	changed, err := withoutHooks(doc, isToolDecideCommand)
	if err != nil {
		r.step(toolDecideStepName, "", err)
		return
	}
	if changed {
		if err := atomicWrite(path, doc.encode(), 0o644); err != nil {
			r.step(toolDecideStepName, "", err)
			return
		}
	}
	os.Remove(toolDecideStatePath())
	if changed {
		r.step(toolDecideStepName, "ok (removed the hook)", nil)
	} else {
		r.step(toolDecideStepName, "skipped (hook already gone)", nil)
	}
}
