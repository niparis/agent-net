package delivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// This file is the runner for the release and deploy steps. It runs the
// repo's own command for one exact commit and turns the outcome into
// evidence: the exit code, the last part of the output, and a few fields the
// command reported itself. It does not decide whether the step may run: the
// caller checks the requirements (tests, approvals) first.

// Limits of the runner.
const (
	// runDefaultTimeout bounds a command when StepCommand.Timeout is zero.
	runDefaultTimeout = 30 * time.Minute
	// runTailBytes is how much of the output is kept: the last 64 KB.
	runTailBytes = 64 * 1024
	// runMaxFields is the most evidence fields one run can report.
	runMaxFields = 32
	// runMaxFieldValue is the longest field value that is kept.
	runMaxFieldValue = 1024
	// runMaxLine is the longest output line that is looked at for fields
	// and URLs. Longer lines are only kept in the tail.
	runMaxLine = 8 * 1024
	// runWaitDelay is how long to wait for the output pipes after the
	// command exited or was killed, in case a child that left the process
	// group still holds them.
	runWaitDelay = time.Second
	// runFieldPrefix starts an evidence line in the command's output.
	runFieldPrefix = "agent-net: "
	// runRedacted replaces a line that looks like it holds a secret.
	runRedacted = "[redacted]"
)

// Step names the runner accepts.
const (
	StepRelease = "release"
	StepDeploy  = "deploy"
)

// ErrWrongCommit: HEAD in the working directory is not the commit whose
// evidence was checked. The command was not run.
var ErrWrongCommit = errors.New("HEAD is not the commit to run the step for")

// ErrDirtyTree: the working directory has changes to tracked files or
// untracked files that are not ignored. Evidence is keyed by the commit, and a
// dirty tree runs different content than that commit. The command was not run.
var ErrDirtyTree = errors.New("working tree is not clean")

// StepCommand says what to run and for which commit.
type StepCommand struct {
	// Step is "release" or "deploy".
	Step string
	// Dir is the working directory: a checkout of the repository with
	// Commit as HEAD. The command runs here.
	Dir string
	// Argv is the repo's command, with no shell, for example
	// ["scripts/release.sh", "v1.2.3"]. A path with a slash is relative to Dir.
	Argv []string
	// Env is added to the environment. The command inherits only PATH and
	// HOME from the caller. AGENT_NET_STEP and AGENT_NET_COMMIT are set by
	// the runner and cannot be overridden.
	Env map[string]string
	// Timeout bounds the run. Zero means runDefaultTimeout.
	Timeout time.Duration
	// Commit is the exact commit. HEAD in Dir must be it. It may be a full
	// SHA or an abbreviation of at least 7 hex digits.
	Commit string
}

// StepRun is the outcome of one run. A command that fails or times out is a
// result, not an error: the caller turns it into failed evidence.
type StepRun struct {
	// ExitCode is the exit code, or -1 when the command was killed.
	ExitCode int
	Duration time.Duration
	// OutputTail is the last 64 KB of stdout and stderr together, starting
	// at a line start, with secret-looking lines replaced by "[redacted]".
	OutputTail string
	// OutputDropped is how many bytes of output came before OutputTail.
	OutputDropped int64
	// TimedOut: the command was killed because it ran past Timeout.
	TimedOut bool
	// Fields are the evidence fields the command reported with
	// "agent-net: key=value" lines, plus url when the output named a GitHub
	// Actions run.
	Fields map[string]string
}

// RunStep runs cmd.Argv in cmd.Dir and reports the outcome. It first checks
// that HEAD in Dir is cmd.Commit and refuses with ErrWrongCommit otherwise. It
// then checks that the tree is clean (ignored files do not count) and refuses
// with ErrDirtyTree otherwise.
//
// The command runs in its own process group. On timeout or cancel the whole
// group is killed. When ctx ends, RunStep returns the partial StepRun and
// ctx.Err(); callers should not treat that as a result. A command that cannot
// start returns an error.
func RunStep(ctx context.Context, cmd StepCommand) (StepRun, error) {
	want, err := checkStepCommand(cmd)
	if err != nil {
		return StepRun{}, err
	}
	head, err := runHead(ctx, cmd.Dir)
	if err != nil {
		return StepRun{}, err
	}
	if !strings.HasPrefix(head, want) {
		return StepRun{}, fmt.Errorf("%w: HEAD is %s, want %s", ErrWrongCommit, head, cmd.Commit)
	}

	if err := runCheckClean(ctx, cmd.Dir); err != nil {
		return StepRun{}, err
	}

	timeout := cmd.Timeout
	if timeout == 0 {
		timeout = runDefaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	col := newRunCollector()
	c := exec.CommandContext(runCtx, cmd.Argv[0], cmd.Argv[1:]...)
	c.Dir = cmd.Dir
	c.Env = runEnv(cmd, head)
	c.Stdout = col
	c.Stderr = col
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		// Setpgid makes the process leader's pid the group id.
		if err := syscall.Kill(-c.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		return nil
	}
	c.WaitDelay = runWaitDelay

	start := time.Now()
	runErr := c.Run()
	dur := time.Since(start)

	if c.ProcessState == nil {
		if ctx.Err() != nil {
			return StepRun{Duration: dur}, ctx.Err()
		}
		return StepRun{Duration: dur}, fmt.Errorf("start %s: %w", cmd.Argv[0], runErr)
	}
	timedOut := ctx.Err() == nil && errors.Is(runCtx.Err(), context.DeadlineExceeded)
	if runErr != nil && ctx.Err() == nil && !timedOut {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) && !errors.Is(runErr, exec.ErrWaitDelay) {
			return StepRun{Duration: dur}, fmt.Errorf("run %s: %w", cmd.Argv[0], runErr)
		}
	}

	got := col.finish()
	run := StepRun{
		ExitCode:      c.ProcessState.ExitCode(),
		Duration:      dur,
		OutputTail:    runRedactLines(got.Tail),
		OutputDropped: got.Dropped,
		TimedOut:      timedOut,
		Fields:        got.Fields,
	}
	if err := ctx.Err(); err != nil {
		return run, err
	}
	return run, nil
}

var runCommitRE = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// checkStepCommand validates cmd and returns the wanted commit, lower case.
func checkStepCommand(cmd StepCommand) (string, error) {
	if cmd.Step != StepRelease && cmd.Step != StepDeploy {
		return "", fmt.Errorf("step %q: want %q or %q", cmd.Step, StepRelease, StepDeploy)
	}
	if cmd.Dir == "" {
		return "", errors.New("no working directory")
	}
	if len(cmd.Argv) == 0 || cmd.Argv[0] == "" {
		return "", errors.New("no command")
	}
	for _, a := range cmd.Argv {
		if strings.ContainsRune(a, 0) {
			return "", errors.New("command argument has a NUL byte")
		}
	}
	if cmd.Timeout < 0 {
		return "", fmt.Errorf("negative timeout %v", cmd.Timeout)
	}
	want := strings.ToLower(cmd.Commit)
	if !runCommitRE.MatchString(want) {
		return "", fmt.Errorf("commit %q: want 7 to 64 hex digits", cmd.Commit)
	}
	for k, v := range cmd.Env {
		if k == "" || strings.ContainsAny(k, "=\x00") || strings.ContainsRune(v, 0) {
			return "", fmt.Errorf("bad environment entry %q", k)
		}
	}
	return want, nil
}

func runHead(ctx context.Context, dir string) (string, error) {
	out, err := runGit(ctx, dir, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", fmt.Errorf("read HEAD in %s: %w", dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// runMaxDirtyListed is how many dirty paths ErrDirtyTree names.
const runMaxDirtyListed = 10

// runCheckClean returns ErrDirtyTree when dir has changes to tracked files or
// untracked files that are not ignored. Git hooks and the fsmonitor hook are
// off, so the check runs no repo code.
func runCheckClean(ctx context.Context, dir string) error {
	out, err := runGit(ctx, dir,
		"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false",
		"status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil {
		return fmt.Errorf("check the tree in %s: %w", dir, err)
	}
	paths := runDirtyPaths(out)
	if len(paths) == 0 {
		return nil
	}
	shown := paths
	if len(shown) > runMaxDirtyListed {
		shown = shown[:runMaxDirtyListed]
	}
	msg := strings.Join(shown, ", ")
	if rest := len(paths) - len(shown); rest > 0 {
		msg += fmt.Sprintf(" and %d more", rest)
	}
	return fmt.Errorf("%w: %d changed or untracked paths: %s", ErrDirtyTree, len(paths), msg)
}

// runDirtyPaths reads the output of git status --porcelain=v1 -z: entries
// "XY path" ended by NUL. A rename or copy is followed by one more entry, the
// old path, which is skipped.
func runDirtyPaths(out []byte) []string {
	var paths []string
	entries := strings.Split(string(out), "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		paths = append(paths, e[3:])
		if e[0] == 'R' || e[0] == 'C' || e[1] == 'R' || e[1] == 'C' {
			i++
		}
	}
	return paths
}

// runEnv builds the command's environment: PATH and HOME from the caller,
// then cmd.Env, then the two variables the runner owns.
func runEnv(cmd StepCommand, head string) []string {
	var env []string
	for _, k := range []string{"PATH", "HOME"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	keys := make([]string, 0, len(cmd.Env))
	for k := range cmd.Env {
		if k == "AGENT_NET_STEP" || k == "AGENT_NET_COMMIT" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+cmd.Env[k])
	}
	return append(env, "AGENT_NET_STEP="+cmd.Step, "AGENT_NET_COMMIT="+head)
}

// runCollected is what a runCollector learned from the output.
type runCollected struct {
	Fields  map[string]string
	Tail    string // raw, starts at a line start when bytes were dropped
	Dropped int64
}

// runCollector is the one bounded buffer for stdout and stderr. It keeps the
// last runTailBytes and, as the output goes by, reads fields and URLs from
// complete lines, so a field printed early is not lost when the tail moves on.
type runCollector struct {
	mu          sync.Mutex
	buf         []byte
	total       int64
	dropped     int64
	lastDropped byte
	line        []byte
	overflow    bool
	fields      map[string]string
	url         string
}

func newRunCollector() *runCollector {
	return &runCollector{fields: map[string]string{}}
}

// Write implements io.Writer. It never fails.
func (c *runCollector) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(p)
	c.total += int64(n)

	c.buf = append(c.buf, p...)
	if over := len(c.buf) - runTailBytes; over > 0 {
		c.lastDropped = c.buf[over-1]
		c.dropped += int64(over)
		c.buf = append(c.buf[:0], c.buf[over:]...)
	}

	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		seg := p
		if i >= 0 {
			seg = p[:i]
		}
		if !c.overflow {
			if len(c.line)+len(seg) > runMaxLine {
				c.overflow = true
				c.line = c.line[:0]
			} else {
				c.line = append(c.line, seg...)
			}
		}
		if i < 0 {
			break
		}
		c.endLine()
		p = p[i+1:]
	}
	return n, nil
}

func (c *runCollector) endLine() {
	if !c.overflow {
		c.readLine(string(c.line))
	}
	c.line = c.line[:0]
	c.overflow = false
}

// finish ends the output and returns what was collected. Call it once.
func (c *runCollector) finish() runCollected {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.line) > 0 || c.overflow {
		c.endLine()
	}
	fields := make(map[string]string, len(c.fields)+1)
	for k, v := range c.fields {
		fields[k] = v
	}
	if _, ok := fields["url"]; !ok && c.url != "" && len(fields) < runMaxFields {
		fields["url"] = c.url
	}
	tail, dropped := c.buf, c.dropped
	if dropped > 0 && c.lastDropped != '\n' {
		// The cut is in the middle of a line. Drop the rest of that line,
		// so no half of a secret is left.
		if i := bytes.IndexByte(tail, '\n'); i >= 0 {
			tail = tail[i+1:]
			dropped += int64(i + 1)
		}
	}
	return runCollected{Fields: fields, Tail: string(tail), Dropped: dropped}
}

var runKeyRE = regexp.MustCompile(`^[a-z_]+$`)

// readLine looks at one complete output line, without its newline.
func (c *runCollector) readLine(line string) {
	line = strings.TrimSuffix(line, "\r")
	if u := runLastActionsURL(line); u != "" {
		c.url = u
	}
	rest, ok := strings.CutPrefix(line, runFieldPrefix)
	if !ok {
		return
	}
	key, val, ok := strings.Cut(rest, "=")
	if !ok || !runKeyRE.MatchString(key) {
		return
	}
	val = strings.TrimSpace(val)
	if val == "" || len(val) > runMaxFieldValue || runSecretRE.MatchString(line) {
		return
	}
	if _, known := c.fields[key]; !known && len(c.fields) >= runMaxFields {
		return
	}
	c.fields[key] = val
}

var runActionsURLRE = regexp.MustCompile(`https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/actions/runs/[0-9]+`)

// runLastActionsURL returns the last GitHub Actions run URL in line, or "".
func runLastActionsURL(line string) string {
	if !strings.Contains(line, "/actions/runs/") {
		return ""
	}
	last := ""
	for _, loc := range runActionsURLRE.FindAllStringIndex(line, -1) {
		if loc[0] > 0 && runIsWordByte(line[loc[0]-1]) {
			continue
		}
		if loc[1] < len(line) && runIsWordByte(line[loc[1]]) {
			continue // a longer id such as runs/12abc
		}
		last = line[loc[0]:loc[1]]
	}
	return last
}

func runIsWordByte(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b == '_'
}

// runSecretRE matches the obvious ways a secret shows up in command output.
// "sk-" must start a word, so "task-1" and "disk-usage" stay readable.
var runSecretRE = regexp.MustCompile(`(?i)token=|password=|authorization:|ghp_|github_pat_|(?:^|[^a-z0-9])sk-`)

// runRedact returns "[redacted]" for a line that looks like it holds a secret.
func runRedact(line string) string {
	if runSecretRE.MatchString(line) {
		return runRedacted
	}
	return line
}

// runRedactLines applies runRedact to every line of s.
func runRedactLines(s string) string {
	if !runSecretRE.MatchString(s) {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = runRedact(l)
	}
	return strings.Join(lines, "\n")
}
