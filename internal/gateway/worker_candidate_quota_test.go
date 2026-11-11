package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestWorkerCandidateUnknownQuotaStaysCandidate(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	profiles := []protocol.WorkerProfile{{
		Name:    "deepseek alibaba high",
		Harness: "opencode",
		Model:   "alibaba-token-plan/deepseek-v4.1-flash-1017",
		Effort:  "high",
	}}
	n := &protocol.NodeView{
		NodeID:   "research",
		Hostname: "research",
		Harnesses: []protocol.HarnessInfo{{
			Name:     "opencode",
			SignedIn: "yes",
			Models:   []string{"alibaba-token-plan/deepseek-v4.1-flash-1017"},
		}},
	}

	quotas := &protocol.Quotas{
		Providers: []protocol.QuotaProvider{
			{Provider: "alibaba", Error: "bl_usage_failed: No console access token found"},
		},
	}

	candidates, skipped, _, err := g.workerModelCandidates(n, profiles, protocol.WorkerProviders{}, quotas, "", false, protocol.ProtectedProviders{})
	if err != nil {
		t.Fatalf("workerModelCandidates error: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Name != "deepseek alibaba high" {
		t.Fatalf("expected candidate 'deepseek alibaba high', got candidates=%v, skipped=%v", candidates, skipped)
	}
}

func TestWorkerCandidateExhaustedQuotaExcluded(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	profiles := []protocol.WorkerProfile{{
		Name:    "deepseek alibaba high",
		Harness: "opencode",
		Model:   "alibaba-token-plan/deepseek-v4.1-flash-1017",
		Effort:  "high",
	}}
	n := &protocol.NodeView{
		NodeID:   "research",
		Hostname: "research",
		Harnesses: []protocol.HarnessInfo{{
			Name:     "opencode",
			SignedIn: "yes",
			Models:   []string{"alibaba-token-plan/deepseek-v4.1-flash-1017"},
		}},
	}

	quotas := &protocol.Quotas{
		Providers: []protocol.QuotaProvider{
			{
				Provider: "alibaba",
				Windows:  []protocol.QuotaWindow{{ID: "monthly", Exhausted: true}},
			},
		},
	}

	candidates, _, _, err := g.workerModelCandidates(n, profiles, protocol.WorkerProviders{}, quotas, "", false, protocol.ProtectedProviders{})
	if err != nil {
		t.Fatalf("workerModelCandidates error: %v", err)
	}
	_, _, err = g.chooseAutomaticWorker(candidates, "normal", nil, nil, quotas, nil, protocol.ProtectedProviders{}, false)
	if err == nil {
		t.Fatalf("expected exhausted quota to exclude candidate, got nil error")
	}
}
