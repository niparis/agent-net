package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func envValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return strings.TrimPrefix(env[i], prefix), true
		}
	}
	return "", false
}

func TestTestRunEnvAppendsGoFlags(t *testing.T) {
	t.Setenv("GOFLAGS", "-count=1")
	t.Setenv("GOMAXPROCS", "8")
	env := testRunEnv(5)
	if got, _ := envValue(env, "GOMAXPROCS"); got != "5" {
		t.Errorf("GOMAXPROCS = %q, want 5", got)
	}
	if got, _ := envValue(env, "GOFLAGS"); got != "-count=1 -p=5" {
		t.Errorf("GOFLAGS = %q, want the old value with -p=5 appended", got)
	}
	for _, key := range []string{"GOMAXPROCS=", "GOFLAGS="} {
		count := 0
		for _, e := range env {
			if strings.HasPrefix(e, key) {
				count++
			}
		}
		if count != 1 {
			t.Errorf("%s appears %d times, want one", key, count)
		}
	}
}

func TestTestRunEnvWithoutOldGoFlags(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	os.Unsetenv("GOFLAGS")
	env := testRunEnv(2)
	if got, _ := envValue(env, "GOFLAGS"); got != "-p=2" {
		t.Errorf("GOFLAGS = %q, want -p=2", got)
	}
}

func TestTestRunReturnsTheCommandExitCode(t *testing.T) {
	code, err := runTestCommand(context.Background(), []string{"sh", "-c", "exit 7"}, 1, 5*time.Second, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if code != 7 {
		t.Fatalf("exit code %d, want 7", code)
	}
}

func TestTestRunKillsProcessGroupAtTimeout(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	script := `sleep 30 & echo $! > "$1"; wait`
	var out bytes.Buffer
	start := time.Now()
	code, err := runTestCommand(context.Background(), []string{"sh", "-c", script, "sh", pidFile}, 1, 250*time.Millisecond, &out, &out)
	if err != nil {
		t.Fatal(err)
	}
	if code != testRunTimeoutExit {
		t.Fatalf("exit code %d, want %d (timed out) after %s", code, testRunTimeoutExit, time.Since(start))
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		t.Fatalf("child pid %q: %v", raw, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("child %d of the suite is still alive after the timeout", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
