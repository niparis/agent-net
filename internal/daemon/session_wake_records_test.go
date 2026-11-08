package daemon

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A finished background run of the same session does not stop an interactive
// wake. Claude keeps its record after the run, so the session then has an
// interactive record and a finished background one. The wake resumes it.
func TestMessageWakesInteractiveClaudeWithFinishedBackgroundRecord(t *testing.T) {
	r := newInteractiveWakeRig(t, protocol.AutonomyWake, []map[string]any{
		{"id": wakeNative[:8], "kind": "interactive", "state": "done"},
		{"id": wakeNative[:8], "kind": "background", "state": "done"},
	})
	r.send(t, "wake after a background run")
	eventually(t, "interactive session with a finished background record receives message", func() bool {
		if lines := r.g.logs.lines("session wake", r.id); len(lines) > 0 {
			t.Fatalf("wake failed: %v", lines)
		}
		return len(r.c.adapter.deliveredTo(r.id)) == 1
	})
}

// A session whose machine has not recorded its mode is not known to be a
// background session, so with autonomy wake it is resumed like an interactive
// one. The gateway asks only with autonomy wake for such a session.
func TestMessageWakesClaudeOfUnknownModeWithAutonomy(t *testing.T) {
	r := newClaudeWakeRig(t, "", protocol.AutonomyWake, nil)
	r.send(t, "wake the session of unknown mode")
	eventually(t, "session of unknown mode receives message", func() bool {
		if lines := r.g.logs.lines("session wake", r.id); len(lines) > 0 {
			t.Fatalf("wake failed: %v", lines)
		}
		return len(r.c.adapter.deliveredTo(r.id)) == 1
	})
	if argValue(readClaude(t, r.repo).Args, "--resume") != wakeNative {
		t.Fatal("session was not resumed")
	}
}
