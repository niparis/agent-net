package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestProtectedChooser(t *testing.T) {
	candidates := []protocol.WorkerModelCandidate{{Harness: "opencode", Model: "anthropic/claude-sonnet"}, {Harness: "opencode", Model: "opencode-go/luna"}}
	scores := []protocol.ModelScore{{Match: "anthropic/claude-sonnet", Score: 55}, {Match: "opencode-go/luna", Score: 45}}
	for _, level := range []string{"normal", "hard"} {
		c, note, err := chooseProtectedWorker(candidates, level, scores, nil, nil, nil, &protocol.WorkerRotation{}, protocol.ProtectedProviders{Providers: []string{"anthropic"}, Reason: "orchestrator"})
		if err != nil || c.Model != "opencode-go/luna" || !strings.Contains(note, "skipped") {
			t.Fatalf("%s: %+v %q %v", level, c, note, err)
		}
	}
	scores[1].Score = 10
	c, note, err := chooseProtectedWorker(candidates, "hard", scores, nil, nil, nil, &protocol.WorkerRotation{}, protocol.ProtectedProviders{Providers: []string{"anthropic"}, Reason: "orchestrator"})
	if err != nil || c.Model != "anthropic/claude-sonnet" || !strings.Contains(note, "orchestrator") {
		t.Fatalf("fallback: %+v %q %v", c, note, err)
	}
}

func TestProtectedChooserQuotaFallback(t *testing.T) {
	candidates := []protocol.WorkerModelCandidate{{Harness: "opencode", Model: "anthropic/claude-sonnet"}, {Harness: "opencode", Model: "opencode-go/luna"}}
	scores := []protocol.ModelScore{{Match: "claude-sonnet", Score: 55}, {Match: "luna", Score: 45}}
	q := &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "opencode-go", Windows: []protocol.QuotaWindow{{Exhausted: true}}}}}
	c, note, err := chooseProtectedWorker(candidates, "hard", scores, nil, q, nil, &protocol.WorkerRotation{}, protocol.ProtectedProviders{Providers: []string{"anthropic"}, Reason: "keep limit"})
	if err != nil || c.Model != candidates[0].Model || !strings.Contains(note, "keep limit") {
		t.Fatalf("fallback: %+v %q %v", c, note, err)
	}
}

// A protected provider stays out of the rotation while ordinary adequate models
// exist, and the ordinary models share the starts.
func TestProtectedChooserRotatesOrdinaryModels(t *testing.T) {
	candidates := []protocol.WorkerModelCandidate{
		{Name: "terra", Harness: "codex", Model: "gpt-6.1-terra", Provider: "codex"},
		{Name: "deepseek", Harness: "opencode", Model: "opencode-go/deepseek", Provider: "opencode-go"},
		{Name: "claude", Harness: "opencode", Model: "anthropic/claude-sonnet", Provider: "anthropic"},
	}
	scores := []protocol.ModelScore{{Match: "terra", Score: 42.3}, {Match: "deepseek", Score: 32.5}, {Match: "claude", Score: 55}}
	q := terraWellBehindPace()
	rotation := &protocol.WorkerRotation{}
	counts := map[string]int{}
	for i := 0; i < 20; i++ {
		c, _, err := chooseProtectedWorker(candidates, "normal", scores, nil, q, nil, rotation, protocol.ProtectedProviders{Providers: []string{"anthropic"}})
		if err != nil {
			t.Fatal(err)
		}
		counts[c.Name]++
	}
	if counts["claude"] != 0 {
		t.Fatalf("protected provider appeared: %v", counts)
	}
	if counts["terra"] == 0 || counts["deepseek"] == 0 || counts["terra"] <= counts["deepseek"] {
		t.Fatalf("ordinary models did not share the starts: %v", counts)
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
	for _, want := range []string{"normal -> opencode deepseek", "32.5, good enough", "20% behind pace", "rotation weight 1.34", "deepseek: 3, terra: 2 of the last 5 normal starts", "terra kept for harder work"} {
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
