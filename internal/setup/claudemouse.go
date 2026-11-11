package setup

import "github.com/niparis/agent-net/internal/claudesettings"

// claudeMouseStepName is the setup line for the user-level mouse choice.
const claudeMouseStepName = "claude mouse"

// claudeMouseSettings writes the one value that decides mouse tracking for
// every Claude Code session (decision 0128). It merges the key into the user
// settings file and keeps every other key. A new machine gets it at install;
// the daemon keeps it there afterwards.
func (r *runner) claudeMouseSettings() {
	if claudePath() == "" {
		r.step(claudeMouseStepName, "skipped (claude not found)", nil)
		return
	}
	path := claudeSettingsPath()
	changed, err := claudesettings.EnsureMouseOn(path)
	switch {
	case err != nil && claudesettings.IsNotJSON(err):
		r.step(claudeMouseStepName, "skipped ("+displayPath(path)+" is not valid JSON, left untouched)", nil)
	case err != nil:
		r.step(claudeMouseStepName, "", err)
	case changed:
		r.step(claudeMouseStepName, "ok (wheel on)", nil)
	default:
		r.step(claudeMouseStepName, "skipped (already on)", nil)
	}
}

// removeClaudeMouseSettings removes only the key agent-net wrote, and only
// while its value is still "0".
func (r *runner) removeClaudeMouseSettings() {
	path := claudeSettingsPath()
	removed, err := claudesettings.RemoveMouseOn(path)
	switch {
	case err != nil && claudesettings.IsNotJSON(err):
		r.step(claudeMouseStepName, "skipped ("+displayPath(path)+" is not valid JSON, left untouched)", nil)
	case err != nil:
		r.step(claudeMouseStepName, "", err)
	case removed:
		r.step(claudeMouseStepName, "ok (removed)", nil)
	default:
		r.step(claudeMouseStepName, "skipped (not set by agent-net)", nil)
	}
}
