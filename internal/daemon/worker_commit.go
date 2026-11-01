package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// Worker commit identity (owner request). Every commit a worker makes in its
// worktree carries trailers that name the worker, its model, its harness, the
// difficulty the caller asked for, and the caller. The daemon installs a
// prepare-commit-msg hook and points only the worker's own processes at it, so
// the model does not have to add the lines and the caller's later merges in
// their own checkout stay untouched.
const (
	trailerWorker     = "Agent-Net-Worker"
	trailerModel      = "Agent-Net-Model"
	trailerHarness    = "Agent-Net-Harness"
	trailerDifficulty = "Agent-Net-Difficulty"
	trailerCaller     = "Agent-Net-Caller"

	envTrailerWorker     = "AGENT_NET_TRAILER_WORKER"
	envTrailerModel      = "AGENT_NET_TRAILER_MODEL"
	envTrailerHarness    = "AGENT_NET_TRAILER_HARNESS"
	envTrailerDifficulty = "AGENT_NET_TRAILER_DIFFICULTY"
	envTrailerCaller     = "AGENT_NET_TRAILER_CALLER"
	envTrailerHooks      = "AGENT_NET_TRAILER_PROJECT_HOOKS"
	envTrailerSession    = "AGENT_NET_TRAILER_SESSION_FILE"
)

// workerCommitHook adds the worker identity trailers to a commit message, then
// runs the project's own prepare-commit-msg hook if it has one. The values are
// in the environment of the worker's processes, never in the script.
const workerCommitHook = `#!/bin/sh
# Installed by agent-net for a worker's worktree. Do not edit.
# It adds the worker identity trailers, then runs the repository's own hook.
msg="$1"
[ -n "$msg" ] || exit 0

add() {
	[ -n "$2" ] || return 0
	git interpret-trailers --if-exists replace --in-place --trailer "$1: $2" -- "$msg"
}

worker="${AGENT_NET_TRAILER_WORKER:-}"
if [ -z "$worker" ] && [ -n "${AGENT_NET_TRAILER_SESSION_FILE:-}" ] && [ -f "$AGENT_NET_TRAILER_SESSION_FILE" ]; then
	worker="$(cat "$AGENT_NET_TRAILER_SESSION_FILE")"
fi
add Agent-Net-Worker "$worker"
add Agent-Net-Model "$AGENT_NET_TRAILER_MODEL"
add Agent-Net-Harness "$AGENT_NET_TRAILER_HARNESS"
add Agent-Net-Difficulty "$AGENT_NET_TRAILER_DIFFICULTY"
add Agent-Net-Caller "$AGENT_NET_TRAILER_CALLER"

hooks="${AGENT_NET_TRAILER_PROJECT_HOOKS:-}"
if [ -n "$hooks" ] && [ -x "$hooks/prepare-commit-msg" ] && [ "$hooks/prepare-commit-msg" != "$0" ]; then
	"$hooks/prepare-commit-msg" "$@" || exit $?
fi
exit 0
`

// workerCommitEnv returns the environment for a worker's own processes: it
// points git at the daemon's prepare-commit-msg hook through the command scope
// (GIT_CONFIG_*) and carries the trailer values. It is empty for a worker
// without a worktree: a commit in the owner's checkout must stay untouched.
func (d *Daemon) workerCommitEnv(w *worker) map[string]string {
	d.wmu.Lock()
	dir, id, session := w.worktree, w.id, w.sessionID
	d.wmu.Unlock()
	if dir == "" {
		return nil
	}
	hooks := filepath.Join(d.cfg.StateDir, "workers", id, "git-hooks")
	if err := os.MkdirAll(hooks, 0o700); err != nil {
		d.Logf("worker %s: commit hook folder: %v", id, err)
		return nil
	}
	hook := filepath.Join(hooks, "prepare-commit-msg")
	if err := os.WriteFile(hook, []byte(workerCommitHook), 0o700); err != nil {
		d.Logf("worker %s: commit hook: %v", id, err)
		return nil
	}
	env := map[string]string{
		"GIT_CONFIG_COUNT":   "1",
		"GIT_CONFIG_KEY_0":   "core.hooksPath",
		"GIT_CONFIG_VALUE_0": hooks,
	}
	sessionFile := filepath.Join(hooks, "worker-session")
	if err := os.WriteFile(sessionFile, []byte(session), 0o600); err != nil {
		d.Logf("worker %s: commit session file: %v", id, err)
	} else {
		env[envTrailerSession] = sessionFile
	}
	for name, value := range workerTrailerValues(w) {
		env[name] = value
	}
	if project := projectHooksPath(context.Background(), dir); project != "" {
		env[envTrailerHooks] = project
	}
	return env
}

// writeWorkerCommitSession records the worker's gateway session for the commit
// hook. It is inside the worker's worktree check because a commit outside one
// carries no trailers. The caller holds Daemon.wmu.
func (d *Daemon) writeWorkerCommitSession(w *worker) {
	if w.worktree == "" {
		return
	}
	hooks := filepath.Join(d.cfg.StateDir, "workers", w.id, "git-hooks")
	if err := os.MkdirAll(hooks, 0o700); err != nil {
		d.Logf("worker %s: commit hook folder: %v", w.id, err)
		return
	}
	if err := os.WriteFile(filepath.Join(hooks, "worker-session"), []byte(w.sessionID), 0o600); err != nil {
		d.Logf("worker %s: commit session file: %v", w.id, err)
	}
}

// workerTrailerValues is the identity of a worker as one line per trailer.
func workerTrailerValues(w *worker) map[string]string {
	difficulty := w.difficultyAsked
	if difficulty == "" {
		difficulty = w.difficultyUsed
	}
	if difficulty == "" {
		difficulty = "normal"
	}
	caller := trailerText(w.owner)
	if name := trailerText(w.ownerName); name != "" && name != caller {
		caller = name + " (" + caller + ")"
	}
	return map[string]string{
		envTrailerWorker:     trailerText(w.sessionID),
		envTrailerModel:      trailerText(w.model),
		envTrailerHarness:    trailerText(w.harness),
		envTrailerDifficulty: trailerText(difficulty),
		envTrailerCaller:     caller,
	}
}

// projectHooksPath is the hook folder git would use without the worker's
// override. The hook script runs the project's prepare-commit-msg from there.
func projectHooksPath(ctx context.Context, dir string) string {
	out, err := gitOutWithEnv(ctx, dir, []string{"GIT_CONFIG_COUNT=0"}, "rev-parse", "--git-path", "hooks")
	if err != nil || out == "" {
		return ""
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(dir, out)
	}
	return filepath.Clean(out)
}

// trailerText makes a value safe for one trailer line.
func trailerText(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	const max = 200
	if len(s) > max {
		s = strings.ToValidUTF8(s[:max], "")
	}
	return strings.TrimSpace(s)
}

// workerGitEnv returns env with the worker's commit settings, and without any
// inherited command-scope git config or trailer values.
func (d *Daemon) workerGitEnv(w *worker, env []string, set map[string]string) []string {
	if set == nil {
		set = map[string]string{}
	}
	for k, v := range d.workerCommitEnv(w) {
		set[k] = v
	}
	out := make([]string, 0, len(env)+len(set))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "GIT_CONFIG_") || strings.HasPrefix(k, "AGENT_NET_TRAILER_") {
			continue
		}
		if _, replaced := set[k]; replaced {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range set {
		out = append(out, k+"="+v)
	}
	return out
}
