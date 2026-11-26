package setup

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// RestartDaemon restarts the daemon service so it reads the gateway URL in a
// changed node.json. The note says what happened for the command's one line.
func RestartDaemon() (string, error) {
	return restartDaemon(runtime.GOOS)
}

func restartDaemon(platform string) (string, error) {
	if os.Getenv("AGENT_NET_NO_SERVICE") == "1" {
		return "daemon not restarted (AGENT_NET_NO_SERVICE=1)", nil
	}
	service := Service{Name: "daemon"}
	switch platform {
	case "linux":
		path, err := exec.LookPath("systemctl")
		if err != nil {
			return "daemon not restarted (systemctl not found)", nil
		}
		out, err := exec.Command(path, "--user", "--no-block", "restart", service.UnitName()).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("systemctl --user restart %s: %s", service.UnitName(), firstLine(strings.TrimSpace(string(out)), err))
		}
		return "daemon restarted", nil
	case "darwin":
		path, err := exec.LookPath("launchctl")
		if err != nil {
			return "daemon not restarted (launchctl not found)", nil
		}
		target := fmt.Sprintf("gui/%d/%s", os.Getuid(), service.Label())
		out, err := exec.Command(path, "kickstart", "-k", target).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("launchctl kickstart %s: %s", target, firstLine(strings.TrimSpace(string(out)), err))
		}
		return "daemon restarted", nil
	}
	return "daemon not restarted (no service support on " + platform + ")", nil
}
