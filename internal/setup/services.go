// Package setup installs agent-net on one machine: services, join, and the
// Claude Code and Pi integrations. Every step is safe to run again.
package setup

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
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
	after, restart := "", "always"
	switch s.Name {
	case "daemon":
		after = "After=agent-net-gateway.service\n"
	case "gateway":
		after = "After=network-online.target\n"
		restart = "on-failure"
	}
	return fmt.Sprintf(`[Unit]
Description=agent-net %s
%s
[Service]
ExecStart=%s
Restart=%s
RestartSec=2

[Install]
WantedBy=default.target
`, s.Name, after, strings.Join(cmd, " "), restart)
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
  <key>StandardErrorPath</key>
  <string>%s</string>
  <key>StandardOutPath</key>
  <string>%s</string>
</dict>
</plist>
`, s.Label(), args.String(), log, log)
}
