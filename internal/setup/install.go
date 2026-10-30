package setup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func systemdDir() string { return filepath.Join(home(), ".config", "systemd", "user") }
func launchAgentsDir() string {
	return filepath.Join(home(), "Library", "LaunchAgents")
}

// installService writes the service file and (re)starts the service so a new
// binary is picked up. It never runs sudo.
func (r *runner) installService(s Service) (string, error) {
	switch runtime.GOOS {
	case "linux":
		return r.installSystemd(s)
	case "darwin":
		return r.installLaunchd(s)
	}
	return fmt.Sprintf("skipped (no service support on %s)", runtime.GOOS), nil
}

func (r *runner) installSystemd(s Service) (string, error) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return "skipped (systemctl not found)", nil
	}
	changed, existed, err := writeIfChanged(filepath.Join(systemdDir(), s.UnitName()), []byte(SystemdUnit(s, r.Exe)), 0o644)
	if err != nil {
		return "", err
	}
	for _, args := range [][]string{
		{"daemon-reload"},
		{"enable", s.UnitName()},
		{"restart", s.UnitName()},
	} {
		if out, err := runCmd("", "systemctl", append([]string{"--user"}, args...)...); err != nil {
			return "", fmt.Errorf("systemctl --user %s: %s", args[0], firstLine(out, err))
		}
	}
	if s.Name == "daemon" {
		if out, _ := runCmd("", "loginctl", "show-user", currentUser(), "-p", "Linger"); strings.Contains(out, "Linger=no") {
			r.hint("hint: to keep the services running when you are logged out, run: sudo loginctl enable-linger %s", currentUser())
		}
	}
	return serviceResult(changed, existed), nil
}

func (r *runner) installLaunchd(s Service) (string, error) {
	plist := filepath.Join(launchAgentsDir(), s.PlistName())
	changed, existed, err := writeIfChanged(plist, []byte(LaunchdPlist(s, r.Exe, home())), 0o644)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(home(), "Library", "Logs"), 0o755); err != nil {
		return "", err
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	runCmd("", "launchctl", "bootout", domain+"/"+s.Label()) // not loaded yet is fine
	var out string
	for i := 0; i < 3; i++ {
		// bootout can still be finishing; retry bootstrap briefly.
		if out, err = runCmd("", "launchctl", "bootstrap", domain, plist); err == nil {
			return serviceResult(changed, existed), nil
		}
		time.Sleep(time.Second)
	}
	return "", fmt.Errorf("launchctl bootstrap: %s", firstLine(out, err))
}

// RefreshDaemonUnit is called when the daemon starts under systemd. A daemon
// that an older version upgraded without running setup (an automatic or remote
// upgrade) still has the old unit, without KillMode=process. This adds the line
// and reloads systemd, without a restart, so the next restart keeps the
// workers. It does nothing outside a systemd service. Failures are ignored: the
// next `agent-net setup` writes the whole unit.
func RefreshDaemonUnit() {
	if runtime.GOOS != "linux" || os.Getenv("INVOCATION_ID") == "" || os.Getenv("AGENT_NET_NO_SERVICE") == "1" {
		return
	}
	path := filepath.Join(systemdDir(), Service{Name: "daemon"}.UnitName())
	if changed, err := EnsureWorkersSurviveUnit(path); err == nil && changed {
		if _, err := exec.LookPath("systemctl"); err == nil {
			runCmd("", "systemctl", "--user", "daemon-reload")
		}
	}
}

// removeService stops a service and deletes its file. It reports whether the
// file existed.
func (r *runner) removeService(s Service) (string, error) {
	switch runtime.GOOS {
	case "linux":
		path := filepath.Join(systemdDir(), s.UnitName())
		if !exists(path) {
			return "skipped (not installed)", nil
		}
		if _, err := exec.LookPath("systemctl"); err == nil {
			runCmd("", "systemctl", "--user", "disable", "--now", s.UnitName())
		}
		if err := os.Remove(path); err != nil {
			return "", err
		}
		if _, err := exec.LookPath("systemctl"); err == nil {
			runCmd("", "systemctl", "--user", "daemon-reload")
		}
		return "ok (removed)", nil
	case "darwin":
		path := filepath.Join(launchAgentsDir(), s.PlistName())
		if !exists(path) {
			return "skipped (not installed)", nil
		}
		runCmd("", "launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), s.Label()))
		if err := os.Remove(path); err != nil {
			return "", err
		}
		return "ok (removed)", nil
	}
	return "skipped (no service support)", nil
}

// serviceResult describes a service step. Both platforms always restart the
// service, so the text says what happened to the file and that it restarted.
func serviceResult(changed, existed bool) string {
	switch {
	case !changed:
		return "ok (restarted)"
	case existed:
		return "updated (restarted)"
	}
	return "ok (installed, started)"
}
