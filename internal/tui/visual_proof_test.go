package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

// TestVisualProofScenario renders independent fixed-data scenes. The shell
// scenario exports captures using the same renderer as the TUI screen tests.
func TestVisualProofScenario(t *testing.T) {
	for _, width := range []int{120, 80} {
		m := newModel(width, 34)
		m.tab = quotasTab
		m.qualityDetail = true
		m.showWeekly = true
		m.weeklyLoaded = true
		m.weeklyScores = []protocol.ScoresWeeklyRow{{Week: "2026-W46", Runs: 2, ProofUnavailable: map[string]int{"example-model": 1}}}
		frame := ansi.Strip(m.testView())
		if !strings.Contains(frame, "example-model: proof unavailable 1") {
			t.Fatal(frame)
		}
		if dir := os.Getenv("AGENT_NET_PROOF_DIR"); dir != "" {
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			capture := fmt.Sprintf("build: %s\nstep: open weekly quality at %dx34\naction: open Quality, scores over time\nexpected: model and unavailable-proof count visible\nerrors: none\n\n%s\n", os.Getenv("AGENT_NET_PROOF_BUILD"), width, frame)
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("weekly-%dx34.txt", width)), []byte(capture), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	ch := deliveryChange{ID: "ch_example", Title: "Visual proof", Repo: "demo", State: protocol.ChangeOpen, Origin: protocol.ChangeOrigin{Kind: protocol.OriginWorker, VisualEvidence: &protocol.VisualEvidence{UserVisible: "yes", VisualProof: []protocol.VisualProof{{File: "scroll.txt", Caption: "scroll: final row visible at 120x34"}}}}}
	m := newModel(120, 34)
	frame := ansi.Strip(strings.Join(m.deliveryDetail(ch, nil, 120, 34), "\n"))
	for _, want := range []string{"user_visible: yes", "scroll.txt", "final row visible"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("change frame missing %s:\n%s", want, frame)
		}
	}
	if dir := os.Getenv("AGENT_NET_PROOF_DIR"); dir != "" {
		capture := fmt.Sprintf("build: %s\nstep: open change\naction: open change details at 120x34\nexpected: proof file and caption visible\nerrors: none\n\n%s\n", os.Getenv("AGENT_NET_PROOF_BUILD"), frame)
		if err := os.WriteFile(filepath.Join(dir, "change-120x34.txt"), []byte(capture), 0600); err != nil {
			t.Fatal(err)
		}
	}

}
