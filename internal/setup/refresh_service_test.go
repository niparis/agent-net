package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRefreshDaemonServiceEnvironmentReload(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			dir := t.TempDir()
			calls := filepath.Join(dir, "calls")
			for _, name := range []string{"launchctl", "systemctl"} {
				script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + calls + "'\n"
				if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", dir)
			path := filepath.Join(dir, "service")
			content := "[Service]\nKillMode=process\nExecStart=/fake/agent-net daemon\n"
			if platform == "darwin" {
				content = "<plist><dict><key>EnvironmentVariables</key><dict></dict></dict></plist>"
			}
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
			if err := refreshDaemonService(platform, path, []string{"PATH=/login/bin"}); err != nil {
				t.Fatal(err)
			}
			want := "--user daemon-reload\n--user --no-block restart agent-net-daemon.service\n"
			if platform == "darwin" {
				want = fmt.Sprintf("bootout gui/%d/com.agent-net.daemon\nbootstrap gui/%d %s\n", os.Getuid(), os.Getuid(), path)
			}
			deadline := time.Now().Add(2 * time.Second)
			for {
				b, _ := os.ReadFile(calls)
				if string(b) == want {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("calls = %q, want %q", b, want)
				}
				time.Sleep(10 * time.Millisecond)
			}
			// A plist already rewritten by an older daemon still needs reloading.
			if err := os.Remove(calls); err != nil {
				t.Fatal(err)
			}
			if err := refreshDaemonService(platform, path, []string{"PATH=/login/bin"}); err != nil {
				t.Fatal(err)
			}
			deadline = time.Now().Add(2 * time.Second)
			for {
				b, _ := os.ReadFile(calls)
				if strings.Contains(string(b), "bootstrap") || strings.Contains(string(b), "restart") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("stale environment was not reloaded: %s", b)
				}
				time.Sleep(10 * time.Millisecond)
			}
			// Once running with the stored PATH, another start does no reload.
			t.Setenv("PATH", "/login/bin")
			if err := os.Remove(calls); err != nil {
				t.Fatal(err)
			}
			if err := refreshDaemonService(platform, path, []string{"PATH=/login/bin"}); err != nil {
				t.Fatal(err)
			}
			if b, err := os.ReadFile(calls); err == nil {
				t.Fatalf("unnecessary reload: %s", b)
			}
		})
	}
}
