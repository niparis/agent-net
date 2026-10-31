// Package setup installs agent-net on one machine: services, join, and the
// Claude Code and Pi integrations. Every step is safe to run again.
package setup

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"strings"

	"github.com/niparis/agent-net/internal/loginpath"
	"github.com/niparis/agent-net/internal/quotaaxi"
)

// Service is one long-running agent-net process.
type Service struct {
	Name string   // "daemon" or "gateway"
	Args []string // arguments after the binary path
}

// UnitName is the systemd unit file name.
func (s Service) UnitName() string { return "agent-net-" + s.Name + ".service" }

// Label is the launchd label.
func (s Service) Label() string { return "com.agent-net." + s.Name }

// PlistName is the launchd plist file name.
func (s Service) PlistName() string { return s.Label() + ".plist" }

// LogName is the log file name under ~/Library/Logs on macOS.
func (s Service) LogName() string { return "agent-net-" + s.Name + ".log" }

func systemdQuote(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	if strings.ContainsAny(s, " \t\"'\\") {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
	}
	return s
}

// SystemdUnit renders the user unit for a service.
func SystemdUnit(s Service, exe string) string {
	cmd := []string{systemdQuote(exe)}
	for _, a := range s.Args {
		cmd = append(cmd, systemdQuote(a))
	}
	after, restart, kill := "", "always", ""
	switch s.Name {
	case "daemon":
		after = "After=agent-net-gateway.service\n"
		// Workers are children of the daemon and must outlive it: a restart
		// (an upgrade) keeps them, and the next daemon adopts them. The
		// default, control-group, would kill them with the daemon.
		kill = killModeLine + "\n"
	case "gateway":
		after = "After=network-online.target\n"
		restart = "on-failure"
	}
	var environment strings.Builder
	if s.Name == "daemon" {
		for _, item := range daemonEnvironment(os.Getenv("HOME")) {
			fmt.Fprintf(&environment, "Environment=%s\n", systemdQuote(item))
		}
	}
	return fmt.Sprintf(`[Unit]
Description=agent-net %s
%s
[Service]
ExecStart=%s
%sRestart=%s
RestartSec=2

[Install]
WantedBy=default.target
`, s.Name, after, strings.Join(cmd, " "), kill+environment.String(), restart)
}

const killModeLine = "KillMode=process"

// EnsureWorkersSurviveUnit adds KillMode=process to the daemon unit that an
// older setup wrote, so the next restart keeps the workers. It only edits the
// file; the caller reloads systemd. It reports whether it changed the file.
// A unit that already has a KillMode line (the owner's choice) is left alone.
func EnsureWorkersSurviveUnit(path string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, nil // no unit: not run by this service
	}
	text := string(raw)
	if strings.Contains(text, "\nKillMode=") || !strings.Contains(text, "\nExecStart=") {
		return false, nil
	}
	text = strings.Replace(text, "\nExecStart=", "\n"+killModeLine+"\nExecStart=", 1)
	return true, os.WriteFile(path, []byte(text), 0o644)
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// LaunchdPlist renders the launchd agent for a service. Paths are absolute.
func LaunchdPlist(s Service, exe, home string) string {
	var args strings.Builder
	for _, a := range append([]string{exe}, s.Args...) {
		fmt.Fprintf(&args, "    <string>%s</string>\n", xmlEscape(a))
	}
	log := xmlEscape(home + "/Library/Logs/" + s.LogName())
	// launchd kills what is left in the job's process group when the job
	// exits. Workers must outlive the daemon, so the daemon job abandons it.
	abandon := ""
	if s.Name == "daemon" {
		abandon = "  <key>AbandonProcessGroup</key>\n  <true/>\n"
	}
	var environment strings.Builder
	if s.Name == "daemon" {
		environment.WriteString("  <key>EnvironmentVariables</key>\n  <dict>\n")
		for _, item := range daemonEnvironment(home) {
			key, value, _ := strings.Cut(item, "=")
			fmt.Fprintf(&environment, "    <key>%s</key>\n    <string>%s</string>\n", xmlEscape(key), xmlEscape(value))
		}
		environment.WriteString("  </dict>\n")
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
%s  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ThrottleInterval</key>
  <integer>2</integer>
%s  <key>StandardErrorPath</key>
  <string>%s</string>
  <key>StandardOutPath</key>
  <string>%s</string>
</dict>
</plist>
`, s.Label(), args.String(), abandon+environment.String(), log, log)
}

// daemonEnvironment captures the login settings once for each service file.
func daemonEnvironment(home string) []string {
	captured := loginpath.Environment()
	env := quotaaxi.ServiceEnvironment(home, captured)
	for _, item := range captured {
		if strings.HasPrefix(item, "PATH=") {
			env = append(env, item)
			break
		}
	}
	return env
}

// EnsureDaemonServicePath is kept for callers migrating only PATH.
func EnsureDaemonServicePath(path string, plist bool, value string) (bool, error) {
	return EnsureDaemonServiceEnvironment(path, plist, []string{"PATH=" + value})
}

// EnsureDaemonServiceEnvironment adds missing entries without replacing settings.
func EnsureDaemonServiceEnvironment(path string, plist bool, values []string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, nil
	}
	text := string(raw)
	changed := false
	for _, item := range values {
		key, value, ok := strings.Cut(item, "=")
		if !ok || key == "" || value == "" {
			continue
		}
		if plist {
			if strings.Contains(text, "<key>"+key+"</key>") {
				continue
			}
			entry := "    <key>" + xmlEscape(key) + "</key>\n    <string>" + xmlEscape(value) + "</string>\n"
			if start := strings.Index(text, "<key>EnvironmentVariables</key>"); start >= 0 {
				i := strings.Index(text[start:], "<dict>")
				if i < 0 {
					continue
				}
				i += start + len("<dict>")
				text = text[:i] + "\n" + entry + text[i:]
			} else if strings.Contains(text, "</dict>") {
				text = strings.Replace(text, "</dict>", "  <key>EnvironmentVariables</key>\n  <dict>\n"+entry+"  </dict>\n</dict>", 1)
			} else {
				continue
			}
		} else {
			if strings.Contains(text, "Environment="+key+"=") || strings.Contains(text, "Environment=\""+key+"=") {
				continue
			}
			if !strings.Contains(text, "\nExecStart=") {
				continue
			}
			text = strings.Replace(text, "\nExecStart=", "\nEnvironment="+systemdQuote(item)+"\nExecStart=", 1)
		}
		changed = true
	}
	if !changed {
		return false, nil
	}
	return true, os.WriteFile(path, []byte(text), 0644)
}
