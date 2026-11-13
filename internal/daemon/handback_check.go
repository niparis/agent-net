package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// The project's pre-hand-back check (owner decision 2026-11-12). The daemon
// runs delivery.handback_check in the worker's worktree after the hand-back
// commit and before the change is recorded. It is one shell command line, it
// is bounded by delivery.handback_check_timeout, and it takes the machine's
// test slot like a merger's suite. Its result goes on the change record and in
// the notice; a failure raises a change signal, never a gate.

// handbackCheckTimeoutDefault is used when delivery.handback_check_timeout has
// no usable value.
const handbackCheckTimeoutDefault = 30 * time.Minute

// handbackCheckSlotPoll is how often a waiting check asks for the machine's
// test slot. The slot itself is the same one merger suites use.
const handbackCheckSlotPoll = 200 * time.Millisecond

// handbackCheckSettings resolves the command and timeout for a project.
func (d *Daemon) handbackCheckSettings(repo string) (command string, timeout time.Duration) {
	snap := d.SettingsSnapshot()
	where := settings.Where{Repo: repo}
	if e, err := snap.Resolve("delivery.handback_check", where); err == nil {
		var text string
		if json.Unmarshal(e.Value, &text) == nil {
			command = strings.TrimSpace(text)
		}
	}
	timeout = handbackCheckTimeoutDefault
	if e, err := snap.Resolve("delivery.handback_check_timeout", where); err == nil {
		var text string
		if json.Unmarshal(e.Value, &text) == nil {
			if parsed, err := time.ParseDuration(text); err == nil && parsed > 0 {
				timeout = parsed
			}
		}
	}
	return command, timeout
}

// handbackCheckPlan is what a complete hand-back has to check: nothing, a
// recorded result that needs no run (not configured), or a command to run on
// one head commit.
type handbackCheckPlan struct {
	result  *protocol.HandbackCheck // set when there is nothing to run
	dir     string
	head    string
	command string
	timeout time.Duration
	worker  string
}

// planHandbackCheck decides what the check for a complete hand-back is. The
// setting empty records not_configured; a hand-back with no commit, or a worker
// without a worktree, records nothing (a nil plan result and no command). It
// never returns an error: a problem is logged and means no check.
func (d *Daemon) planHandbackCheck(ctx context.Context, w *worker) handbackCheckPlan {
	d.wmu.Lock()
	dir, branch, base, project := w.worktree, w.branch, w.base, w.project
	d.wmu.Unlock()
	if dir == "" || branch == "" {
		return handbackCheckPlan{}
	}
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	head, err := gitBranchHead(checkCtx, dir, branch)
	cancel()
	if err != nil {
		d.Logf("worker.handback_check worker=%s error=%q", w.id, err.Error())
		return handbackCheckPlan{}
	}
	if base != "" && head == base {
		return handbackCheckPlan{} // nothing changed: there is no check to run
	}
	command, timeout := d.handbackCheckSettings(project)
	if command == "" {
		return handbackCheckPlan{result: &protocol.HandbackCheck{Status: protocol.HandbackCheckNotConfigured}}
	}
	return handbackCheckPlan{dir: dir, head: head, command: command, timeout: timeout, worker: w.id}
}

// runs is true when the plan has a command to run.
func (p handbackCheckPlan) runs() bool { return p.command != "" }

// runHandbackCheckPlan runs the command for the plan and returns its result.
// run, when set, is marked running once the check has the test slot. The
// timeout covers the command only: it starts when the slot is granted, so a
// busy machine cannot time out a check that never ran. The queue wait is
// reported in QueueWaitMS. It never returns an error: a check that cannot run
// is reported as a failed or timed-out result, so the notice is always sent.
func (d *Daemon) runHandbackCheckPlan(ctx context.Context, p handbackCheckPlan, run *handbackCheckRun) *protocol.HandbackCheck {
	waitCtx, cancelWait := context.WithCancel(ctx)
	defer cancelWait()
	if d.checksDone != nil {
		// The caller's context outlives the request on purpose, so only this
		// ends the check when the daemon stops.
		defer context.AfterFunc(d.checksDone, cancelWait)()
	}
	queued := time.Now()
	parallel, slot, ok := d.acquireHandbackCheckSlot(waitCtx)
	wait := time.Since(queued)
	if !ok {
		if d.checksStopped() {
			return d.interruptedCheck(p.worker, queued, "")
		}
		return &protocol.HandbackCheck{Status: protocol.HandbackCheckTimedOut, QueueWaitMS: wait.Milliseconds(),
			OutputTail: "stopped while waiting for the machine's test slot"}
	}
	defer d.releaseTestSlot(slot)
	if run != nil {
		run.running.Store(true)
	}
	runCtx, cancelRun := context.WithTimeout(waitCtx, p.timeout)
	defer cancelRun()
	check := d.runCheckCommand(runCtx, p.dir, p.command, parallel, p.worker)
	check.QueueWaitMS = wait.Milliseconds()
	if check.Status == protocol.HandbackCheckTimedOut && d.checksStopped() {
		return d.interruptedCheck(p.worker, queued, check.OutputTail)
	}
	return check
}

// checksStopped is true once Stop has begun to stop the running checks.
func (d *Daemon) checksStopped() bool {
	return d.checksDone != nil && d.checksDone.Err() != nil
}

// interruptedCheck is the result of a check the daemon stopped (a restart or
// an upgrade). It proves nothing about the code, so it is recorded as a check
// that could not run, which every gateway version accepts, and raises no
// signal.
func (d *Daemon) interruptedCheck(workerID string, started time.Time, tail string) *protocol.HandbackCheck {
	check := &protocol.HandbackCheck{Status: protocol.HandbackCheckCouldNotRun,
		Detail:     "interrupted: the daemon stopped before the check ended",
		DurationMS: time.Since(started).Milliseconds(), OutputTail: tail}
	d.Logf("worker.handback_check worker=%s status=%s interrupted=1 duration=%s", workerID, check.Status, time.Duration(check.DurationMS)*time.Millisecond)
	return check
}

// acquireHandbackCheckSlot waits in the machine's test queue for an in-process
// suite. It returns the granted parallelism and the queue place to release. A
// hand-back check is not a worker start, so the start gap does not apply; its
// slot still counts as load for worker admission.
func (d *Daemon) acquireHandbackCheckSlot(ctx context.Context) (int, *localConn, bool) {
	// The slot and the queue are keyed by connection. An in-process check has
	// no connection, so it uses an empty one that nobody reads or writes.
	lc := &localConn{}
	ticker := time.NewTicker(handbackCheckSlotPoll)
	defer ticker.Stop()
	for {
		r := d.acquireTestSlotNoGap(lc, "")
		if r.Admitted {
			return r.Parallel, lc, true
		}
		select {
		case <-ctx.Done():
			d.releaseTestSlot(lc)
			return 0, nil, false
		case <-ticker.C:
		}
	}
}

// runCheckCommand runs the check with the same environment a worker had, in
// its own process group, at nice 19. The output keeps its last few KiB.
func (d *Daemon) runCheckCommand(ctx context.Context, dir, command string, parallel int, workerID string) *protocol.HandbackCheck {
	bin, args := "nice", []string{"-n", "19", "sh", "-c", command}
	if _, err := exec.LookPath("nice"); err != nil {
		bin, args = "sh", []string{"-c", command}
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = d.handbackCheckEnv(ctx, dir, parallel)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Kill the whole group, not only nice, when the timeout expires.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	tail := &tailBuffer{max: protocol.MaxHandbackCheckOutputBytes}
	cmd.Stdout, cmd.Stderr = tail, tail
	started := time.Now()
	check := &protocol.HandbackCheck{Status: protocol.HandbackCheckPassed}
	err := cmd.Start()
	if err != nil {
		if ctx.Err() != nil {
			check.Status = protocol.HandbackCheckTimedOut
		} else {
			// The command never ran, so this proves nothing about the code.
			check.Status = protocol.HandbackCheckCouldNotRun
			check.Detail = oneLine(err.Error())
		}
		check.OutputTail = safeCheckOutput(err.Error())
		check.DurationMS = time.Since(started).Milliseconds()
		d.Logf("worker.handback_check worker=%s status=%s error=%q", workerID, check.Status, err.Error())
		return check
	}
	err = cmd.Wait()
	check.DurationMS = time.Since(started).Milliseconds()
	check.OutputTail = safeCheckOutput(tail.String())
	if ctx.Err() != nil {
		check.Status = protocol.HandbackCheckTimedOut
		check.ExitCode = 0
	} else if err != nil {
		check.ExitCode = exitCodeOf(err)
		check.Status = protocol.HandbackCheckFailed
		if check.ExitCode == 127 {
			// The shell could not find a command the check needs. That is not
			// a failing test, and the notice says which tool is missing.
			check.Status = protocol.HandbackCheckCouldNotRun
			check.Detail = missingToolDetail(check.OutputTail)
			check.ExitCode = 0
		}
	}
	d.Logf("worker.handback_check worker=%s status=%s exit=%d duration=%s", workerID, check.Status, check.ExitCode, time.Duration(check.DurationMS)*time.Millisecond)
	return check
}

// handbackCheckEnv is the worker's environment plus the values the check needs:
// the PATH where the worker finds go and gofmt, the shared Go caches and the
// parallelism the test slot granted.
func (d *Daemon) handbackCheckEnv(ctx context.Context, dir string, parallel int) []string {
	if parallel < 1 {
		parallel = 1
	}
	flags := "-p=" + strconv.Itoa(parallel)
	if old := strings.TrimSpace(os.Getenv("GOFLAGS")); old != "" {
		// The granted -p comes last, so an inherited one cannot win.
		flags = old + " " + flags
	}
	env := os.Environ()
	set := map[string]string{
		"GOCACHE":    d.workerGoCacheDir(),
		"GOTMPDIR":   d.workerGoTmpDir(),
		"GOMAXPROCS": strconv.Itoa(parallel),
		"GOFLAGS":    flags,
	}
	base := workerEnv(env, set)
	if toolchain := d.workerToolchainPATH(ctx, dir, base); toolchain != "" {
		// The toolchain comes first so a wrong go on the daemon's PATH cannot
		// win over the one the worker would use.
		if path := envValue(base, "PATH"); path != "" {
			toolchain += string(os.PathListSeparator) + path
		}
		set["PATH"] = toolchain
	}
	return workerEnv(env, set)
}

// envValue is one variable's value in a KEY=value list.
func envValue(env []string, key string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v
		}
	}
	return ""
}

// missingToolDetail turns a shell's "not found" line into the one-line reason
// the notice shows, for example "gofmt not found".
func missingToolDetail(out string) string {
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			line = t
		}
	}
	for _, suffix := range []string{": command not found", ": not found"} {
		prefix, ok := strings.CutSuffix(line, suffix)
		if !ok {
			continue
		}
		tool := prefix
		if i := strings.LastIndex(tool, ":"); i >= 0 {
			tool = tool[i+1:]
		}
		if tool = strings.TrimSpace(tool); tool != "" {
			return tool + " not found"
		}
	}
	return "a command the check needs is missing"
}

// oneLine makes an error text fit the notice's single line for a check that
// could not run.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > protocol.MaxHandbackCheckDetailBytes {
		s = s[:protocol.MaxHandbackCheckDetailBytes]
		for len(s) > 0 && !utf8.RuneStart(s[len(s)-1]) {
			s = s[:len(s)-1]
		}
	}
	if s == "" {
		return "the command did not start"
	}
	return s
}

// exitCodeOf is the process exit status, or -1 when none is available.
func exitCodeOf(err error) int {
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode()
	}
	return -1
}

// safeCheckOutput keeps output valid for JSON and notes: UTF-8, no terminal
// controls beyond tab and newline, at most the configured tail.
func safeCheckOutput(s string) string {
	s = strings.ToValidUTF8(s, "?")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || (r >= 0x20 && r != 0x7f && !(r >= 0x80 && r < 0xa0)) {
			return r
		}
		return ' '
	}, s)
	return s
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.max <= 0 {
		return len(p), nil
	}
	if len(p) >= b.max {
		b.buf = append(b.buf[:0], p[len(p)-b.max:]...)
	} else {
		if over := len(b.buf) + len(p) - b.max; over > 0 {
			b.buf = b.buf[:copy(b.buf, b.buf[over:])]
		}
		b.buf = append(b.buf, p...)
	}
	// Start at a rune boundary so the kept text is valid UTF-8.
	for len(b.buf) > 0 && !utf8.RuneStart(b.buf[0]) {
		b.buf = b.buf[1:]
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
