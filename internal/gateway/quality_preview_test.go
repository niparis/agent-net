package gateway

import (
	"reflect"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestQualityPreviewPaceClause(t *testing.T) {
	for _, tc := range []struct{ note, want string }{
		{"codex 5% ahead of pace (weekly, 40% used at 35% elapsed)", "codex 5% ahead of pace"},
		{"codex on pace (weekly, 40% used at 40% elapsed)", ""},
		{"", ""},
	} {
		if got := qualityPaceClause(tc.note); got != tc.want {
			t.Errorf("%q: got %q want %q", tc.note, got, tc.want)
		}
	}
}

func TestQualityPreviewMatchesStartAndPreservesStoredRotation(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	candidates := []protocol.WorkerModelCandidate{{Harness: "codex", Model: "gpt-6.1-terra", DefaultEffort: "high", Provider: "codex"}, {Harness: "codex", Model: "gpt-6.1-sol", DefaultEffort: "high", Provider: "codex"}}
	scores := []protocol.ModelScore{{Match: "gpt-6.1-terra", Score: 51}, {Match: "gpt-6.1-sol", Score: 51}}
	quotas := &protocol.Quotas{GeneratedAt: 1000000, Providers: []protocol.QuotaProvider{{Provider: "codex", Windows: []protocol.QuotaWindow{{Kind: "weekly", PercentUsed: 55, ResetsAt: 1302400}}}}}
	before, err := s.WorkerRotation()
	if err != nil {
		t.Fatal(err)
	}
	preview, _, err := g.chooseAutomaticWorker(candidates, "hard", scores, nil, quotas, nil, protocol.ProtectedProviders{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := qualityPaceClause(preview.QuotaNote); got != "codex 5% ahead of pace" {
		t.Fatalf("pace note: %q", got)
	}
	after, err := s.WorkerRotation()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("preview persisted rotation")
	}
	start, _, err := g.chooseAutomaticWorker(candidates, "hard", scores, nil, quotas, nil, protocol.ProtectedProviders{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Model != start.Model || preview.Harness != start.Harness || preview.QuotaNote != start.QuotaNote {
		t.Fatalf("preview %+v, start %+v", preview, start)
	}
}
