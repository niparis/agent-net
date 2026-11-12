package daemon

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestFakeHarnessDoesNotWriteToPackageDir(t *testing.T) {
	t.Cleanup(func() {
		os.Remove("fake-codex.json")
		os.Remove("fake-pi.json")
		os.Remove("fake-opencode.json")
	})

	t.Run("codex", func(t *testing.T) {
		cmd := exec.Command(os.Args[0], "app-server")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Env = append(os.Environ(), envFakeCodex+"=1")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		_ = cmd.Wait()

		if _, err := os.Stat("fake-codex.json"); !os.IsNotExist(err) {
			t.Fatalf("fake-codex.json was created in package folder: %v", err)
		}
	})

	t.Run("pi", func(t *testing.T) {
		cmd := exec.Command(os.Args[0], "--mode", "rpc")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Env = append(os.Environ(), envFakePi+"=1")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		_ = cmd.Wait()

		if _, err := os.Stat("fake-pi.json"); !os.IsNotExist(err) {
			t.Fatalf("fake-pi.json was created in package folder: %v", err)
		}
	})

	t.Run("opencode", func(t *testing.T) {
		cmd := exec.Command(os.Args[0], "serve")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Env = append(os.Environ(), envFakeOpenCode+"=1", "OPENCODE_SERVER_PASSWORD=test")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		_ = cmd.Wait()

		if _, err := os.Stat("fake-opencode.json"); !os.IsNotExist(err) {
			t.Fatalf("fake-opencode.json was created in package folder: %v", err)
		}
	})
}
