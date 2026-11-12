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

// runHandbackCheck runs the project's check for a complete hand-back, or
// returns nil when there is nothing to check. The setting empty records
// not_configured; a hand-back with no commit, or a worker without a worktree,
// records nothing. It never returns an error: a check that cannot run is
// reported as a failed or timed-out result, so the notice is always sent.
func (d *Daemon) runHandbackCheck(ctx context.Context, w *worker) *protocol.HandbackCheck {
	d.wmu.Lock()
	dir, branch, base, project := w.worktree, w.branch, w.base, w.project
	d.wmu.Unlock()
	if dir == "" || branch == "" {
		return nil
	}
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	head, err := gitBranchHead(checkCtx, dir, branch)
	cancel()
	if err != nil {
		d.Logf("worker.handback_check worker=%s error=%q", w.id, err.Error())
		return nil
	}
	if base != "" && head == base {
		return nil // nothing changed: there is no check to run
	}
	command, timeout := d.handbackCheckSettings(project)
	if command == "" {
		return &protocol.HandbackCheck{Status: protocol.HandbackCheckNotConfigured}
	}
	// The timeout covers the queue as well as the command, so a busy machine
	// can never hold the hand-back forever.
	runCtx, cancelRun := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancelRun()
	parallel, slot, ok := d.acquireHandbackCheckSlot(runCtx)
	if !ok {
		return &protocol.HandbackCheck{Status: protocol.HandbackCheckTimedOut, DurationMS: timeout.Milliseconds(),
			OutputTail: "timed out waiting for the machine's test slot"}
	}
	defer d.releaseTestSlot(slot)
	return d.runCheckCommand(runCtx, dir, command, parallel, w.id)
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
	cmd.Env = d.handbackCheckEnv(parallel)
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
		check.Status = protocol.HandbackCheckFailed
		check.ExitCode = 127
		if ctx.Err() != nil {
			check.Status = protocol.HandbackCheckTimedOut
			check.ExitCode = 0
		}
		check.OutputTail = safeCheckOutput(err.Error())
		check.DurationMS = time.Since(started).Milliseconds()
		d.Logf("worker.handback_check worker=%s status=%s error=%q", workerID, check.Status, err.Error())
		return check
	}
	err = cmd.Wait()
	check.DurationMS = time.Since(started).Milliseconds()
	if ctx.Err() != nil {
		check.Status = protocol.HandbackCheckTimedOut
		check.ExitCode = 0
	} else if err != nil {
		check.Status = protocol.HandbackCheckFailed
		check.ExitCode = exitCodeOf(err)
	}
	check.OutputTail = safeCheckOutput(tail.String())
	d.Logf("worker.handback_check worker=%s status=%s exit=%d duration=%s", workerID, check.Status, check.ExitCode, time.Duration(check.DurationMS)*time.Millisecond)
	return check
}

// handbackCheckEnv is the worker's environment plus the values the check needs:
// the shared Go caches and the parallelism the test slot granted.
func (d *Daemon) handbackCheckEnv(parallel int) []string {
	if parallel < 1 {
		parallel = 1
	}
	flags := "-p=" + strconv.Itoa(parallel)
	if old := strings.TrimSpace(os.Getenv("GOFLAGS")); old != "" {
		// The granted -p comes last, so an inherited one cannot win.
		flags = old + " " + flags
	}
	return workerEnv(os.Environ(), map[string]string{
		"GOCACHE":    d.workerGoCacheDir(),
		"GOTMPDIR":   d.workerGoTmpDir(),
		"GOMAXPROCS": strconv.Itoa(parallel),
		"GOFLAGS":    flags,
	})
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
