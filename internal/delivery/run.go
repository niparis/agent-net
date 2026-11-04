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
	"sync/atomic"
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
	// the runner and cannot be overridden. Every value of 8 bytes or more is
	// treated as a secret: an output line that holds it is redacted and is
	// not read as a field. Pass plain values the command must echo back as
	// evidence (a version, a tag) in Argv.
	Env map[string]string
	// Timeout bounds the run. Zero means runDefaultTimeout.
	Timeout time.Duration
	// Commit is the exact commit, a full commit ID (see IsCommitID). HEAD in
	// Dir must be it.
	Commit string
	// GitPath is the PATH list git is found on and runs with (see
	// MergeRequest.GitPath). Empty means this process's PATH. It is not
	// the command's PATH.
	GitPath string
}

// StepRun is the outcome of one run. A command that fails or times out is a
// result, not an error: the caller turns it into failed evidence.
type StepRun struct {
	// ExitCode is the exit code, or -1 when the command was killed.
	ExitCode int
	Duration time.Duration
	// OutputTail is the last 64 KB of stdout and stderr together, starting
	// at a line start, with secret-looking lines, PEM blocks and lines that
	// hold an Env value replaced by "[redacted]".
	OutputTail string
	// OutputDropped is how many bytes of output came before OutputTail.
	OutputDropped int64
	// TimedOut: the runner killed the command because it ran past Timeout.
	// A command that exited by itself is never timed out.
	TimedOut bool
	// Fields are the evidence fields the command reported with
	// "agent-net: key=value" lines, plus url when the output named a GitHub
	// Actions run. A line that would be redacted from the tail is not read,
	// so a field whose name ends in "key", "token" or "secret" is dropped.
	Fields map[string]string
}

// RunStep runs cmd.Argv in cmd.Dir and reports the outcome. It first refuses
// a repository with replace refs or a grafts file (ErrAlteredHistory). It
// then checks that HEAD in Dir is cmd.Commit and refuses with ErrWrongCommit
// otherwise. It then checks that the tree is clean (ignored files do not
// count) and refuses with ErrDirtyTree otherwise.
//
// The command runs in its own process group. On timeout or cancel the whole
// group is killed. When ctx ends, RunStep returns the partial StepRun and
// ctx.Err(); callers should not treat that as a result. A command that cannot
// start returns an error.
func RunStep(ctx context.Context, cmd StepCommand) (StepRun, error) {
	if err := checkStepCommand(cmd); err != nil {
		return StepRun{}, err
	}
	g := gitIn{gitPath: cmd.GitPath, dir: cmd.Dir}
	if err := checkHistory(ctx, g); err != nil {
		return StepRun{}, err
	}
	head, err := revCommit(ctx, g, "HEAD")
	if err != nil {
		return StepRun{}, fmt.Errorf("read HEAD in %s: %w", cmd.Dir, err)
	}
	if head != cmd.Commit {
		return StepRun{}, fmt.Errorf("%w: HEAD is %s, want %s", ErrWrongCommit, head, cmd.Commit)
	}

	if err := runCheckClean(ctx, g); err != nil {
		return StepRun{}, err
	}

	timeout := cmd.Timeout
	if timeout == 0 {
		timeout = runDefaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	red := newRunRedactor(cmd.Env)
	col := newRunCollector(red)
	var killed atomic.Bool
	c := exec.CommandContext(runCtx, cmd.Argv[0], cmd.Argv[1:]...)
	c.Dir = cmd.Dir
	c.Env = runEnv(cmd, head)
	c.Stdout = col
	c.Stderr = col
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		// Called only while the command still runs. Setpgid makes the
		// process leader's pid the group id.
		killed.Store(true)
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
	timedOut := killed.Load() && ctx.Err() == nil && errors.Is(runCtx.Err(), context.DeadlineExceeded)
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
		OutputTail:    red.lines(got.Tail),
		OutputDropped: got.Dropped,
		TimedOut:      timedOut,
		Fields:        got.Fields,
	}
	if err := ctx.Err(); err != nil {
		return run, err
	}
	return run, nil
}

// checkStepCommand validates cmd.
func checkStepCommand(cmd StepCommand) error {
	if cmd.Step != StepRelease && cmd.Step != StepDeploy {
		return fmt.Errorf("step %q: want %q or %q", cmd.Step, StepRelease, StepDeploy)
	}
	if cmd.Dir == "" {
		return errors.New("no working directory")
	}
	if len(cmd.Argv) == 0 || cmd.Argv[0] == "" {
		return errors.New("no command")
	}
	for _, a := range cmd.Argv {
		if strings.ContainsRune(a, 0) {
			return errors.New("command argument has a NUL byte")
		}
	}
	if cmd.Timeout < 0 {
		return fmt.Errorf("negative timeout %v", cmd.Timeout)
	}
	if err := checkCommitID("commit", cmd.Commit); err != nil {
		return err
	}
	for k, v := range cmd.Env {
		if k == "" || strings.ContainsAny(k, "=\x00") || strings.ContainsRune(v, 0) {
			return fmt.Errorf("bad environment entry %q", k)
		}
	}
	return nil
}

// runMaxDirtyListed is how many dirty paths ErrDirtyTree names.
const runMaxDirtyListed = 10

// runCheckClean returns ErrDirtyTree when the directory has changes to
// tracked files or untracked files that are not ignored. Git hooks and the
// fsmonitor hook are off, so the check runs no repo code.
func runCheckClean(ctx context.Context, g gitIn) error {
	out, err := g.bytes(ctx, nil, "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil {
		return fmt.Errorf("check the tree in %s: %w", g.dir, err)
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
// last runTailBytes in a ring and, as the output goes by, reads fields and
// URLs from complete lines, so a field printed early is not lost when the
// tail moves on.
type runCollector struct {
	mu          sync.Mutex
	red         runRedactor
	ring        []byte // runTailBytes long once the first byte arrives
	start       int    // index of the oldest kept byte
	size        int    // how many bytes are kept
	total       int64
	dropped     int64
	lastDropped byte
	line        []byte
	overflow    bool
	inPEM       bool
	fields      map[string]string
	url         string
}

func newRunCollector(red runRedactor) *runCollector {
	return &runCollector{red: red, fields: map[string]string{}}
}

// Write implements io.Writer. It never fails.
func (c *runCollector) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(p)
	c.total += int64(n)
	c.keep(p)

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

// keep adds p to the ring, dropping the oldest bytes when it is full.
func (c *runCollector) keep(p []byte) {
	if len(p) == 0 {
		return
	}
	if c.ring == nil {
		c.ring = make([]byte, runTailBytes)
	}
	if len(p) >= runTailBytes {
		// Everything kept so far, and the start of p, is dropped.
		cut := len(p) - runTailBytes
		if cut > 0 {
			c.lastDropped = p[cut-1]
		} else if c.size > 0 {
			c.lastDropped = c.ring[(c.start+c.size-1)%runTailBytes]
		}
		c.dropped += int64(c.size + cut)
		copy(c.ring, p[cut:])
		c.start, c.size = 0, runTailBytes
		return
	}
	if over := c.size + len(p) - runTailBytes; over > 0 {
		c.lastDropped = c.ring[(c.start+over-1)%runTailBytes]
		c.start = (c.start + over) % runTailBytes
		c.size -= over
		c.dropped += int64(over)
	}
	end := (c.start + c.size) % runTailBytes
	n := copy(c.ring[end:], p)
	copy(c.ring, p[n:])
	c.size += len(p)
}

// tail returns the kept bytes in order.
func (c *runCollector) tail() []byte {
	out := make([]byte, 0, c.size)
	if c.size == 0 {
		return out
	}
	first := c.ring[c.start:min(c.start+c.size, runTailBytes)]
	out = append(out, first...)
	return append(out, c.ring[:c.size-len(first)]...)
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
	tail, dropped := c.tail(), c.dropped
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

// readLine looks at one complete output line, without its newline. Nothing
// is read from a line inside a PEM block or a line that looks like it holds
// a secret.
func (c *runCollector) readLine(line string) {
	line = strings.TrimSuffix(line, "\r")
	begin, end := runPEMBegin(line), runPEMEnd(line)
	if begin {
		c.inPEM = true
	}
	inPEM := c.inPEM
	if end {
		c.inPEM = false
	}
	if inPEM || c.red.secret(line) {
		return
	}
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
	if val == "" || len(val) > runMaxFieldValue {
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

// runSecretRE matches the obvious ways a secret shows up in command output:
// GitHub tokens (ghp_, gho_, ghs_, ghu_, ghr_, github_pat_), API keys
// (sk-...), "token=", "secret:", "key =" and the like, "password=",
// Authorization headers, bearer tokens and AWS_SECRET_ACCESS_KEY. "sk-" must
// start a word, so "task-1" and "disk-usage" stay readable.
var runSecretRE = regexp.MustCompile(`(?i)(?:token|secret|key)\s*[:=]|password\s*[:=]|authorization:|bearer\s|aws_secret_access_key|gh[opsur]_[a-z0-9]|github_pat_|(?:^|[^a-z0-9])sk-`)

// runMinSecretLen is the shortest Env value that is redacted. Shorter values
// ("1", "true", "v2") would redact ordinary output.
const runMinSecretLen = 8

// runRedactor decides which output lines are hidden.
type runRedactor struct {
	// values are exact secret values, from StepCommand.Env.
	values []string
}

func newRunRedactor(env map[string]string) runRedactor {
	var r runRedactor
	for _, v := range env {
		if len(v) >= runMinSecretLen {
			r.values = append(r.values, v)
		}
	}
	sort.Strings(r.values)
	return r
}

// secret reports whether one line looks like it holds a secret.
func (r runRedactor) secret(line string) bool {
	if runSecretRE.MatchString(line) {
		return true
	}
	for _, v := range r.values {
		if strings.Contains(line, v) {
			return true
		}
	}
	return false
}

func runPEMBegin(line string) bool { return strings.Contains(line, "-----BEGIN ") }
func runPEMEnd(line string) bool   { return strings.Contains(line, "-----END ") }

// lines replaces every secret line of s, and every line of a PEM block from
// its BEGIN line to its END line, with "[redacted]". When s starts inside a
// PEM block (its BEGIN line was cut off), the lines up to the END line are
// redacted too.
func (r runRedactor) lines(s string) string {
	lines := strings.Split(s, "\n")
	inPEM := false
	for _, l := range lines {
		if runPEMBegin(l) {
			break
		}
		if runPEMEnd(l) {
			inPEM = true
			break
		}
	}
	for i, l := range lines {
		begin, end := runPEMBegin(l), runPEMEnd(l)
		if begin {
			inPEM = true
		}
		if inPEM || r.secret(l) {
			lines[i] = runRedacted
		}
		if end {
			inPEM = false
		}
	}
	return strings.Join(lines, "\n")
}

// runRedactLines redacts s with no Env values: for text other than a step's
// output, such as git's stderr.
func runRedactLines(s string) string { return runRedactor{}.lines(s) }

// runRedact returns "[redacted]" for a line that looks like it holds a secret.
func runRedact(line string) string { return runRedactor{}.lines(line) }
