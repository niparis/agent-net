package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

// The leak check must find a registered fake that is still alive and kill it.
func TestReapFakesKillsALeakedFake(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("sleep", "600")
	cmd.Env = append(os.Environ(), envFakePids+"="+dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	defer cmd.Process.Kill()
	pid := cmd.Process.Pid
	os.WriteFile(filepath.Join(dir, strconv.Itoa(pid)), nil, 0o600)
	// reapFakes reads the child's environment, which is empty until exec ends.
	waitProcessEnv(t, pid, envFakePids+"="+dir)
	got := reapFakes(dir)
	if len(got) != 1 || got[0] != pid {
		t.Fatalf("reapFakes = %v, want [%d]", got, pid)
	}
	<-done
	if again := reapFakes(dir); len(again) != 0 {
		t.Fatalf("still alive after the reap: %v", again)
	}
}
