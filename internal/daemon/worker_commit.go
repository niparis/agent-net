package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/niparis/agent-net/internal/settings"
)

// Worker commit identity (owner request). Every commit a worker makes in its
// worktree carries trailers that name the worker, its model, its harness, the
// difficulty the caller asked for, and the caller. The daemon installs a
// prepare-commit-msg hook and points only the worker's own processes at it, so
// the model does not have to add the lines and the caller's later merges in
// their own checkout stay untouched.
const (
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
add "$AGENT_NET_TRAILER_NAME_WORKER" "$worker"
add "$AGENT_NET_TRAILER_NAME_MODEL" "$AGENT_NET_TRAILER_MODEL"
add "$AGENT_NET_TRAILER_NAME_HARNESS" "$AGENT_NET_TRAILER_HARNESS"
add "$AGENT_NET_TRAILER_NAME_DIFFICULTY" "$AGENT_NET_TRAILER_DIFFICULTY"
add "$AGENT_NET_TRAILER_NAME_CALLER" "$AGENT_NET_TRAILER_CALLER"

hooks="${AGENT_NET_TRAILER_PROJECT_HOOKS:-}"
if [ -n "$hooks" ] && [ -x "$hooks/prepare-commit-msg" ] && [ "$hooks/prepare-commit-msg" != "$0" ]; then
	"$hooks/prepare-commit-msg" "$@" || exit $?
fi
exit 0
`

// workerForwardedHooks are the standard git hooks (githooks(5)) other than
// prepare-commit-msg. core.hooksPath replaces the whole hook folder, so each
// one forwards to the repository's own hook of the same name. Otherwise
// pointing git at the worker folder would silently disable pre-commit,
// commit-msg, pre-push and the rest.
var workerForwardedHooks = []string{
	"applypatch-msg",
	"pre-applypatch",
	"post-applypatch",
	"pre-commit",
	"pre-merge-commit",
	"commit-msg",
	"post-commit",
	"pre-rebase",
	"post-checkout",
	"post-merge",
	"pre-push",
	"pre-receive",
	"update",
	"proc-receive",
	"post-receive",
	"post-update",
	"reference-transaction",
	"push-to-checkout",
	"pre-auto-gc",
	"post-rewrite",
	"sendemail-validate",
	"fsmonitor-watchman",
	"p4-changelist",
	"p4-prepare-changelist",
	"p4-post-changelist",
	"p4-pre-submit",
	"post-index-change",
}

// workerForwardHook is the script for one forwarded hook. It execs the
// repository's hook of the same name, passing the arguments and standard input
// through, and returns its exit code. It does nothing when the repository has
// no such hook.
func workerForwardHook(name string) string {
	return fmt.Sprintf(`#!/bin/sh
# Installed by agent-net for a worker's worktree. Do not edit.
# Runs the repository's own %s hook, when it has one.
hooks="${AGENT_NET_TRAILER_PROJECT_HOOKS:-}"
hook="$hooks/%s"
if [ -n "$hooks" ] && [ -x "$hook" ] && [ "$hook" != "$0" ]; then
	exec "$hook" "$@"
fi
exit 0
`, name, name)
}

// installWorkerCommitHooks writes the trailer hook and the forwarding hooks
// into the worker's private hook folder.
func installWorkerCommitHooks(hooks string) error {
	if err := os.WriteFile(filepath.Join(hooks, "prepare-commit-msg"), []byte(workerCommitHook), 0o700); err != nil {
		return err
	}
	for _, name := range workerForwardedHooks {
		if err := os.WriteFile(filepath.Join(hooks, name), []byte(workerForwardHook(name)), 0o700); err != nil {
			return err
		}
	}
	return nil
}

// workerCommitEnv returns the environment for a worker's own processes: it
// points git at the daemon's prepare-commit-msg hook through the command scope
// (GIT_CONFIG_*) and carries the trailer values. It is empty for a worker
// without a worktree: a commit in the owner's checkout must stay untouched.
func (d *Daemon) workerCommitEnv(w *worker) map[string]string {
	d.wmu.Lock()
	dir, id := w.worktree, w.id
	identity := workerIdentity{
		session: w.sessionID, model: w.model, harness: w.harness,
		difficultyAsked: w.difficultyAsked, difficultyUsed: w.difficultyUsed,
		owner: w.owner, ownerName: w.ownerName,
	}
	d.wmu.Unlock()
	if dir == "" {
		return nil
	}
	hooks := filepath.Join(d.cfg.StateDir, "workers", id, "git-hooks")
	if err := os.MkdirAll(hooks, 0o700); err != nil {
		d.Logf("worker %s: commit hook folder: %v", id, err)
		return nil
	}
	if err := installWorkerCommitHooks(hooks); err != nil {
		d.Logf("worker %s: commit hooks: %v", id, err)
		return nil
	}
	snap := d.SettingsSnapshot()
	trailerNames := map[string]string{
		"AGENT_NET_TRAILER_NAME_WORKER":     snap.String("delivery.trailer_worker", settings.Where{}),
		"AGENT_NET_TRAILER_NAME_MODEL":      snap.String("delivery.trailer_model", settings.Where{}),
		"AGENT_NET_TRAILER_NAME_HARNESS":    snap.String("delivery.trailer_harness", settings.Where{}),
		"AGENT_NET_TRAILER_NAME_DIFFICULTY": snap.String("delivery.trailer_difficulty", settings.Where{}),
		"AGENT_NET_TRAILER_NAME_CALLER":     snap.String("delivery.trailer_caller", settings.Where{}),
	}
	env := map[string]string{
		"GIT_CONFIG_COUNT":   "1",
		"GIT_CONFIG_KEY_0":   "core.hooksPath",
		"GIT_CONFIG_VALUE_0": hooks,
	}
	for key, value := range trailerNames {
		env[key] = value
	}
	sessionFile := filepath.Join(hooks, "worker-session")
	if err := os.WriteFile(sessionFile, []byte(identity.session), 0o600); err != nil {
		d.Logf("worker %s: commit session file: %v", id, err)
	} else {
		env[envTrailerSession] = sessionFile
	}
	for name, value := range workerTrailerValues(identity) {
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

// workerIdentity is the part of a worker record that becomes commit trailers.
// It is a copy, taken under Daemon.wmu, so a session ID learned in another
// goroutine cannot race with building the hook environment.
type workerIdentity struct {
	session, model, harness, difficultyAsked, difficultyUsed, owner, ownerName string
}

// workerTrailerValues is the identity of a worker as one line per trailer.
func workerTrailerValues(w workerIdentity) map[string]string {
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
		envTrailerWorker:     trailerText(w.session),
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
