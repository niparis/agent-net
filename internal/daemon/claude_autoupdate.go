package daemon

import (
	"context"
	"fmt"

	"github.com/niparis/agent-net/internal/claudelaunch"
	"github.com/niparis/agent-net/internal/claudesettings"
	"github.com/niparis/agent-net/internal/protocol"
)

// agent-net's daemon is the only updater for every harness on the owner's
// machines (decision 0139). Claude Code runs its own background updater in
// every session, so two updaters race on the same install and the in-session
// one reports "update failed / run claude doctor". Every Claude Code process
// agent-net starts carries DISABLE_AUTOUPDATER=1, and the user settings file
// carries the same key for sessions the owner starts by hand.

// claudeLaunchEnv adds DISABLE_AUTOUPDATER=1 to the environment of a Claude
// Code process. Every launch site calls it, so a new path cannot forget it.
func claudeLaunchEnv(env []string) []string { return claudelaunch.Env(env) }

// claudePlanEnv adds DISABLE_AUTOUPDATER=1 to an enter plan that runs Claude
// Code, and returns any other plan unchanged.
func claudePlanEnv(plan protocol.EnterPlan) protocol.EnterPlan { return claudelaunch.Plan(plan) }

// claudePlanEnvAlways adds DISABLE_AUTOUPDATER=1 to a plan the caller knows
// runs Claude Code, whatever its binary is called.
func claudePlanEnvAlways(plan protocol.EnterPlan) protocol.EnterPlan {
	return claudelaunch.WithAutoUpdateOff(plan)
}

// ensureClaudeAutoUpdateSetting writes the value when the user settings file
// does not carry it, and puts a drifted value back. It follows the mouse key:
// only that key is touched, and a file that is not valid JSON is reported and
// left alone.
func (d *Daemon) ensureClaudeAutoUpdateSetting(ctx context.Context) {
	path := d.claudeSettingsFile()
	if path == "" {
		return
	}
	changed, err := claudesettings.EnsureAutoUpdateOff(path)
	if err != nil {
		if claudesettings.IsNotJSON(err) {
			d.reportMouseNotice(ctx, "settings:"+path, fmt.Sprintf(
				"Claude settings %s is not valid JSON; the auto-update setting was not applied", path))
			return
		}
		d.Logf("claude auto-update: %v", err)
		return
	}
	if changed {
		d.Logf("claude auto-update: set %s=%s in %s", claudesettings.AutoUpdateKey, claudesettings.AutoUpdateOff, path)
	}
}
