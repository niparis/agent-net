package daemon

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// commitTestDaemon is a daemon with private state, enough for the worktree and
// commit hook helpers. It never touches the real setup.
func commitTestDaemon(t *testing.T) *Daemon {
	t.Helper()
	return &Daemon{cfg: Config{StateDir: t.TempDir()}, log: log.New(io.Discard, "", 0)}
}

// commitTestWorker makes a worktree for a worker with a full identity.
func commitTestWorker(t *testing.T, d *Daemon) (*worker, string) {
	t.Helper()
	repo := newRepo(t, "commit-project")
	w := &worker{
		id: protocol.NewID("w"), harness: "codex", project: "commit-project",
		path: repo, model: "gpt-6.1-terra:high", owner: protocol.NewID("s"), ownerName: "agent-net",
		difficultyAsked: "normal", sessionID: protocol.NewID("s"),
	}
	if err := d.createWorktree(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	return w, w.worktree
}

// gitTestEnv is a git environment without the owner's global or system config.
// extra comes last, so it wins over an inherited value.
func gitTestEnv(extra map[string]string) []string {
	env := append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

func gitWithEnv(t *testing.T, dir string, extra map[string]string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitTestEnv(extra)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// fakeHarnessCommit is a stand-in for a worker harness: it commits what it
// changed in the worktree, with the environment the daemon gave it.
func fakeHarnessCommit(t *testing.T, dir string, env map[string]string, message string) {
	t.Helper()
	if err := runFakeHarnessCommit(dir, env, message); err != nil {
		t.Fatalf("fake harness: %v", err)
	}
}

func runFakeHarnessCommit(dir string, env map[string]string, message string) error {
	script := "set -e\ngit add -A\ngit commit -q -m " + testShellQuote(message) + "\n"
	cmd := exec.Command("sh", "-c", script)
	cmd.Dir = dir
	cmd.Env = gitTestEnv(env)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w\n%s", err, out)
	}
	return nil
}

func testShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func trailerValue(t *testing.T, dir, key string) string {
	t.Helper()
	return gitWithEnv(t, dir, nil, "log", "-1", "--format=%(trailers:key="+key+",valueonly)")
}

func wantTrailers(w *worker) map[string]string {
	return map[string]string{
		"Agent-Net-Worker":     w.sessionID,
		"Agent-Net-Model":      "gpt-6.1-terra:high",
		"Agent-Net-Harness":    "codex",
		"Agent-Net-Difficulty": "normal",
		"Agent-Net-Caller":     "agent-net (" + w.owner + ")",
	}
}

func TestWorkerCommitHasIdentityTrailers(t *testing.T) {
	d := commitTestDaemon(t)
	w, dir := commitTestWorker(t, d)
	env := d.workerCommitEnv(w)
	if env == nil {
		t.Fatal("the worker has no commit environment")
	}
	if err := os.WriteFile(filepath.Join(dir, "work.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeHarnessCommit(t, dir, env, "work from the harness")

	for key, want := range wantTrailers(w) {
		if got := trailerValue(t, dir, key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestWorkerCommitRunsProjectHook(t *testing.T) {
	d := commitTestDaemon(t)
	w, dir := commitTestWorker(t, d)
	hooks := filepath.Join(w.mainPath, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	projectHook := filepath.Join(hooks, "prepare-commit-msg")
	if err := os.WriteFile(projectHook, []byte("#!/bin/sh\necho 'Project-Hook-Ran: yes' >> \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := d.workerCommitEnv(w)
	if err := os.WriteFile(filepath.Join(dir, "work.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeHarnessCommit(t, dir, env, "work with a project hook")

	msg := gitWithEnv(t, dir, nil, "log", "-1", "--format=%B")
	if !strings.Contains(msg, "Project-Hook-Ran: yes") {
		t.Errorf("the project's prepare-commit-msg hook did not run:\n%s", msg)
	}
	for key, want := range wantTrailers(w) {
		if got := trailerValue(t, dir, key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestWorkerWithoutWorktreeHasNoCommitHook(t *testing.T) {
	d := commitTestDaemon(t)
	w := &worker{id: protocol.NewID("w"), harness: "codex", sessionID: protocol.NewID("s")}
	if env := d.workerCommitEnv(w); env != nil {
		t.Fatalf("a worker without a worktree got a commit environment: %v", env)
	}
}

func TestWorkerCommitPreCommitHookCanBlock(t *testing.T) {
	d := commitTestDaemon(t)
	w, dir := commitTestWorker(t, d)
	hooks := filepath.Join(w.mainPath, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := d.workerCommitEnv(w)
	if err := os.WriteFile(filepath.Join(dir, "work.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runFakeHarnessCommit(dir, env, "this commit must be blocked"); err == nil {
		t.Fatal("the project's pre-commit hook did not block the worker's commit")
	}
	if n := gitWithEnv(t, dir, nil, "rev-list", "--count", "HEAD"); n != "1" {
		t.Fatalf("the blocked commit was made: %s commits", n)
	}
}

func TestWorkerCommitMsgHookRuns(t *testing.T) {
	d := commitTestDaemon(t)
	w, dir := commitTestWorker(t, d)
	hooks := filepath.Join(w.mainPath, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "commit-msg"), []byte("#!/bin/sh\necho 'Commit-Msg-Ran: yes' >> \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := d.workerCommitEnv(w)
	if err := os.WriteFile(filepath.Join(dir, "work.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeHarnessCommit(t, dir, env, "work with a commit-msg hook")
	msg := gitWithEnv(t, dir, nil, "log", "-1", "--format=%B")
	if !strings.Contains(msg, "Commit-Msg-Ran: yes") {
		t.Errorf("the project's commit-msg hook did not run:\n%s", msg)
	}
}

func TestCommitOutsideWorkerEnvironmentHasNoTrailers(t *testing.T) {
	d := commitTestDaemon(t)
	w, dir := commitTestWorker(t, d)
	_ = w
	if err := os.WriteFile(filepath.Join(dir, "work.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeHarnessCommit(t, dir, nil, "a commit outside the worker environment")

	msg := gitWithEnv(t, dir, nil, "log", "-1", "--format=%B")
	if strings.Contains(msg, "Agent-Net-") {
		t.Errorf("a commit outside the worker environment has trailers:\n%s", msg)
	}
}

func TestWorkerCommitUsesSessionLearnedAfterLaunch(t *testing.T) {
	d := commitTestDaemon(t)
	w, dir := commitTestWorker(t, d)
	w.sessionID = ""
	env := d.workerCommitEnv(w) // a sync start knows no session ID yet
	w.sessionID = protocol.NewID("s")
	d.wmu.Lock()
	d.writeWorkerCommitSession(w)
	d.wmu.Unlock()
	if err := os.WriteFile(filepath.Join(dir, "work.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeHarnessCommit(t, dir, env, "work after the session was registered")
	if got := trailerValue(t, dir, "Agent-Net-Worker"); got != w.sessionID {
		t.Errorf("Agent-Net-Worker = %q, want %q", got, w.sessionID)
	}
}

func TestWorkerCommitAmendKeepsOneSet(t *testing.T) {
	d := commitTestDaemon(t)
	w, dir := commitTestWorker(t, d)
	env := d.workerCommitEnv(w)
	if err := os.WriteFile(filepath.Join(dir, "work.txt"), []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeHarnessCommit(t, dir, env, "first try")

	if err := os.WriteFile(filepath.Join(dir, "work.txt"), []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitWithEnv(t, dir, env, "add", "-A")
	gitWithEnv(t, dir, env, "commit", "-q", "--amend", "--no-edit")

	msg := gitWithEnv(t, dir, nil, "log", "-1", "--format=%B")
	for key := range wantTrailers(w) {
		if n := strings.Count(msg, key+":"); n != 1 {
			t.Errorf("%s appears %d times after an amend, want 1:\n%s", key, n, msg)
		}
	}
}
