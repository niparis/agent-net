package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// test-run runs one local test suite through the machine's admission. The
// merger uses it for every suite: the daemon grants one suite at a time per
// machine, and the suite gets the free cores as parallelism, a lowered
// priority and the wall-clock timeout from merger.test_timeout.

// testRunTimeoutExit is the exit code when the timeout killed the suite.
const testRunTimeoutExit = 124

// testSlotPoll is how often a queued suite asks the daemon again.
var testSlotPoll = 2 * time.Second

// exitCodeError makes the process exit with a command's own exit code.
type exitCodeError struct{ code int }

func (e exitCodeError) Error() string { return fmt.Sprintf("exit code %d", e.code) }

func cmdTestRun(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("test-run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	timeout := fs.Duration("timeout", 0, "wall-clock limit (default: merger.test_timeout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	argv := fs.Args()
	if len(argv) == 0 {
		return errors.New("usage: agent-net test-run [--timeout D] -- <command...>")
	}
	cl, err := dialCLI(ctx)
	if err != nil {
		return err
	}
	defer cl.Close()
	cwd, _ := os.Getwd()

	poll := time.NewTicker(testSlotPoll)
	defer poll.Stop()
	var slot protocol.TestSlotResult
	last := 0
	for {
		raw, err := cl.Call(ctx, protocol.OpTestSlot, protocol.TestSlotRequest{Cwd: cwd})
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &slot); err != nil {
			return err
		}
		if slot.Admitted {
			break
		}
		if slot.Position != last {
			last = slot.Position
			fmt.Fprintf(os.Stderr, "test-run: waiting for the test slot on %s, position %d: %s\n", slot.Hostname, slot.Position, slot.Reason)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-poll.C:
		}
	}

	wait := *timeout
	if wait <= 0 {
		wait = time.Duration(slot.TimeoutSeconds) * time.Second
	}
	if wait <= 0 {
		wait = 90 * time.Minute
	}
	fmt.Fprintf(os.Stderr, "test-run: %d parallel on %s, timeout %s\n", slot.Parallel, slot.Hostname, wait)
	code, err := runTestCommand(ctx, argv, slot.Parallel, wait, os.Stdout, os.Stderr)
	if err != nil {
		return fmt.Errorf("cannot run %s: %w", argv[0], err)
	}
	if code == testRunTimeoutExit {
		fmt.Fprintf(os.Stderr, "test-run: killed the process group after %s\n", wait)
	}
	if code != 0 {
		return exitCodeError{code: code}
	}
	return nil
}

// testRunEnv is the suite's environment: GOMAXPROCS and an appended -p in
// GOFLAGS, leaving any GOFLAGS the caller set in place.
func testRunEnv(parallel int) []string {
	if parallel < 1 {
		parallel = 1
	}
	n := strconv.Itoa(parallel)
	flags := os.Getenv("GOFLAGS")
	if flags == "" {
		flags = "-p=" + n
	} else {
		flags += " -p=" + n
	}
	// Replace inherited values instead of adding a second entry: a child's
	// environment lookup may find the first one.
	env := make([]string, 0, len(os.Environ())+2)
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "GOMAXPROCS=") || strings.HasPrefix(e, "GOFLAGS=") {
			continue
		}
		env = append(env, e)
	}
	return append(env, "GOMAXPROCS="+n, "GOFLAGS="+flags)
}

// runTestCommand runs argv in its own process group at a lowered priority,
// with the suite's environment, and kills the whole group at the timeout.
// It returns the command's exit code, or testRunTimeoutExit when the timeout
// fired. An error means the command could not be started.
func runTestCommand(parent context.Context, argv []string, parallel int, timeout time.Duration, stdout, stderr io.Writer) (int, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = testRunEnv(parallel)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		return nil
	}
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		return 1, err
	}
	// Lowered priority where the system allows it. The suite must never fail
	// for this.
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, cmd.Process.Pid, 19)

	err := cmd.Wait()
	if err == nil {
		return 0, nil
	}
	if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil {
		// A child the suite left behind held the output open. The suite's
		// own exit code is known and stands.
		return cmd.ProcessState.ExitCode(), nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && ctx.Err() != context.DeadlineExceeded {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal()), nil
		}
		return exitErr.ExitCode(), nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return testRunTimeoutExit, nil
	}
	return 1, err
}
