package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/model"
	"github.com/niparis/agent-net/internal/protocol"
)

// protectedChoose asks the one gate for a level, with every candidate on a
// machine that has its harness.
func protectedChoose(t *testing.T, candidates []protocol.WorkerModelCandidate, level string, scores []protocol.ModelScore, quotas *protocol.Quotas, protected protocol.ProtectedProviders) (ChooseResult, error) {
	t.Helper()
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	node := &protocol.NodeView{Hostname: "box"}
	seen := map[string]bool{}
	for _, c := range candidates {
		if !seen[c.Harness] {
			seen[c.Harness] = true
			node.Harnesses = append(node.Harnesses, protocol.HarnessInfo{Name: c.Harness, SignedIn: "yes"})
		}
	}
	return g.Choose(ChooseRequest{Purpose: model.PurposeWorkerAutomatic, Node: node, Candidates: candidates,
		Difficulty: level, D: protocol.PickerD(nil, level), Scores: scores, Quotas: quotas, Protected: protected})
}

func TestProtectedChooser(t *testing.T) {
	candidates := []protocol.WorkerModelCandidate{{Harness: "opencode", Model: "anthropic/claude-sonnet"}, {Harness: "opencode", Model: "opencode-go/luna"}}
	scores := []protocol.ModelScore{{Match: "anthropic/claude-sonnet", Score: 55}, {Match: "opencode-go/luna", Score: 45}}
	for _, level := range []string{"normal", "hard"} {
		got, err := protectedChoose(t, candidates, level, scores, nil, protocol.ProtectedProviders{Providers: []string{"anthropic"}, Reason: "orchestrator"})
		if err != nil || got.Choice.Model != "opencode-go/luna" || (strings.Contains(got.Note, "skipped") != (level == "hard")) {
			t.Fatalf("%s: %+v %q %v", level, got.Choice, got.Note, err)
		}
	}
	scores[1].Score = 10
	got, err := protectedChoose(t, candidates, "hard", scores, nil, protocol.ProtectedProviders{Providers: []string{"anthropic"}, Reason: "orchestrator"})
	if err != nil || got.Choice.Model != "anthropic/claude-sonnet" || !strings.Contains(got.Note, "orchestrator") {
		t.Fatalf("fallback: %+v %q %v", got.Choice, got.Note, err)
	}
}

func TestProtectedChooserQuotaFallback(t *testing.T) {
	candidates := []protocol.WorkerModelCandidate{{Harness: "opencode", Model: "anthropic/claude-sonnet"}, {Harness: "opencode", Model: "opencode-go/luna"}}
	scores := []protocol.ModelScore{{Match: "claude-sonnet", Score: 55}, {Match: "luna", Score: 45}}
	q := &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "opencode-go", Windows: []protocol.QuotaWindow{{Exhausted: true}}}}}
	got, err := protectedChoose(t, candidates, "hard", scores, q, protocol.ProtectedProviders{Providers: []string{"anthropic"}, Reason: "keep limit"})
	if err != nil || got.Choice.Model != candidates[0].Model || !strings.Contains(got.Note, "keep limit") {
		t.Fatalf("fallback: %+v %q %v", got.Choice, got.Note, err)
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

func TestWorkerChoiceNote(t *testing.T) {
	choice := protocol.WorkerModelChoice{WorkerModelCandidate: protocol.WorkerModelCandidate{Harness: "opencode", Model: "deepseek"}, Score: 32.5, Fit: "good", QuotaNote: "opencode-go 20% behind pace", Weight: 1.34, ShareNote: "deepseek: 3, terra: 2 of the last 5 normal starts"}
	got := workerChoiceNote("normal", choice, []protocol.WorkerModelCandidate{{Harness: "codex", Model: "terra"}}, []protocol.ModelScore{{Match: "terra", Score: 42.3}}, nil)
	for _, want := range []string{"32.5, good enough", "20% behind pace", "rotation weight 1.34", "deepseek: 3, terra: 2 of the last 5 normal starts", "terra kept for harder work"} {
		if !strings.Contains(got, want) {
			t.Errorf("note %q lacks %q", got, want)
		}
	}
}

func TestWorkerChoiceNotePoorRecord(t *testing.T) {
	choice := protocol.WorkerModelChoice{WorkerModelCandidate: protocol.WorkerModelCandidate{Model: "strong"}, Score: 42}
	c := []protocol.WorkerModelCandidate{{Model: "cheap", Track: protocol.TrackRecord{Complete: 1, Partial: 4}}}
	if got := workerChoiceNote("normal", choice, c, nil, nil); !strings.Contains(got, "cheap has a poor track record") {
		t.Fatalf("note %q", got)
	}
}

// Claude models through agy are not the owner's Anthropic subscription, so the
// protected rule must leave them alone (decision 0049 D4).
func TestProtectedChooserIgnoresAgyClaudeModels(t *testing.T) {
	protected := protocol.ProtectedProviders{Providers: []string{"anthropic"}, Reason: "orchestrator"}
	if isProtected("agy", "claude-sonnet-5-5-medium", protected) {
		t.Fatal("an agy Claude model is treated as protected Anthropic")
	}
	if !isProtected("opencode", "anthropic/claude-sonnet-4", protected) {
		t.Fatal("an OpenCode Anthropic model is no longer protected")
	}
	candidates := []protocol.WorkerModelCandidate{{Name: "sonnet agy", Harness: "agy", Model: "claude-sonnet-5-5-medium"}}
	scores := []protocol.ModelScore{{Match: "claude-sonnet-5-5-medium", Score: 55}}
	got, err := protectedChoose(t, candidates, "very_complex", scores, nil, protected)
	if err != nil || got.Choice.Model != "claude-sonnet-5-5-medium" || strings.Contains(got.Note, "orchestrator") {
		t.Fatalf("choice: %+v %q %v", got.Choice, got.Note, err)
	}
}
