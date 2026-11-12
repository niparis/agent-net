// Package claudelaunch sets the environment of every Claude Code process
// agent-net starts.
//
// agent-net's daemon is the only updater for the harnesses on the owner's
// machines (decision 0139). Claude Code otherwise runs its own background
// updater in every session, which loses the race against the daemon and shows
// "update failed / run claude doctor". So every launch carries
// DISABLE_AUTOUPDATER=1: owner starts and resumes, workers, the merger, the
// CTO, sign-in panes and the compaction attach. The user settings file carries
// the same key for sessions the owner starts by hand.
//
// New launch paths call Env (for a process) or Plan (for an enter plan) rather
// than adding the variable by themselves.
package claudelaunch

import (
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/claudesettings"
	"github.com/niparis/agent-net/internal/protocol"
)

// Env returns env with DISABLE_AUTOUPDATER=1, replacing any inherited value.
func Env(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); k == claudesettings.AutoUpdateKey {
			continue
		}
		out = append(out, kv)
	}
	return append(out, claudesettings.AutoUpdateKey+"="+claudesettings.AutoUpdateOff)
}

// Plan adds the variable to an enter plan that runs Claude Code, and returns
// any other plan unchanged. The value travels in the plan's environment, which
// never crosses the gateway, so the session's own machine sets it.
func Plan(plan protocol.EnterPlan) protocol.EnterPlan {
	if !Runs(plan.Argv) {
		return plan
	}
	return WithAutoUpdateOff(plan)
}

// WithAutoUpdateOff adds the variable to a plan the caller knows runs Claude
// Code. Use it when the binary is known from the harness, not from its name,
// for example a wrapper or a test binary.
func WithAutoUpdateOff(plan protocol.EnterPlan) protocol.EnterPlan {
	env := make(map[string]string, len(plan.Env)+1)
	for k, v := range plan.Env {
		env[k] = v
	}
	env[claudesettings.AutoUpdateKey] = claudesettings.AutoUpdateOff
	plan.Env = env
	return plan
}

// Runs says whether argv starts the Claude Code binary.
func Runs(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	return filepath.Base(argv[0]) == "claude"
}
