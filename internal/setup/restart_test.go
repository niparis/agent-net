package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRestartDaemonRunsTheServiceManager(t *testing.T) {
	for _, c := range []struct {
		platform, command, want string
	}{
		{"linux", "systemctl", "--user --no-block restart agent-net-daemon.service"},
		{"darwin", "launchctl", fmt.Sprintf("kickstart -k gui/%d/com.agent-net.daemon", os.Getuid())},
	} {
		t.Run(c.platform, func(t *testing.T) {
			dir := t.TempDir()
			calls := filepath.Join(dir, "calls")
			script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + calls + "'\n"
			if err := os.WriteFile(filepath.Join(dir, c.command), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			t.Setenv("AGENT_NET_NO_SERVICE", "")
			note, err := restartDaemon(c.platform)
			if err != nil {
				t.Fatal(err)
			}
			if note != "daemon restarted" {
				t.Fatalf("note = %q", note)
			}
			raw, err := os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(string(raw)); got != c.want {
				t.Fatalf("call = %q, want %q", got, c.want)
			}
		})
	}
}

func TestRestartDaemonSkipsWithNoService(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + calls + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("AGENT_NET_NO_SERVICE", "1")
	note, err := restartDaemon("linux")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "not restarted") || !strings.Contains(note, "AGENT_NET_NO_SERVICE") {
		t.Fatalf("note = %q", note)
	}
	if _, err := os.Stat(calls); err == nil {
		t.Fatal("ran the service command with AGENT_NET_NO_SERVICE=1")
	}
}
