package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/model"
	"github.com/niparis/agent-net/internal/protocol"
)

// A protected provider is held by a floor of 100 percent left. When it is the
// only model the ranker can allow, the gate refuses it and the start says why.
func TestAProtectedProviderIsHeldByAFloor(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	node := &protocol.NodeView{Hostname: "box", Harnesses: []protocol.HarnessInfo{
		{Name: "claude", SignedIn: "yes"}, {Name: "codex", SignedIn: "yes"}}}
	const week = 7 * 24 * 3600
	quotas := &protocol.Quotas{GeneratedAt: 1000, Providers: []protocol.QuotaProvider{
		{Provider: "anthropic", Windows: []protocol.QuotaWindow{{Kind: "weekly", PercentUsed: 10, ResetsAt: 1000 + week}}},
	}}
	_, err := g.Choose(ChooseRequest{
		Purpose: model.PurposeWorkerAutomatic, Node: node,
		Candidates: []protocol.WorkerModelCandidate{
			{Name: "claude", Harness: "claude", Model: "claude-opus-5-5"},
			{Name: "terra", Harness: "codex", Model: "gpt-6.1-terra"},
		},
		Difficulty: "hard", D: protocol.PickerD(nil, "hard"),
		Scores:    []protocol.ModelScore{{Match: "claude-opus-5-5", Score: 60}, {Match: "gpt-6.1-terra", Score: 10}},
		Quotas:    quotas,
		Protected: protocol.ProtectedProviders{Providers: []string{"anthropic"}, Reason: "orchestrator"},
	})
	if err == nil || !strings.Contains(err.Error(), "under your 100% worker floor") {
		t.Fatalf("err = %v, want the protected provider held by its floor", err)
	}
}

// A protected provider stays out while an ordinary candidate is allowed.
func TestProtectedChooserKeepsProtectedOutOfTheCostRanker(t *testing.T) {
	candidates := []protocol.WorkerModelCandidate{
		{Name: "terra", Harness: "codex", Model: "gpt-6.1-terra", Provider: "codex"},
		{Name: "deepseek", Harness: "opencode", Model: "opencode-go/deepseek", Provider: "opencode-go"},
		{Name: "claude", Harness: "opencode", Model: "anthropic/claude-sonnet", Provider: "anthropic"},
	}
	scores := []protocol.ModelScore{{Match: "terra", Score: 42.3}, {Match: "deepseek", Score: 32.5}, {Match: "claude", Score: 55}}
	q := terraWellBehindPace()
	q.Providers = append(q.Providers, protocol.QuotaProvider{Provider: "anthropic", Windows: []protocol.QuotaWindow{{Kind: "weekly", PercentUsed: 10, ResetsAt: q.GeneratedAt + 84*3600}}})
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	node := &protocol.NodeView{Hostname: "box", Harnesses: []protocol.HarnessInfo{{Name: "codex", SignedIn: "yes"}, {Name: "opencode", SignedIn: "yes"}}}
	counts := map[string]int{}
	for i := 0; i < 20; i++ {
		got, err := g.Choose(ChooseRequest{Purpose: model.PurposeWorkerAutomatic, Node: node, Candidates: candidates,
			Difficulty: "normal", D: protocol.PickerD(nil, "normal"), Scores: scores, Quotas: q, Protected: protocol.ProtectedProviders{Providers: []string{"anthropic"}}})
		if err != nil {
			t.Fatal(err)
		}
		counts[got.Choice.Name]++
	}
	if counts["claude"] != 0 || counts["terra"] == 0 {
		t.Fatalf("protected provider appeared, or no ordinary model ran: %v", counts)
	}
}

// terraWellBehindPace puts codex thirty points behind its weekly pace, enough
// to outweigh the over-qualification penalty that favours weaker adequate models.
func terraWellBehindPace() *protocol.Quotas {
	const now = int64(1000000)
	q := &protocol.Quotas{GeneratedAt: now, Providers: []protocol.QuotaProvider{{Provider: "codex", Windows: []protocol.QuotaWindow{{Kind: "weekly", PercentUsed: 20, ResetsAt: now + 84*3600}}}}}
	return q
}

func TestWorkerRotationSurvivesStoreRestart(t *testing.T) {
	path := seededStorePath(t, "gw.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	rotation := protocol.WorkerRotation{Levels: map[string]*protocol.WorkerRotationLevel{
		"normal": {Current: map[string]float64{"codex\x00terra": 1.25}, Recent: []string{"codex\x00terra", "opencode\x00deepseek"}},
	}}
	if err := s.saveWorkerRotation(rotation); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.WorkerRotation()
	if err != nil {
		t.Fatal(err)
	}
	level := got.Levels["normal"]
	if level == nil || level.Current["codex\x00terra"] != 1.25 || len(level.Recent) != 2 || level.Recent[1] != "opencode\x00deepseek" {
		t.Fatalf("rotation after restart: %+v", got)
	}
	if hard := got.Levels["hard"]; hard != nil {
		t.Fatalf("rotation leaked to another difficulty: %+v", hard)
	}
}
