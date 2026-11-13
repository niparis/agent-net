package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// TestStopDormantProofScenario writes the text frames for
// scripts/stop-dormant-proof.sh: stopping the CTO, which stays online and
// turns dormant. Each step starts from a fresh model.
func TestStopDormantProofScenario(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		// Step 1: the CTO row before the stop.
		m, _ := ctoStopModel(size.w, size.h)
		frame := ansi.Strip(m.testView())
		if row := sessionRowLine(frame, "CTO"); !strings.Contains(row, "idle") {
			t.Fatalf("%dx%d: the CTO row does not start idle: %q\n%s", size.w, size.h, row, frame)
		}
		writeStopDormantProof(t, size.w, size.h, "before", "open Sessions, filtered to the CTO row",
			"the CTO row reads idle", frame)

		// Step 2: x then y marks the row while the stop runs.
		m, _ = press(m, keyRune("x"))
		m, cmd := press(m, keyRune("y"))
		if cmd == nil {
			t.Fatalf("%dx%d: the confirm did not start a stop", size.w, size.h)
		}
		frame = ansi.Strip(m.testView())
		if row := sessionRowLine(frame, "CTO"); !strings.Contains(row, "stopping") {
			t.Fatalf("%dx%d: the row has no pending mark: %q\n%s", size.w, size.h, row, frame)
		}
		writeStopDormantProof(t, size.w, size.h, "stopping", "press x, then y, on the CTO row",
			"the row shows stopping while the stop runs", frame)

		// Step 3: the reply and the gateway snapshot arrive: online, dormant.
		m = finish(t, m, cmd)
		m.applyFetch(fetchedMsg{snap: dormantAfterStop(m.snap, ctoTestSession), at: t0})
		frame = ansi.Strip(m.testView())
		if row := sessionRowLine(frame, "CTO"); strings.Contains(row, "stopping") || !strings.Contains(row, "dormant") {
			t.Fatalf("%dx%d: the stopped row is not dormant: %q\n%s", size.w, size.h, row, frame)
		}
		writeStopDormantProof(t, size.w, size.h, "dormant", "the stop reply and the snapshot arrive: online, status dormant",
			"the stopping mark is gone and the row reads dormant", frame)

		// Step 4: the timeout stays quiet for a session that reads dormant.
		m.now = func() time.Time { return t0.Add(31 * time.Second) }
		next, _ := m.update(tickMsg{})
		m = next.(Model)
		frame = ansi.Strip(m.testView())
		if strings.Contains(frame, "still running") {
			t.Fatalf("%dx%d: the timeout says still running:\n%s", size.w, size.h, frame)
		}
		writeStopDormantProof(t, size.w, size.h, "expired", "the next tick runs 31 s after the stop",
			"no still running notice; the row still reads dormant", frame)
	}
}

func writeStopDormantProof(t *testing.T, w, h int, step, action, expected, frame string) {
	t.Helper()
	dir := os.Getenv("AGENT_NET_PROOF_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	capture := fmt.Sprintf("build: %s\nsize: %dx%d\nstep: %s\naction: %s\nexpected: %s\nerrors: none\n\n%s\n", os.Getenv("AGENT_NET_PROOF_BUILD"), w, h, step, action, expected, frame)
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("stop-dormant-%s-%dx%d.txt", step, w, h)), []byte(capture), 0600); err != nil {
		t.Fatal(err)
	}
}
