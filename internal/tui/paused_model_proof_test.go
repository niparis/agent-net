package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

// pausedStartNote is the note the gateway wrote for an automatic start while
// the owner had paused Opus (gateway TestAutomaticStartLeavesOutAPausedModel).
const pausedStartNote = "difficulty: no assessment (no judge result); started at hard -> opencode deepseek/deepseek-v4.1-flash:high (bucket 3.33-3.67: deepseek n0, chosen (benchmark); deepseek opencode n0 allowed (benchmark); terra n0 allowed (benchmark); rotation weight 1.50; deepseek: 1 of the last 1 hard starts; skipped opus: paused by the owner); uses pay-per-use provider (deepseek)"

// The worker log says which model the owner paused and that the pick left it
// out, at both screen sizes (decision 0188). With AGENT_NET_PROOF_DIR set, the
// frames are written there (scripts/paused-model-proof.sh).
func TestPausedModelProofScenario(t *testing.T) {
	workerLogProof(t, workerLogScenario{
		file:   "paused-model",
		action: "open the TUI after an automatic start while the owner paused Opus (score 0)",
		want:   "skipped opus: paused by the owner",
		entry: protocol.WorkerLogEntry{StartedAt: t0.Unix() - 600, Title: "Fix the login page", Harness: "opencode",
			Model: "deepseek/deepseek-v4.1-flash:high", Status: "done", Difficulty: "hard", RunSeconds: 540, ModelNote: pausedStartNote},
	})
}

// workerLogScenario is one worker log entry a proof opens, and the text its
// details must show.
type workerLogScenario struct {
	file, action, want string
	entry              protocol.WorkerLogEntry
}

// workerLogProof opens the worker log in the Quotas tab at 80x24 and 120x34
// with one entry, and checks that its details show the wanted text and that no
// line overflows. With AGENT_NET_PROOF_DIR set, each frame is written there.
func workerLogProof(t *testing.T, s workerLogScenario) {
	t.Helper()
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		w, h := size[0], size[1]
		m := New(&fakeSource{snap: fakeSnapshot()})
		m.tab, m.workersLog, m.haveData = quotasTab, true, true
		m.loc, m.now = time.UTC, func() time.Time { return t0 }
		m.snap.WorkerLog = []protocol.WorkerLogEntry{s.entry}
		m.width, m.height = w, h
		frame := ansi.Strip(m.testView())
		// The note wraps inside the details box: read it without the borders
		// and the line breaks.
		text := strings.Join(strings.Fields(strings.ReplaceAll(frame, "│", " ")), " ")
		if !strings.Contains(text, s.want) {
			t.Fatalf("%dx%d: the worker log does not say %q:\n%s", w, h, s.want, frame)
		}
		for _, line := range strings.Split(frame, "\n") {
			if ansi.StringWidth(line) > w {
				t.Fatalf("%dx%d: line overflow: %q", w, h, line)
			}
		}
		dir := os.Getenv("AGENT_NET_PROOF_DIR")
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		capture := fmt.Sprintf("build: %s\nstep: open the worker log in the Quotas tab at %dx%d\naction: %s\nexpected: the picker note says %q\nerrors: none\n\n%s\n",
			os.Getenv("AGENT_NET_PROOF_BUILD"), w, h, s.action, s.want, frame)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s-%dx%d.txt", s.file, w, h)), []byte(capture), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
