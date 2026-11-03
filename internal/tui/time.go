package tui

import (
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// displayTime turns a Unix time in seconds into the owner's display zone. Every
// time the screen shows goes through it (or through clock, which takes an
// already parsed time), so the screen never uses the zone of the machine that
// runs it.
func (m Model) displayTime(unix int64) time.Time {
	return time.Unix(unix, 0).In(m.loc)
}

// utcZones are the names that mean UTC to auto. A zone that merely stands at
// zero offset in winter, such as Europe/London, is not one of them.
var utcZones = map[string]bool{
	"UTC": true, "Etc/UTC": true, "GMT": true, "Etc/GMT": true,
	"Universal": true, "Zulu": true, "Etc/Universal": true, "Etc/Zulu": true,
}

func isUTCZone(name string) bool { return utcZones[name] }

// timeZoneChoice says which zone the screen shows and where it came from. The
// owner's setting wins. Auto takes the first non-UTC zone that a machine
// reported, preferring a Mac: the owner's own machine, not a server. When
// every machine is on UTC, it says UTC rather than keeping the server's zone.
func timeZoneChoice(setting string, nodes []protocol.NodeView) (zone, source string, ok bool) {
	if setting != "" && !strings.EqualFold(setting, protocol.TimeZoneAuto) {
		if _, err := time.LoadLocation(setting); err == nil {
			return setting, "set by you", true
		}
	}
	for _, wantMac := range []bool{true, false} {
		for _, n := range nodes {
			if wantMac && n.OS != "darwin" {
				continue
			}
			if z := strings.TrimSpace(n.TimeZone); z != "" && !isUTCZone(z) {
				if _, err := time.LoadLocation(z); err == nil {
					return z, "auto, from " + timeZoneMachine(n), true
				}
			}
		}
	}
	for _, n := range nodes {
		if z := strings.TrimSpace(n.TimeZone); z != "" {
			if _, err := time.LoadLocation(z); err == nil {
				return z, "auto, from " + timeZoneMachine(n), true
			}
		}
	}
	return "", "auto", false
}

func timeZoneMachine(n protocol.NodeView) string {
	return firstNonEmpty(n.Hostname, firstNonEmpty(n.Reported, n.NodeID))
}

// applyTimeZone sets the display location from the owner's setting. When auto
// finds nothing (no machine reported a zone yet), the location stays as it is.
func (m *Model) applyTimeZone() {
	zone, source, ok := timeZoneChoice(m.snap.TimeZone, m.snap.Nodes)
	m.zoneSource = source
	if !ok {
		return
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return
	}
	m.loc, m.zoneName = loc, zone
}

// timeZoneLine is the Settings row: the effective zone and its source.
func (m Model) timeZoneLine() string {
	if m.zoneName == "" {
		return "Time zone: auto"
	}
	line := "Time zone: " + m.zoneName
	if m.zoneSource != "" && m.zoneSource != protocol.TimeZoneAuto {
		line += " (" + m.zoneSource + ")"
	}
	return line
}
