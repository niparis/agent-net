package daemon

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// startSleeper runs a process in its own session, like a worker, and reaps it.
func startSleeper(t *testing.T) (pid int, token string) {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go cmd.Wait()
	t.Cleanup(func() { syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	tok, _ := procToken(cmd.Process.Pid)
	return cmd.Process.Pid, tok
}

func TestStopRecordedWorkersKillsRunningWorkersOnly(t *testing.T) {
	dir := t.TempDir()
	livePid, liveTok := startSleeper(t)
	reusedPid, _ := startSleeper(t) // a process that is not the recorded run
	st, err := openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	st.saveWorker(workerRow{ID: "w1", Harness: "opencode", PID: livePid, PGID: livePid, StartToken: liveTok, StartedAt: now, LastActive: now})
	st.saveWorker(workerRow{ID: "w2", Harness: "opencode", PID: reusedPid, PGID: reusedPid, StartToken: "other-run", StartedAt: now, LastActive: now})
	st.close()

	n, err := StopRecordedWorkers(dir, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("stopped %d workers, want 1", n)
	}
	eventually(t, "the worker is gone", func() bool { return !alive(livePid) })
	if !alive(reusedPid) {
		t.Error("killed a process that is not the recorded worker run")
	}
}

func TestStopRecordedWorkersWithNoStateIsFine(t *testing.T) {
	n, err := StopRecordedWorkers(t.TempDir(), time.Second)
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}
