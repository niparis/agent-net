package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// An agy profile is stored, and the Workers panel says that it cannot start a
// worker yet instead of calling it stale (decision 0049, step 2).
func TestWorkerPanelAgyProfileSaysNotStartableYet(t *testing.T) {
	m := newModel(120, 34)
	m.snap.Nodes = m.snap.Nodes[:1]
	m.snap.Nodes[0].Harnesses = []protocol.HarnessInfo{{Name: "agy", Models: []string{"gemini-3.8-flash-low"}}}
	m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "argon agy", Harness: "agy", Model: "gemini-3.8-flash-low"}}
	view := ansi.Strip(strings.Join(m.quotaModelsView(120), "\n"))
	if !strings.Contains(view, "not startable yet") {
		t.Fatalf("missing the note:\n%s", view)
	}
	for _, unwanted := range []string{"model gone", "not available on an online machine", "not a worker provider"} {
		if strings.Contains(view, unwanted) {
			t.Errorf("agy profile shown as %q:\n%s", unwanted, view)
		}
	}
}

// A profile on a harness that cannot start workers yet is never a forecast
// candidate, even when its model is listed, allowed and scored.
func TestAgyProfileIsNotAWorkerCandidate(t *testing.T) {
	m := newModel(120, 34)
	m.snap.Nodes = m.snap.Nodes[:1]
	m.snap.Nodes[0].Harnesses = []protocol.HarnessInfo{{Name: "agy", Models: []string{"gemini-3.8-flash-low"}}}
	m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "argon agy", Harness: "agy", Model: "gemini-3.8-flash-low"}}
	m.snap.WorkerProviders = []string{"agy"}
	m.snap.Scores = []protocol.ModelScore{{Match: "gemini-3.8-flash-low", Score: 55}}
	for _, c := range m.workerForecastInputs().candidates {
		if c.Harness == "agy" {
			t.Fatalf("agy is a worker candidate: %+v", c)
		}
	}
}

// A Claude model through agy is not the owner's Anthropic subscription, so the
// panel shows no lock on it.
func TestWorkerPanelAgyClaudeProfileIsNotProtected(t *testing.T) {
	m := newModel(120, 34)
	m.snap.Nodes = m.snap.Nodes[:1]
	m.snap.Nodes[0].Harnesses = []protocol.HarnessInfo{{Name: "agy", Models: []string{"claude-sonnet-5-5-medium"}}}
	m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "sonnet agy", Harness: "agy", Model: "claude-sonnet-5-5-medium"}}
	m.snap.ProtectedProviders = protocol.ProtectedProviders{Providers: []string{"anthropic"}}
	view := ansi.Strip(strings.Join(m.quotaModelsView(120), "\n"))
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "sonnet agy") && strings.Contains(line, "🔒") {
			t.Fatalf("agy Claude profile is locked: %s", line)
		}
	}
}
