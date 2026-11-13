package gateway

import (
	"fmt"
	"os"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// Two models finish the same kind of task. Opus does it well at a high price,
// DeepSeek nearly as well at a tiny one. Before the cost rule, the better score
// won; the backtest shows the cost rule now takes DeepSeek for the same work.
func TestPickerCostBacktestPicksTheCheaperModelForPastTasks(t *testing.T) {
	r := newDeliveryRig(t)
	s := r.s
	if err := s.ReplacePrices([]protocol.ModelPrice{
		{ID: "anthropic/claude-opus-5.5", Prompt: 4e-6, Completion: 2e-5, CacheRead: 2e-7},
		{ID: "deepseek/deepseek-v4.1-flash-1017", Prompt: 3e-7, Completion: 1.2e-6, CacheRead: 6e-9},
	}); err != nil {
		t.Fatal(err)
	}
	profiles := []protocol.WorkerProfile{
		{Name: "opus", Harness: "claude", Model: "claude-opus-5-5", Effort: "high"},
		{Name: "deepseek", Harness: "opencode", Model: "alibaba-token-plan/deepseek-v4.1-flash-1017", Effort: "high"},
	}
	if err := s.saveWorkerProfiles(profiles); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		model, harness := "claude-opus-5-5:high", "claude"
		if i%2 == 1 {
			model, harness = "alibaba-token-plan/deepseek-v4.1-flash-1017:high", "opencode"
		}
		session := fmt.Sprintf("s_back_%d", i)
		if _, err := s.db.Exec(`INSERT INTO worker_usage(session_id,usage,updated_at) VALUES(?,?,?)`, session, `{"in":100000,"out":20000,"cache_read":3000000}`, 1); err != nil {
			t.Fatal(err)
		}
		pickerRun(t, s, r.a, session, harness, model, protocol.OutcomeComplete, 0, int64(1000+i))
		pickerAssessment(t, s, session, 2.5)
	}
	rows, err := s.PickerCostBacktest(40)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 8 {
		t.Fatalf("%d rows, want 8", len(rows))
	}
	for _, row := range rows {
		if row.Now != "alibaba-token-plan/deepseek-v4.1-flash-1017:high" || row.NowCost <= 0 || row.ThenCost <= 0 {
			t.Fatalf("row: %+v", row)
		}
	}
}

// TestPickerCostBacktestReport prints the backtest of a copy of a real gateway
// database: AGENT_NET_PICKER_BACKTEST_DB=/path/to/copy/gateway.db go test
// ./internal/gateway -run TestPickerCostBacktestReport -v. It never opens the live
// database; give it a copy. It skips without the variable.
func TestPickerCostBacktestReport(t *testing.T) {
	path := os.Getenv("AGENT_NET_PICKER_BACKTEST_DB")
	if path == "" {
		t.Skip("set AGENT_NET_PICKER_BACKTEST_DB to a copy of gateway.db")
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.PickerCostBacktest(40)
	if err != nil {
		t.Fatal(err)
	}
	var then, now float64
	fmt.Printf("%-3s %-5s %-44s %9s  %-44s %9s\n", "#", "D", "chosen then", "cost then", "cost rule now", "cost now")
	for i, r := range rows {
		fmt.Printf("%-3d %-5.2f %-44s %9.4f  %-44s %9.4f\n", i+1, r.D, r.Then, r.ThenCost, r.Now, r.NowCost)
		then += r.ThenCost
		now += r.NowCost
	}
	fmt.Printf("total then $%.2f, now (expected) $%.2f over %d tasks\n", then, now, len(rows))
}
