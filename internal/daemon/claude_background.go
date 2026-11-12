package daemon

import (
	"context"
	"os/exec"
)

// claudeBackground describes one Claude Code background session. Every start
// of a Claude session by this daemon goes through it: the owner's start and
// resume, the merger, proposals, workers, the CTO and the wake fallback. The
// daemon owns the job, so a start needs no herdr pane and no TUI to approve
// it; viewing is a separate step (the view plan).
type claudeBackground struct {
	// Name is the job name shown in `claude agents`.
	Name string
	// Settings is the --settings JSON, when the session has any.
	Settings string
	// Resume is the native ID of the conversation to continue.
	Resume string
	// ModelArgs is the model selection, already as flags.
	ModelArgs []string
	// Flags are session specific flags that come right after --bg.
	Flags []string
	// FirstMessage is the first prompt of a new conversation.
	FirstMessage string
}

// claudeBackgroundArgs is the one place that writes `claude --bg ...`. A test
// (TestClaudeBackgroundHasOneBuilder) fails when another file does it.
func claudeBackgroundArgs(s claudeBackground) []string {
	args := append([]string{"--bg"}, s.Flags...)
	if s.Name != "" {
		args = append(args, "--name", s.Name)
	}
	if s.Settings != "" {
		args = append(args, "--settings", s.Settings)
	}
	if s.Resume != "" {
		args = append(args, "--resume", s.Resume)
	}
	args = append(args, s.ModelArgs...)
	if s.FirstMessage != "" {
		args = append(args, "--", s.FirstMessage)
	}
	return args
}

// startBackground starts the Claude background session in dir and returns its
// session file. The caller holds claudeStartMu until the file is known, so
// discovery cannot register the session first. Records and lifetime rules
// belong to the caller.
func (d *Daemon) startBackground(ctx context.Context, bin, dir string, s claudeBackground, logPath string, env map[string]string, onJob func(string)) (claudeSessionFile, error) {
	return d.launchClaudeBackground(ctx, bin, dir, s, logPath, env, onJob)
}

// runClaudeBackground runs `claude --bg` for the spec and returns its output
// and the background job ID it printed (empty when it printed none). env is the
// complete process environment: the owner and worker launchers and the CTO
// each filter it differently. The output is returned also on failure, so the
// caller can read the trust prompt or the error detail from it.
func runClaudeBackground(ctx context.Context, bin, dir string, s claudeBackground, env []string) (out []byte, jobID string, err error) {
	cmd := exec.CommandContext(ctx, bin, claudeBackgroundArgs(s)...)
	cmd.Dir = dir
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		return out, "", err
	}
	if m := claudeBgID.FindStringSubmatch(string(out)); m != nil {
		jobID = m[1]
	}
	return out, jobID, nil
}
