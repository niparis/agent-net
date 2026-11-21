package daemon

import (
	"os"
	"path/filepath"
	"strings"
)

// The urgent marker (decision 0180). The Claude Code hook runs after every tool
// call of every session, and almost always nothing urgent waits. So the daemon
// leaves one empty file per session that has an urgent message held, and the
// hook stats it and exits at once when it is missing: no dial, no daemon call.

const urgentMarkerDir = "urgent"

// UrgentMarkerPath is the marker file of one session under the daemon's state
// directory. It is empty when the native ID is not a plain file name, so a
// hook input can never point outside the marker directory.
func UrgentMarkerPath(stateDir, harness, nativeID string) string {
	if !safeMarkerPart(harness) || !safeMarkerPart(nativeID) {
		return ""
	}
	return filepath.Join(stateDir, urgentMarkerDir, harness+"-"+nativeID)
}

func safeMarkerPart(s string) bool {
	if s == "" || len(s) > 200 {
		return false
	}
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.'
		if !ok {
			return false
		}
	}
	return !strings.HasPrefix(s, ".")
}

// setUrgentMarker creates the marker of a session. A failure only costs the
// early delivery: the message still goes at the turn end.
func (d *Daemon) setUrgentMarker(harness, nativeID string) {
	p := UrgentMarkerPath(d.cfg.StateDir, harness, nativeID)
	if p == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		d.Logf("urgent marker for %s: %v", nativeID, err)
		return
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		d.Logf("urgent marker for %s: %v", nativeID, err)
		return
	}
	f.Close()
}

func (d *Daemon) clearUrgentMarker(harness, nativeID string) {
	if p := UrgentMarkerPath(d.cfg.StateDir, harness, nativeID); p != "" {
		os.Remove(p)
	}
}

// clearUrgentHeld forgets that an urgent message waits for a session: the
// memory flag and the marker. It is true when the flag was set.
func (d *Daemon) clearUrgentHeld(sessionID string) bool {
	_, ok := d.urgentHeld.LoadAndDelete(sessionID)
	if ls := d.sessionByID(sessionID); ls != nil {
		info := d.infoOf(ls)
		d.clearUrgentMarker(info.Harness, info.NativeID)
	}
	return ok
}

// clearUrgentMarkers removes every marker. At start the memory flags are gone,
// so a marker left by an earlier run would send every hook call to the daemon
// for nothing.
func (d *Daemon) clearUrgentMarkers() {
	os.RemoveAll(filepath.Join(d.cfg.StateDir, urgentMarkerDir))
}
