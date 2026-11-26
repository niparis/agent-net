package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRestartServiceRunsTheServiceManager(t *testing.T) {
	for _, c := range []struct {
		service, platform, command, want string
	}{
		{"daemon", "linux", "systemctl", "--user --no-block restart agent-net-daemon.service"},
		{"daemon", "darwin", "launchctl", fmt.Sprintf("kickstart -k gui/%d/com.agent-net.daemon", os.Getuid())},
		{"gateway", "linux", "systemctl", "--user --no-block restart agent-net-gateway.service"},
		{"gateway", "darwin", "launchctl", fmt.Sprintf("kickstart -k gui/%d/com.agent-net.gateway", os.Getuid())},
	} {
		t.Run(c.service+"/"+c.platform, func(t *testing.T) {
			calls := fakeServiceManager(t, c.command)
			t.Setenv("AGENT_NET_NO_SERVICE", "")
			note, err := restartService(c.service, c.platform)
			if err != nil {
				t.Fatal(err)
			}
			if note != c.service+" restarted" {
				t.Fatalf("note = %q", note)
			}
			if got := readCalls(t, calls); got != c.want {
				t.Fatalf("call = %q, want %q", got, c.want)
			}
		})
	}
}

func TestRestartServiceSkipsWithNoService(t *testing.T) {
	for _, service := range []string{"daemon", "gateway"} {
		t.Run(service, func(t *testing.T) {
			calls := fakeServiceManager(t, "systemctl")
			t.Setenv("AGENT_NET_NO_SERVICE", "1")
			note, err := restartService(service, "linux")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(note, service+" not restarted") || !strings.Contains(note, "AGENT_NET_NO_SERVICE") {
				t.Fatalf("note = %q", note)
			}
			if _, err := os.Stat(calls); err == nil {
				t.Fatal("ran the service command with AGENT_NET_NO_SERVICE=1")
			}
		})
	}
}

// fakeServiceManager puts a command that records its arguments on PATH and
// returns the file it writes them to.
func fakeServiceManager(t *testing.T, command string) string {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + calls + "'\n"
	if err := os.WriteFile(filepath.Join(dir, command), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return calls
}

func readCalls(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(raw))
}
