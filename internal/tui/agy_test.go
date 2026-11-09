package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// Since build step 4 an agy profile can start workers, so the panel shows it
// like any other profile, not stale and not "not startable yet".
func TestWorkerPanelAgyProfileIsStartable(t *testing.T) {
	m := newModel(120, 34)
	m.snap.Nodes = m.snap.Nodes[:1]
	m.snap.Nodes[0].Harnesses = []protocol.HarnessInfo{{Name: "agy", Models: []string{"gemini-3.8-flash-low"}}}
	m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "argon agy", Harness: "agy", Model: "gemini-3.8-flash-low"}}
	view := ansi.Strip(strings.Join(m.quotaModelsView(120), "\n"))
	for _, unwanted := range []string{"not startable yet", "model gone"} {
		if strings.Contains(view, unwanted) {
			t.Errorf("agy profile shown as %q:\n%s", unwanted, view)
		}
	}
}

// The gateway's picker preview marks an agy profile like any other profile.
func TestAgyProfileIsInThePickerPreview(t *testing.T) {
	m := newModel(120, 34)
	m.snap.Nodes = m.snap.Nodes[:1]
	m.snap.Nodes[0].Harnesses = []protocol.HarnessInfo{{Name: "agy", Models: []string{"gemini-3.8-flash-low"}}}
	m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "argon agy", Harness: "agy", Model: "gemini-3.8-flash-low"}}
	m.snap.WorkerProviders = []string{"agy"}
	m.snap.Scores = []protocol.ModelScore{{Match: "gemini-3.8-flash-low", Score: 55}}
	m.snap.Nodes[0].QualityPickModels = map[string]string{"normal": pickerPreviewName("gemini-3.8-flash-low")}
	if view := ansi.Strip(strings.Join(m.quotaModelsView(120), "\n")); !strings.Contains(view, "▸") {
		t.Fatalf("agy preview is not marked:\n%s", view)
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
		if strings.Contains(line, "claude-sonnet-5-5-medium") && strings.Contains(line, "🔒") {
			t.Fatalf("agy Claude profile is locked: %s", line)
		}
	}
}

func TestAgyWaitingDeliveryDetail(t *testing.T) {
	m, _ := feedModel([]protocol.FeedEvent{{Event: protocol.Event{ID: 1, Kind: "msg.dispatched", MessageID: "m_1", Detail: "waiting for the session's next turn"}}}, nil)
	m.open = &openMsg{id: "m_1", loading: true}
	if got := strings.Join(m.messageView(120, 30), "\n"); !strings.Contains(got, "waiting for the session's next turn") {
		t.Fatalf("waiting detail missing: %s", got)
	}
}
