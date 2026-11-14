package gateway

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A start whose gate skips the cell rule's first pick must still explain the
// pick with the cell rule: the bucket and the per-candidate expected costs. The
// old code cleared that note when the gate moved to the next candidate, so the
// start read like the old level rule (decision 0161, decision 0162).
func TestAutomaticStartKeepsTheCellNoteWhenTheGateSkipsTheFirstPick(t *testing.T) {
	g, node, owner, _, c := setupFloorGateway(t)
	s := g.store

	for _, price := range []struct {
		id                    string
		prompt, completion    float64
		cacheRead, cacheWrite float64
	}{
		{"anthropic/claude-opus-5.5", 4e-6, 2e-5, 2e-7, 0},
		{"openai/gpt-6.1-terra", 2e-6, 1e-5, 1e-7, 0},
		{"deepseek/deepseek-v4.1-flash", 3e-7, 1.2e-6, 6e-9, 0},
	} {
		if _, err := s.db.Exec(`INSERT OR REPLACE INTO model_prices(id,prompt,completion,cache_read,cache_write,updated_at) VALUES(?,?,?,?,?,0)`,
			price.id, price.prompt, price.completion, price.cacheRead, price.cacheWrite); err != nil {
			t.Fatal(err)
		}
	}

	lo, hi, ok := protocol.LevelDRange("hard")
	if !ok {
		t.Fatal("no D range for hard")
	}
	d := (lo + hi) / 2
	addTasks := func(model string, in, out float64) {
		t.Helper()
		for i := 0; i < 5; i++ {
			session := fmt.Sprintf("s_%s_%d", strings.ReplaceAll(model, "/", "_"), i)
			pickerRun(t, s, node, session, "opencode", model, protocol.OutcomeComplete, 0, int64(1000+i))
			pickerAssessment(t, s, session, d)
			if _, err := s.db.Exec(`INSERT OR REPLACE INTO worker_usage(session_id,usage,updated_at) VALUES(?,?,0)`,
				session, fmt.Sprintf(`{"in":%g,"out":%g}`, in, out)); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Terra is dear and DeepSeek is cheap, so the cost rule picks DeepSeek.
	addTasks("gpt-6.1-terra:high", 100_000, 20_000)
	addTasks("deepseek/deepseek-v4.1-flash:high", 100_000, 20_000)

	// DeepSeek's provider is under the worker floor, so the gate must skip the
	// cost rule's pick and take the next candidate.
	if _, err := s.db.Exec(`INSERT INTO owner_settings (key,value,updated_at) VALUES (?,?,0) ON CONFLICT (key) DO UPDATE SET value=excluded.value`,
		workerFloorsKey, `{"floors":{"deepseek":20}}`); err != nil {
		t.Fatal(err)
	}
	g.usage.set(node, c.now(), protocol.UsageReport{Quotas: &protocol.Quotas{
		GeneratedAt: c.now().Unix(),
		Providers: []protocol.QuotaProvider{
			{Provider: "deepseek", Windows: []protocol.QuotaWindow{{ID: "weekly", Kind: "weekly", PercentUsed: 90, ResetsAt: c.now().Add(72 * 3600).Unix()}}},
			{Provider: "codex", Windows: []protocol.QuotaWindow{{ID: "weekly", Kind: "weekly", PercentUsed: 20, ResetsAt: c.now().Add(72 * 3600).Unix()}}},
		},
	}})

	ans, err := g.workerStart(context.Background(), g.conns[node], protocol.WorkerStartFrame{
		Owner: owner,
		Level: "hard",
		WorkerStartRequest: protocol.WorkerStartRequest{
			Project: "alpha",
			Task:    "test",
			Model:   "auto",
		},
	}, true)
	if err != nil {
		t.Fatalf("workerStart: %v", err)
	}
	if !strings.Contains(ans.Model, "terra") {
		t.Fatalf("the start did not move to the next candidate: %+v", ans)
	}
	for _, want := range []string{"bucket", "lowest expected cost", "deepseek", "terra", "$", "expected"} {
		if !strings.Contains(ans.ModelNote, want) {
			t.Errorf("model note lacks %q: %s", want, ans.ModelNote)
		}
	}
	if strings.Contains(ans.ModelNote, "good enough; no quota pace reading") {
		t.Errorf("model note fell back to the old level rule: %s", ans.ModelNote)
	}
}