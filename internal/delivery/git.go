package delivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// This file is the one way the package runs git. Every call:
//
//   - runs the git binary found on GitPath (or on this process's PATH), with an
//     explicit argument list and no shell;
//   - switches off hooks, the fsmonitor hook, automatic gc and maintenance;
//   - runs only in a repository whose own config names no program and no
//     rewrite of the remote (checkRepo, in config.go);
//   - ignores replace refs (GIT_NO_REPLACE_OBJECTS=1), so a commit ID always
//     names the content that was checked;
//   - drops inherited GIT_* variables, except a few that say how to reach a
//     remote and where the user's global config is, so nothing points git at
//     another repository or changes what it reads;
//   - never prompts and never opens an editor;
//   - is bounded by the caller's context and gitCallTimeout, whichever ends
//     first, and kills git's whole process group when that ends.

// gitCallTimeout bounds one git call when the caller's context ends later. A
// variable so tests can shorten it.
var gitCallTimeout = 2 * time.Minute

const (
	// gitWaitDelay is how long to wait for git's output pipes after it
	// exited or was killed.
	gitWaitDelay = 2 * time.Second
	// gitMaxStderr is the most stderr a GitError keeps.
	gitMaxStderr = 4 << 10
)

// gitFixedArgs come before the command of every call.
var gitFixedArgs = []string{
	"--no-pager",
	"--literal-pathspecs",
	"-c", "core.hooksPath=/dev/null",
	"-c", "core.fsmonitor=false",
	"-c", "gc.auto=0",
	"-c", "maintenance.auto=false",
	// Command line config beats every file, so this one cannot be undone by
	// the repository: the ext:: transport runs a program named in the URL.
	"-c", "protocol.ext.allow=never",
}

// gitEnvKeep lists the GIT_* variables a call inherits: how to reach a remote
// and where the user's global and system config is (credential helpers and
// URL rewrites live there). Every other GIT_* variable is dropped, among them
// GIT_DIR, GIT_WORK_TREE, GIT_INDEX_FILE, GIT_OBJECT_DIRECTORY,
// GIT_ALTERNATE_OBJECT_DIRECTORIES, GIT_COMMON_DIR, GIT_REPLACE_REF_BASE,
// GIT_CONFIG_PARAMETERS, GIT_CONFIG_COUNT and the author and committer
// variables.
var gitEnvKeep = map[string]bool{
	"GIT_SSH":             true,
	"GIT_SSH_COMMAND":     true,
	"GIT_SSH_VARIANT":     true,
	"GIT_ASKPASS":         true,
	"GIT_PROXY_COMMAND":   true,
	"GIT_CONFIG_GLOBAL":   true,
	"GIT_CONFIG_SYSTEM":   true,
	"GIT_CONFIG_NOSYSTEM": true,
}

// gitEnvSet is added to the environment of every call.
var gitEnvSet = []string{
	"GIT_NO_REPLACE_OBJECTS=1",
	"GIT_TERMINAL_PROMPT=0",
	"GIT_EDITOR=true",
	"GIT_MERGE_AUTOEDIT=no",
	"GIT_OPTIONAL_LOCKS=0",
	"LC_ALL=C",
}

// GitError is a git call that failed.
type GitError struct {
	// Args are the arguments after the fixed flags.
	Args []string
	// ExitCode is git's exit code, or -1 when git did not exit by itself
	// (it could not start, or it was killed when the context ended).
	ExitCode int
	// Stderr is the end of git's stderr, trimmed, with secret-looking lines
	// redacted.
	Stderr string
	Err    error
}

func (e *GitError) Error() string {
	cmd := strings.Join(e.Args, " ")
	if len(cmd) > 200 {
		cmd = cmd[:200] + "..."
	}
	if e.Stderr != "" {
		return fmt.Sprintf("git %s: %v: %s", cmd, e.Err, e.Stderr)
	}
	return fmt.Sprintf("git %s: %v", cmd, e.Err)
}

func (e *GitError) Unwrap() error { return e.Err }

// isExit reports whether err is a git call that exited with code. Exit code 1
// is how many git commands say "no" (a ref that does not exist, a commit that
// is not an ancestor), as opposed to "broken".
func isExit(err error, code int) bool {
	var ge *GitError
	return errors.As(err, &ge) && ge.ExitCode == code
}

// gitIn runs git in one directory.
type gitIn struct {
	// gitPath is the PATH list git is found on and runs with. Empty means
	// this process's PATH.
	gitPath string
	dir     string
}

// run runs git and returns its stdout without surrounding white space.
func (g gitIn) run(ctx context.Context, args ...string) (string, error) {
	out, err := g.bytes(ctx, nil, args...)
	return strings.TrimSpace(string(out)), err
}

// bytes runs git with stdin (nil for none) and returns its raw stdout.
func (g gitIn) bytes(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	bin, err := gitBinary(g.gitPath)
	if err != nil {
		return nil, &GitError{Args: args, ExitCode: -1, Err: err}
	}
	ctx, cancel := context.WithTimeout(ctx, gitCallTimeout)
	defer cancel()

	full := make([]string, 0, len(gitFixedArgs)+len(args))
	full = append(append(full, gitFixedArgs...), args...)
	cmd := exec.CommandContext(ctx, bin, full...)
	cmd.Dir = g.dir
	cmd.Env = gitEnv(os.Environ(), g.gitPath)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout bytes.Buffer
	stderr := &capWriter{max: 64 << 10}
	cmd.Stdout = &stdout
	cmd.Stderr = stderr
	// git starts other programs (ssh, credential helpers, git-lfs). Its own
	// process group lets a timeout kill all of them.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		return nil
	}
	cmd.WaitDelay = gitWaitDelay

	if err := cmd.Run(); err != nil {
		code := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = fmt.Errorf("%w (%v)", ctxErr, err)
			code = -1
		}
		return stdout.Bytes(), &GitError{Args: args, ExitCode: code, Stderr: gitStderr(stderr.buf.String()), Err: err}
	}
	return stdout.Bytes(), nil
}

// gitBinary finds git: the first executable named git on gitPath, or git on
// this process's PATH when gitPath is empty.
func gitBinary(gitPath string) (string, error) {
	if gitPath == "" {
		return exec.LookPath("git")
	}
	for _, dir := range filepath.SplitList(gitPath) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		p := filepath.Join(dir, "git")
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("git not found on GitPath %q", gitPath)
}

// gitEnv builds the environment of a git call from the inherited one.
func gitEnv(inherited []string, gitPath string) []string {
	env := make([]string, 0, len(inherited)+len(gitEnvSet)+1)
	for _, kv := range inherited {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "GIT_") && !gitEnvKeep[k] {
			continue
		}
		if k == "LC_ALL" || (gitPath != "" && k == "PATH") {
			continue
		}
		env = append(env, kv)
	}
	if gitPath != "" {
		env = append(env, "PATH="+gitPath)
	}
	return append(env, gitEnvSet...)
}

// gitStderr trims git's stderr, redacts lines that look like secrets and
// keeps the last gitMaxStderr bytes.
func gitStderr(s string) string {
	s = strings.TrimSpace(runRedactLines(s))
	if len(s) > gitMaxStderr {
		s = s[len(s)-gitMaxStderr:]
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
	}
	return s
}

// capWriter keeps the first max bytes written to it and drops the rest.
type capWriter struct {
	buf bytes.Buffer
	max int
}

func (w *capWriter) Write(p []byte) (int, error) {
	if room := w.max - w.buf.Len(); room > 0 {
		if len(p) > room {
			w.buf.Write(p[:room])
		} else {
			w.buf.Write(p)
		}
	}
	return len(p), nil
}

// ---- commit identity ----

var commitIDRE = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// IsCommitID reports whether s is a full commit ID: 40 (SHA-1) or 64
// (SHA-256) lowercase hex digits. The package names commits only this way:
// never by a branch name, an abbreviation or a prefix.
func IsCommitID(s string) bool { return commitIDRE.MatchString(s) }

func checkCommitID(what, s string) error {
	if !IsCommitID(s) {
		return fmt.Errorf("%s %q is not a full commit ID (40 or 64 lowercase hex digits)", what, s)
	}
	return nil
}

// ---- checks shared by the steps ----

// ErrAlteredHistory: the repository has replace refs or a grafts file, so git
// could show other content or other parents than the commit IDs name. Every
// call ignores replace refs, but a repository where someone created one is
// refused: it is a sign of tampering, and grafts are not switched off by
// GIT_NO_REPLACE_OBJECTS.
var ErrAlteredHistory = errors.New("repository has replace refs or a grafts file")

// checkHistory refuses a repository with replace refs or a grafts file.
// refs/replace and info/grafts are shared by every worktree of a repository.
func checkHistory(ctx context.Context, g gitIn) error {
	refs, err := g.run(ctx, "for-each-ref", "--count=5", "--format=%(refname)", "refs/replace/")
	if err != nil {
		return fmt.Errorf("list replace refs in %s: %w", g.dir, err)
	}
	if refs != "" {
		return fmt.Errorf("%w: %s has %s", ErrAlteredHistory, g.dir, strings.ReplaceAll(refs, "\n", ", "))
	}
	grafts, err := g.run(ctx, "rev-parse", "--path-format=absolute", "--git-path", "info/grafts")
	if err != nil {
		return fmt.Errorf("find the grafts file of %s: %w", g.dir, err)
	}
	if _, err := os.Lstat(grafts); err == nil {
		return fmt.Errorf("%w: %s exists", ErrAlteredHistory, grafts)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// checkRefName refuses a branch or remote name that git does not accept in a
// ref, or that it could read as an option.
func checkRefName(ctx context.Context, g gitIn, what, name string) error {
	if name == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, "\x00\n") {
		return fmt.Errorf("bad %s %q", what, name)
	}
	if _, err := g.run(ctx, "check-ref-format", "refs/heads/"+name); err != nil {
		if isExit(err, 1) {
			return fmt.Errorf("bad %s %q", what, name)
		}
		return err
	}
	return nil
}

// fetchRefspec fetches one refspec from remote. It never fetches tags or
// submodules and does not write FETCH_HEAD.
func fetchRefspec(ctx context.Context, g gitIn, remote, refspec string) error {
	if _, err := g.run(ctx, "fetch", "--no-tags", "--no-recurse-submodules", "--no-write-fetch-head", "--quiet",
		remote, refspec); err != nil {
		return fmt.Errorf("fetch %s: %w", remote, err)
	}
	return nil
}

// revCommit resolves a full ref name or commit ID to a commit ID. A ref that
// does not exist gives a GitError with exit code 1.
func revCommit(ctx context.Context, g gitIn, rev string) (string, error) {
	return g.run(ctx, "rev-parse", "--verify", "--quiet", "--end-of-options", rev+"^{commit}")
}

// isAncestor reports whether commit a is an ancestor of (or equal to) b.
func isAncestor(ctx context.Context, g gitIn, a, b string) (bool, error) {
	_, err := g.run(ctx, "merge-base", "--is-ancestor", a, b)
	if err == nil {
		return true, nil
	}
	if isExit(err, 1) {
		return false, nil
	}
	return false, err
}
