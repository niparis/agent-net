package delivery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestIsCommitID(t *testing.T) {
	cases := map[string]bool{
		strings.Repeat("a", 40):                    true,
		strings.Repeat("0", 64):                    true,
		"0123456789abcdef0123456789abcdef01234567": true,
		strings.Repeat("a", 39):                    false,
		strings.Repeat("a", 41):                    false,
		strings.Repeat("a", 63):                    false,
		strings.Repeat("A", 40):                    false, // upper case is not the form git prints
		"0123456":                                  false, // no abbreviations
		"main":                                     false,
		"HEAD":                                     false,
		"--all":                                    false,
		"":                                         false,
		strings.Repeat("a", 40) + "\n":             false,
		strings.Repeat("g", 40):                    false,
	}
	for s, want := range cases {
		if got := IsCommitID(s); got != want {
			t.Errorf("IsCommitID(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestGitEnv(t *testing.T) {
	inherited := []string{
		"HOME=/home/x",
		"PATH=/usr/bin",
		"LC_ALL=fr_FR.UTF-8",
		"GIT_DIR=/elsewhere/.git",
		"GIT_WORK_TREE=/elsewhere",
		"GIT_INDEX_FILE=/elsewhere/index",
		"GIT_OBJECT_DIRECTORY=/elsewhere/objects",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=/elsewhere/objects",
		"GIT_COMMON_DIR=/elsewhere/.git",
		"GIT_REPLACE_REF_BASE=refs/other/",
		"GIT_NO_REPLACE_OBJECTS=0",
		"GIT_CONFIG_PARAMETERS='core.hookspath'='/x'",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.hooksPath",
		"GIT_CONFIG_VALUE_0=/x",
		"GIT_AUTHOR_NAME=Intruder",
		"GIT_EXEC_PATH=/evil",
		"GIT_SSH_COMMAND=ssh -i key",
		"GIT_CONFIG_GLOBAL=/home/x/.gitconfig",
		"GIT_CONFIG_NOSYSTEM=1",
		"SSH_AUTH_SOCK=/run/agent",
	}
	env := gitEnv(inherited, "")
	for _, want := range []string{
		"HOME=/home/x", "PATH=/usr/bin", "SSH_AUTH_SOCK=/run/agent",
		"GIT_SSH_COMMAND=ssh -i key", "GIT_CONFIG_GLOBAL=/home/x/.gitconfig", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_NO_REPLACE_OBJECTS=1", "LC_ALL=C", "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true",
	} {
		if !slices.Contains(env, want) {
			t.Errorf("env lacks %q: %v", want, env)
		}
	}
	seen := map[string]int{}
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		seen[k]++
	}
	for _, k := range []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR", "GIT_REPLACE_REF_BASE",
		"GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0",
		"GIT_AUTHOR_NAME", "GIT_EXEC_PATH",
	} {
		if seen[k] != 0 {
			t.Errorf("env keeps %s: %v", k, env)
		}
	}
	for k, n := range seen {
		if n > 1 {
			t.Errorf("%s is set %d times: %v", k, n, env)
		}
	}

	env = gitEnv(inherited, "/opt/git/bin:/usr/bin")
	if !slices.Contains(env, "PATH=/opt/git/bin:/usr/bin") || slices.Contains(env, "PATH=/usr/bin") {
		t.Errorf("GitPath does not replace PATH: %v", env)
	}
}

func TestGitIgnoresInheritedRepoVariables(t *testing.T) {
	r := newTestRepo(t, "main")
	head := r.commit("mine")
	other := newTestRepo(t, "main")
	other.commit("other")
	t.Setenv("GIT_DIR", filepath.Join(other.dir, ".git"))
	t.Setenv("GIT_WORK_TREE", other.dir)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other.dir, ".git", "index"))

	got, err := gitIn{dir: r.dir}.run(context.Background(), "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if got != head {
		t.Fatalf("HEAD = %s, want %s: an inherited GIT_DIR was followed", got, head)
	}
}

// fakeGit writes an executable named git into a new directory and returns
// the directory.
func fakeGit(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestGitPathFindsGit(t *testing.T) {
	dir := fakeGit(t, `echo "fake $PATH $*"`+"\n")
	gitPath := dir + string(os.PathListSeparator) + "/usr/bin:/bin"
	got, err := gitIn{gitPath: gitPath, dir: t.TempDir()}.run(context.Background(), "version")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "fake "+gitPath+" ") || !strings.HasSuffix(got, " version") {
		t.Fatalf("output = %q: git was not taken from GitPath, or PATH is not GitPath", got)
	}
	if !strings.Contains(got, "core.hooksPath=/dev/null") || !strings.Contains(got, "core.fsmonitor=false") {
		t.Fatalf("fixed flags missing: %q", got)
	}

	_, err = gitIn{gitPath: t.TempDir(), dir: t.TempDir()}.run(context.Background(), "version")
	var ge *GitError
	if !errors.As(err, &ge) || ge.ExitCode != -1 || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want git not found", err)
	}
}

func TestGitCallTimeoutKillsGitAndChildren(t *testing.T) {
	// The shell waits for a child that holds stdout open, so only killing
	// the process group (or WaitDelay) ends the call.
	dir := fakeGit(t, "sleep 30\necho done\n")
	old := gitCallTimeout
	gitCallTimeout = 300 * time.Millisecond
	defer func() { gitCallTimeout = old }()

	start := time.Now()
	_, err := gitIn{gitPath: dir + ":/usr/bin:/bin", dir: t.TempDir()}.run(context.Background(), "fetch")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want a deadline error", err)
	}
	var ge *GitError
	if !errors.As(err, &ge) || ge.ExitCode != -1 {
		t.Fatalf("err = %#v, want ExitCode -1", err)
	}
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Fatalf("call took %v", d)
	}

	// The caller's context still wins when it ends first.
	gitCallTimeout = time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start = time.Now()
	if _, err := (gitIn{gitPath: dir + ":/usr/bin:/bin", dir: t.TempDir()}).run(ctx, "fetch"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Fatalf("call took %v", d)
	}
}

func TestGitErrorCarriesExitCode(t *testing.T) {
	r := newTestRepo(t, "main")
	r.commit("a")
	g := gitIn{dir: r.dir}
	_, err := revCommit(context.Background(), g, "refs/heads/nope")
	if !isExit(err, 1) {
		t.Fatalf("err = %#v, want exit code 1", err)
	}
	_, err = g.run(context.Background(), "cat-file", "-t", strings.Repeat("d", 40))
	var ge *GitError
	if !errors.As(err, &ge) || ge.ExitCode != 128 {
		t.Fatalf("err = %#v, want exit code 128", err)
	}
	if ge.Stderr == "" || !strings.Contains(err.Error(), ge.Stderr) {
		t.Fatalf("stderr not in the error: %v", err)
	}
}

func TestCheckHistoryRefusesGrafts(t *testing.T) {
	r := newTestRepo(t, "main")
	r.commit("a")
	head := r.commit("b")
	g := gitIn{dir: r.dir}
	if err := checkHistory(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.dir, ".git", "info", "grafts"), []byte(head+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkHistory(context.Background(), g); !errors.Is(err, ErrAlteredHistory) {
		t.Fatalf("err = %v, want ErrAlteredHistory", err)
	}
}
