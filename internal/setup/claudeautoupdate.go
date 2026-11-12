package setup

import "github.com/niparis/agent-net/internal/claudesettings"

// claudeAutoUpdateStepName is the setup line for turning Claude Code's own
// updater off. agent-net's daemon is the only updater (decision 0133).
const claudeAutoUpdateStepName = "claude auto-update"

// claudeAutoUpdateSettings writes the one value that turns Claude Code's own
// background updater off for sessions the owner starts by hand. It merges the
// key into the user settings file and keeps every other key. A new machine
// gets it at install; the daemon keeps it there afterwards.
func (r *runner) claudeAutoUpdateSettings() {
	if claudePath() == "" {
		r.step(claudeAutoUpdateStepName, "skipped (claude not found)", nil)
		return
	}
	path := claudeSettingsPath()
	changed, err := claudesettings.EnsureAutoUpdateOff(path)
	switch {
	case err != nil && claudesettings.IsNotJSON(err):
		r.step(claudeAutoUpdateStepName, "skipped ("+displayPath(path)+" is not valid JSON, left untouched)", nil)
	case err != nil:
		r.step(claudeAutoUpdateStepName, "", err)
	case changed:
		r.step(claudeAutoUpdateStepName, "ok (off)", nil)
	default:
		r.step(claudeAutoUpdateStepName, "skipped (already off)", nil)
	}
}

// removeClaudeAutoUpdateSettings removes only the key agent-net wrote, and only
// while its value is still "1".
func (r *runner) removeClaudeAutoUpdateSettings() {
	path := claudeSettingsPath()
	removed, err := claudesettings.RemoveAutoUpdateOff(path)
	switch {
	case err != nil && claudesettings.IsNotJSON(err):
		r.step(claudeAutoUpdateStepName, "skipped ("+displayPath(path)+" is not valid JSON, left untouched)", nil)
	case err != nil:
		r.step(claudeAutoUpdateStepName, "", err)
	case removed:
		r.step(claudeAutoUpdateStepName, "ok (removed)", nil)
	default:
		r.step(claudeAutoUpdateStepName, "skipped (not set by agent-net)", nil)
	}
}
