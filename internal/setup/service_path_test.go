package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceLoginPath(t *testing.T) {
	shell := filepath.Join(t.TempDir(), "shell")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\nprintf '%s\\0' '/test/bin:/other/bin' '' '' '' '' '' '' '' ''\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	for _, got := range []string{SystemdUnit(Service{Name: "daemon"}, "/bin/a"), LaunchdPlist(Service{Name: "daemon"}, "/bin/a", "/home/test")} {
		if !strings.Contains(got, "/test/bin:/other/bin") {
			t.Errorf("missing login PATH: %s", got)
		}
	}
}

func TestUpgradeAddsServicePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.service")
	if err := os.WriteFile(path, []byte("[Service]\nExecStart=/bin/a\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureDaemonServicePath(path, false, "/test/bin"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "Environment=PATH=/test/bin") {
		t.Fatalf("missing PATH: %s", b)
	}
}

func TestUpgradePlistAddsAndKeepsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.plist")
	original := "<plist><dict><key>EnvironmentVariables</key><dict><key>HOME</key><string>/home/test</string></dict></dict></plist>"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	if changed, err := EnsureDaemonServicePath(path, true, "/test/a&b"); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if changed, err := EnsureDaemonServicePath(path, true, "/different"); err != nil || changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "/test/a&amp;b") || !strings.Contains(string(b), "<key>HOME</key>") {
		t.Fatalf("plist: %s", b)
	}
}

func TestLoginPathFallback(t *testing.T) {
	t.Setenv("SHELL", "/does/not/exist")
	t.Setenv("PATH", "/test/current")
	got := SystemdUnit(Service{Name: "daemon"}, "/bin/a")
	for _, dir := range []string{"/test/current", "/opt/homebrew/bin", "/usr/local/bin", ".local/bin"} {
		if !strings.Contains(got, dir) {
			t.Errorf("missing %s: %s", dir, got)
		}
	}
}

func TestServiceUsesLoginEnvironment(t *testing.T) {
	shell := filepath.Join(t.TempDir(), "shell")
	script := `#!/bin/sh
printf '%s\0' '/login/bin' '/login/cache' '/login/config' '/login/data & more' '/login/runtime' '/login/state' '/login/db' '/login/pi' '1'
`
	if err := os.WriteFile(shell, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	t.Setenv("XDG_DATA_HOME", "/wrong")
	t.Setenv("QUOTA_AXI_OPENCODE_GO_PI_AUTH", "wrong")
	for _, got := range []string{SystemdUnit(Service{Name: "daemon"}, "/bin/a"), LaunchdPlist(Service{Name: "daemon"}, "/bin/a", "/home/test")} {
		for _, want := range []string{"/login/bin", "/login/cache", "/login/config", "/login/data", "/login/runtime", "/login/state", "/login/db", "/login/pi", "QUOTA_AXI_OPENCODE_GO_PI_AUTH"} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in %s", want, got)
			}
		}
		if strings.Contains(got, "/wrong") || strings.Contains(got, "wrong") {
			t.Errorf("inherited process environment: %s", got)
		}
	}
}

func TestUpgradeAddsMissingLoginEnvironment(t *testing.T) {
	for _, plist := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "service")
		content := "[Service]\nEnvironment=PATH=/old\nExecStart=/bin/a\n"
		if plist {
			content = "<plist><dict><key>EnvironmentVariables</key><dict><key>PATH</key><string>/old</string></dict></dict></plist>"
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		values := []string{"PATH=/new", "QUOTA_AXI_OPENCODE_GO_PI_AUTH=1"}
		if changed, err := EnsureDaemonServiceEnvironment(path, plist, values); err != nil || !changed {
			t.Fatalf("changed %v err %v", changed, err)
		}
		if changed, err := EnsureDaemonServiceEnvironment(path, plist, values); err != nil || changed {
			t.Fatalf("second changed %v err %v", changed, err)
		}
		b, _ := os.ReadFile(path)
		if !strings.Contains(string(b), "/old") || strings.Contains(string(b), "/new") || !strings.Contains(string(b), "QUOTA_AXI_OPENCODE_GO_PI_AUTH") {
			t.Fatalf("service: %s", b)
		}
	}
}
