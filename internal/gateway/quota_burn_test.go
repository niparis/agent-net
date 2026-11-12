package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// burnGateway is a gateway with a fixed clock and one machine.
func burnGateway(t *testing.T) (*Gateway, string, *clock) {
	t.Helper()
	g, node := scopeGateway(t)
	c := &clock{t: time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)}
	g.store.now = c.now
	return g, node, c
}

// finishedWorker records an ended worker session with its tokens and one run.
func finishedWorker(t *testing.T, g *Gateway, node, harness, model string, tokens int64, start, end time.Time) string {
	t.Helper()
	id, err := g.store.UpsertSession(node, protocol.SessionInfo{Harness: harness, NativeID: "n" + start.String() + model, Model: model,
		Worker: &protocol.WorkerInfo{ID: "w", Tokens: tokens, StartedAt: start.Unix()}})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.store.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: id, Harness: harness, Model: model,
		Outcome: protocol.OutcomeComplete, StartedAt: start.Unix(), EndedAt: end.Unix()}); err != nil {
		t.Fatal(err)
	}
	if err := g.store.SetWorkerSessionEnded(node, id); err != nil {
		t.Fatal(err)
	}
	return id
}

// A quota failure teaches the size of the window: the tokens that the provider's
// workers used in the window before it.
func TestAgyQuotaFailureLearnsTheBudget(t *testing.T) {
	g, node, c := burnGateway(t)
	// Two agy workers used 1.5M and 2.5M tokens in the last day. A third one
	// ran a month ago and is outside any window.
	finishedWorker(t, g, node, "agy", "gemini-4-argon-high", 1_500_000, c.t.Add(-3*time.Hour), c.t.Add(-2*time.Hour))
	finishedWorker(t, g, node, "agy", "gemini-4-argon-high", 2_500_000, c.t.Add(-2*time.Hour), c.t.Add(-time.Hour))
	finishedWorker(t, g, node, "agy", "gemini-4-argon-high", 9_000_000, c.t.Add(-40*24*time.Hour), c.t.Add(-40*24*time.Hour+time.Hour))
	// A worker of another provider does not count.
	finishedWorker(t, g, node, "codex", "gpt-6.1-sol", 7_000_000, c.t.Add(-2*time.Hour), c.t.Add(-time.Hour))

	failing, err := g.store.UpsertSession(node, protocol.SessionInfo{Harness: "agy", NativeID: "failing", Model: "gemini-4-argon-high",
		Worker: &protocol.WorkerInfo{ID: "w_fail", Tokens: 1_000_000, StartedAt: c.t.Add(-30 * time.Minute).Unix()}})
	if err != nil {
		t.Fatal(err)
	}
	nc := &nodeConn{id: node}
	raw, _ := json.Marshal(protocol.WorkerStopped{SessionID: failing, WorkerID: "w_fail", Project: "alpha", Harness: "agy", Model: "gemini-4-argon-high",
		StartedAt: c.t.Add(-30 * time.Minute).Unix(), Reason: protocol.StopRunFailed,
		Failure: "Antigravity result ERROR error: Individual quota reached. Resets in 134h28m52s."})
	if _, err := g.handleWorker(context.Background(), nc, "worker.stopped", raw); err != nil {
		t.Fatal(err)
	}
	budgets, err := g.store.quotaBudgets("agy")
	if err != nil || len(budgets) != 1 {
		t.Fatalf("budgets %v, %v", budgets, err)
	}
	// The reset is 134 hours away: a weekly window. The failing run's own
	// 1M tokens count, the run from 40 days ago and the codex run do not.
	if want := int64(5_000_000); budgets[0].WindowSeconds != int64(7*24*time.Hour/time.Second) || budgets[0].Tokens != want {
		t.Fatalf("learned %+v, want a weekly window of %d tokens", budgets[0], want)
	}
	if other, _ := g.store.quotaBudgets("codex"); len(other) != 0 {
		t.Fatalf("another provider learned %v", other)
	}
}

// Running workers on every machine make the burn of a provider, with the
// budget it learned, and the picker holds the next start when it would not last.
func TestWithQuotaBurnHoldsTheNextStartOnAProviderThatWouldRunOut(t *testing.T) {
	g, node, c := burnGateway(t)
	if _, err := g.store.db.Exec(`INSERT INTO quota_budgets (provider, window_seconds, tokens, at) VALUES ('codex', ?, 4000000, 1)`, int64(5*time.Hour/time.Second)); err != nil {
		t.Fatal(err)
	}
	// Finished workers set how long a run lasts: 30 minutes.
	finishedWorker(t, g, node, "codex", "gpt-6.1-sol", 100, c.t.Add(-9*time.Hour), c.t.Add(-9*time.Hour+30*time.Minute))
	for i := 0; i < 5; i++ {
		id, err := g.store.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "run" + string(rune('a'+i)), Model: "gpt-6.1-sol", Status: "busy",
			Worker: &protocol.WorkerInfo{ID: "w" + string(rune('a'+i)), Tokens: 600_000, StartedAt: c.t.Add(-20 * time.Minute).Unix()}})
		if err != nil {
			t.Fatal(err)
		}
		_ = id
	}
	agents, err := g.store.Agents(protocol.QueryAgents{OnlineOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	q := g.withQuotaBurn(nil, agents)
	reason, hold := protocol.QuotaBurnHold(q, "codex", "gpt-6.1-sol")
	if !hold || !strings.Contains(reason, "learned") || !strings.Contains(reason, "5 running") {
		t.Fatalf("hold=%v reason=%q", hold, reason)
	}
	// Another provider has no running worker: nothing to hold.
	if reason, hold := protocol.QuotaBurnHold(q, "agy", ""); hold {
		t.Fatalf("agy held: %s", reason)
	}
	// A provider with plenty of quota is unaffected: a larger budget lets the start go.
	if _, err := g.store.db.Exec(`UPDATE quota_budgets SET tokens = 400000000`); err != nil {
		t.Fatal(err)
	}
	q = g.withQuotaBurn(nil, agents)
	if reason, hold := protocol.QuotaBurnHold(q, "codex", "gpt-6.1-sol"); hold {
		t.Fatalf("plenty of quota held: %s", reason)
	}
	// With no worker running, the store is not read and the reading is returned as it is.
	if got := g.withQuotaBurn(nil, nil); got != nil {
		t.Fatalf("no running worker changed the reading: %+v", got)
	}
}

// Five Codex workers burn the learned budget of the 5 hour window. The next
// automatic start goes to another provider and says why. A start that can run
// only on Codex is refused with the hold, and a Codex start with plenty of
// budget left is not touched.
func TestAutomaticStartLeavesAProviderThatWouldRunOut(t *testing.T) {
	g, node, owner, _, c := setupFloorGateway(t)
	nc := g.conns[node]
	if _, err := g.store.db.Exec(`INSERT INTO quota_budgets (provider, window_seconds, tokens, at) VALUES ('codex', ?, 4000000, 1)`, int64(5*time.Hour/time.Second)); err != nil {
		t.Fatal(err)
	}
	finishedWorker(t, g, node, "codex", "gpt-6.1-terra", 100, c.t.Add(-9*time.Hour), c.t.Add(-9*time.Hour+30*time.Minute))
	for i := 0; i < 5; i++ {
		if _, err := g.store.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "run" + string(rune('a'+i)), Model: "gpt-6.1-terra", Status: "busy",
			Worker: &protocol.WorkerInfo{ID: "w" + string(rune('a'+i)), Tokens: 600_000, StartedAt: c.t.Add(-20 * time.Minute).Unix()}}); err != nil {
			t.Fatal(err)
		}
	}
	start := func(harness string) (protocol.WorkerStartAnswer, error) {
		return g.workerStart(context.Background(), nc, protocol.WorkerStartFrame{Owner: owner, Level: "hard",
			WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: "test", Harness: harness, Model: "auto"}}, true)
	}
	for i := 0; i < 4; i++ {
		ans, err := start("")
		if err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
		if ans.Harness == "codex" {
			t.Fatalf("start %d went to codex, which would run out: %+v", i, ans)
		}
		if !strings.Contains(ans.ModelNote, "held back for quota: codex") {
			t.Fatalf("start %d note lacks the hold: %q", i, ans.ModelNote)
		}
	}
	_, err := start("codex")
	if err == nil || !strings.Contains(err.Error(), "held back for quota: codex") {
		t.Fatalf("a codex-only start must be refused with the hold: %v", err)
	}
	// The budget is large: Codex is not touched.
	if _, err := g.store.db.Exec(`UPDATE quota_budgets SET tokens = 400000000`); err != nil {
		t.Fatal(err)
	}
	ans, err := start("codex")
	if err != nil || ans.Harness != "codex" {
		t.Fatalf("a provider with plenty of quota must start: %+v %v", ans, err)
	}
	if strings.Contains(ans.ModelNote, "held back") {
		t.Fatalf("no hold expected: %q", ans.ModelNote)
	}
}
