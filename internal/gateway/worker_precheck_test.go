package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A start that names a model whose provider has no allowance left is refused
// by "worker.precheck" at once, with the reason and the picker's own choice,
// before the caller's daemon runs the difficulty judge.
func TestPrecheckRefusesExhaustedNamedModelWithAlternative(t *testing.T) {
	g, node := scopeGateway(t)
	st := g.store
	c := &clock{t: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)}
	st.now = c.now
	if err := st.SetNodeHello(node, "nas", "1"); err != nil {
		t.Fatal(err)
	}
	launch := true
	if err := st.SetNodeLaunch(node, &launch); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetNodeHarnesses(node, []protocol.HarnessInfo{{Name: "agy", Models: []string{"gemini-4-argon-high"}}, {Name: "claude", Models: []string{"claude-opus-5-5"}}}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddWorkerProfile(protocol.WorkerProfile{Name: "opus", Harness: "claude", Model: "claude-opus-5-5"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`INSERT INTO owner_settings (key,value,updated_at) VALUES (?,?,0)`, workerProvidersKey, `{"providers":["anthropic","google"]}`); err != nil {
		t.Fatal(err)
	}
	owner, err := st.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	nc := &nodeConn{id: node}
	g.conns[node] = nc
	t.Cleanup(func() { delete(g.conns, node) })
	if err := st.RecordQuotaBlock(node, protocol.QuotaBlock{Provider: "agy", Until: c.t.Add(48 * time.Hour)}, "Individual quota reached"); err != nil {
		t.Fatal(err)
	}
	precheck := func(harness, model string) error {
		t.Helper()
		raw, _ := json.Marshal(protocol.WorkerStartFrame{Owner: owner,
			WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: "test", Harness: harness, Model: model}})
		_, err := g.handleWorker(context.Background(), nc, "worker.precheck", raw)
		return err
	}
	err = precheck("agy", "gemini-4-argon-high")
	if err == nil || !strings.Contains(err.Error(), "agy quota used up until 2026-11-06 12:00 UTC") || !strings.Contains(err.Error(), "; try claude claude-opus-5-5") {
		t.Fatalf("named model of an exhausted provider: %v", err)
	}
	// A harness alone is refused the same way.
	err = precheck("agy", "")
	if err == nil || !strings.Contains(err.Error(), "; try claude claude-opus-5-5") {
		t.Fatalf("named harness of an exhausted provider: %v", err)
	}
	// A model that can run, and a start that names nothing, pass the check.
	if err := precheck("claude", "claude-opus-5-5"); err != nil {
		t.Fatalf("model that can run: %v", err)
	}
	if err := precheck("", ""); err != nil {
		t.Fatalf("start with no harness: %v", err)
	}
	// Nothing started: no start is left open at the gateway.
	if n, err := st.OpenStarts(); err != nil || n != 0 {
		t.Fatalf("open starts %d, %v", n, err)
	}
}
