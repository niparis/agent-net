package setup

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// refreshDaemonService only operates on the daemon job. Tests supply a private
// service file and fake service commands. No service command runs if the file
// does not exist.
func refreshDaemonService(platform, path string, environment []string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	unitChanged := false
	if platform == "linux" {
		var err error
		unitChanged, err = EnsureWorkersSurviveUnit(path)
		if err != nil {
			return err
		}
	}
	changed, err := EnsureDaemonServiceEnvironment(path, platform == "darwin", environment)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	stored := servicePATH(raw, platform == "darwin")
	stale := stored != "" && stored != os.Getenv("PATH")
	if platform == "linux" {
		if !unitChanged && !changed && !stale {
			return nil
		}
		if out, err := runCmd("", "systemctl", "--user", "daemon-reload"); err != nil {
			return fmt.Errorf("systemctl --user daemon-reload: %s", firstLine(out, err))
		}
		if changed || stale {
			if out, err := runCmd("", "systemctl", "--user", "--no-block", "restart", Service{Name: "daemon"}.UnitName()); err != nil {
				return fmt.Errorf("systemctl --user restart: %s", firstLine(out, err))
			}
		}
		return nil
	}
	if !changed && !stale {
		return nil
	}
	launchctl, err := exec.LookPath("launchctl")
	if err != nil {
		return err
	}
	// bootout terminates this daemon. A separate process group must finish
	// bootstrap even when the old job lacks AbandonProcessGroup. Pass paths as
	// arguments, never shell source. No other LaunchAgent is unloaded.
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	script := `"$1" bootout "$2/com.agent-net.daemon"
for attempt in 1 2 3; do
 "$1" bootstrap "$2" "$3" && exit 0
 /bin/sleep 1
done
exit 1
`
	cmd := exec.Command("/bin/sh", "-c", script, "agent-net-reload", launchctl, domain, path)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start LaunchAgent reload: %w", err)
	}
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("reload daemon LaunchAgent: %v", err)
		}
	}()
	return nil
}

// servicePATH reads the stored PATH rather than the current login settings:
// setup preserves explicit settings in existing service files.
func servicePATH(raw []byte, plist bool) string {
	if plist {
		decoder := xml.NewDecoder(bytes.NewReader(raw))
		for {
			token, err := decoder.Token()
			if err != nil {
				return ""
			}
			if start, ok := token.(xml.StartElement); ok && start.Name.Local == "key" {
				var key string
				if decoder.DecodeElement(&key, &start) != nil {
					return ""
				}
				if key == "PATH" {
					var value string
					if decoder.Decode(&value) == nil {
						return value
					}
					return ""
				}
			}
		}
	}
	for _, line := range strings.Split(string(raw), "\n") {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), "Environment=")
		if !ok {
			continue
		}
		if strings.HasPrefix(value, "\"") {
			if unquoted, err := strconv.Unquote(value); err == nil {
				value = unquoted
			}
		}
		if path, ok := strings.CutPrefix(value, "PATH="); ok {
			return strings.ReplaceAll(path, "%%", "%")
		}
	}
	return ""
}
