package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A queued automatic start passes the gate again when it leaves the queue: the
// recheck answers with a model that can run now, not the model it queued with
// (decision 0162).
func TestQueuedRecheckChoosesAgainThroughTheGate(t *testing.T) {
	g, node, owner, _, c := setupFloorGateway(t)
	quotas := &protocol.Quotas{
		GeneratedAt: c.now().Unix(),
		Providers: []protocol.QuotaProvider{
			{Provider: "claude", Windows: []protocol.QuotaWindow{{ID: "5h", Kind: "five_hour", Exhausted: true, ResetsAt: c.now().Add(2 * 3600).Unix()}}},
			{Provider: "codex", Windows: []protocol.QuotaWindow{{ID: "weekly", Kind: "weekly", PercentUsed: 20, ResetsAt: c.now().Add(84 * 3600).Unix()}}},
			{Provider: "deepseek", Windows: []protocol.QuotaWindow{{ID: "5h", Kind: "five_hour", PercentUsed: 20, ResetsAt: c.now().Add(2 * 3600).Unix()}}},
		},
	}
	g.usage.set(node, c.now(), protocol.UsageReport{Quotas: quotas})
	// The queued start chose deepseek; the gate must try that model first,
	// not take a fresh rotation step away from it.
	request := protocol.WorkerRecheck{Ref: "q_recheck", Owner: owner, Project: "alpha", Difficulty: "hard",
		Harness: "opencode", Model: "deepseek/deepseek-v4.1-flash:high"}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	answer, err := g.handleWorker(context.Background(), &nodeConn{id: node}, "worker.recheck", raw)
	if err != nil {
		t.Fatalf("worker.recheck: %v", err)
	}
	got, ok := answer.(protocol.WorkerRecheckAnswer)
	if !ok {
		t.Fatalf("answer type %T", answer)
	}
	if got.Refused {
		t.Fatalf("recheck refused: %s", got.Reason)
	}
	if got.Harness != "opencode" || !strings.HasPrefix(got.Model, "deepseek/deepseek-v4.1-flash") {
		t.Fatalf("recheck moved away from the queued model that can still run: %+v", got)
	}
	// The pace order would pick deepseek now; a start queued on terra keeps
	// terra, because the gate only checks the model it already has.
	request = protocol.WorkerRecheck{Ref: "q_recheck", Owner: owner, Project: "alpha", Difficulty: "hard",
		Harness: "codex", Model: "gpt-6.1-terra:high"}
	raw, err = json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	answer, err = g.handleWorker(context.Background(), &nodeConn{id: node}, "worker.recheck", raw)
	if err != nil {
		t.Fatalf("worker.recheck terra: %v", err)
	}
	got = answer.(protocol.WorkerRecheckAnswer)
	if got.Refused || got.Harness != "codex" {
		t.Fatalf("recheck did not keep the queued terra: %+v", got)
	}
}
