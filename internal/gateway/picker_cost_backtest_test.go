package gateway

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// backtestFixture is two models that finish the same kind of task. Opus does it
// well at a high price, DeepSeek nearly as well at a tiny one. Each session's
// usage comes only from the "tokens:" line of a stored notice, as for runs that
// ended before the gateway kept usage by kind.
func backtestFixture(t *testing.T) (*Store, *protocol.PriceBook) {
	t.Helper()
	r := newDeliveryRig(t)
	s := r.s
	book := protocol.NewPriceBook([]protocol.ModelPrice{
		{ID: "anthropic/claude-opus-5.5", Prompt: 4e-6, Completion: 2e-5, CacheRead: 2e-7},
		{ID: "deepseek/deepseek-v4.1-flash-1017", Prompt: 3e-7, Completion: 1.2e-6, CacheRead: 6e-9},
	})
	if err := s.saveWorkerProfiles([]protocol.WorkerProfile{
		{Name: "opus", Harness: "claude", Model: "claude-opus-5-5", Effort: "high"},
		{Name: "deepseek", Harness: "opencode", Model: "alibaba-token-plan/deepseek-v4.1-flash-1017", Effort: "high"},
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		model, harness := "claude-opus-5-5:high", "claude"
		if i%2 == 1 {
			model, harness = "alibaba-token-plan/deepseek-v4.1-flash-1017:high", "opencode"
		}
		session := fmt.Sprintf("s_back_%d", i)
		// Two notices of one session: the later, larger count stands.
		for n, tokens := range []string{"1000 in, 200 out", "100000 in, 20000 out, 3000000 cache read, 0 cache write"} {
			body := "[agent-net worker notice]\nstatus: done\nworker: " + session + " (" + harness + ", " + model + "), project p on h\nrun time: 1m\ntokens: " + tokens + "\n"
			if _, err := s.db.Exec(`INSERT INTO messages(id,conversation_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state) VALUES(?,?,?,?,?,?,0,?,?,?,?)`,
				fmt.Sprintf("m_back_%d_%d", i, n), "c_back", "agent-net", "o", "notice", body, fmt.Sprintf("k_back_%d_%d", i, n), int64(i*10+n), int64(1<<40), "delivered"); err != nil {
				t.Fatal(err)
			}
		}
		pickerRun(t, s, r.a, session, harness, model, protocol.OutcomeComplete, 0, int64(1000+i))
		pickerAssessment(t, s, session, 2.5)
	}
	return s, book
}

func TestPickerCostBacktestPricesTasksFromNoticeTokens(t *testing.T) {
	s, book := backtestFixture(t)
	res, err := s.PickerCostBacktest(40, PickerCostBacktestOptions{Prices: book})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 8 || res.Tasks != 8 || res.TasksWithUsage != 8 || res.Prices != 2 {
		t.Fatalf("result: %+v", res)
	}
	for i, row := range res.Rows {
		want := 100000*4e-6 + 20000*2e-5 + 3e6*2e-7 // opus
		if i%2 == 1 {
			want = 100000*3e-7 + 20000*1.2e-6 + 3e6*6e-9
		}
		if row.ThenCost < want*.999 || row.ThenCost > want*1.001 {
			t.Errorf("row %d cost then %v, want %v", i, row.ThenCost, want)
		}
		if row.Now != "alibaba-token-plan/deepseek-v4.1-flash-1017:high" || row.NowCost <= 0 {
			t.Errorf("row %d now: %+v", i, row)
		}
	}
}

// Today's worker floors apply: a provider under its floor cannot be picked, so
// the next cheapest model takes the work.
func TestPickerCostBacktestAppliesTheWorkerFloors(t *testing.T) {
	s, book := backtestFixture(t)
	raw, _ := json.Marshal(protocol.WorkerFloors{Floors: map[string]int{"opencode": 20, "alibaba": 20}})
	if _, err := s.db.Exec(`INSERT INTO owner_settings (key,value,updated_at) VALUES (?,?,?) ON CONFLICT (key) DO UPDATE SET value=excluded.value`, workerFloorsKey, string(raw), 1); err != nil {
		t.Fatal(err)
	}
	// The deepseek profile's provider has 14 percent left.
	provider := protocol.QuotaProviderForStart("opencode", "alibaba-token-plan/deepseek-v4.1-flash-1017")
	if provider == "" {
		t.Skip("the fixture's model has no quota provider")
	}
	q := &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: provider, Windows: []protocol.QuotaWindow{{ID: "weekly", Kind: "weekly", PercentUsed: 86}}}}}
	res, err := s.PickerCostBacktest(40, PickerCostBacktestOptions{Prices: book, Quotas: q})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.OutByFloor) != 1 || !strings.Contains(res.OutByFloor[0], "deepseek") {
		t.Fatalf("out by floor: %v", res.OutByFloor)
	}
	for _, row := range res.Rows {
		if strings.Contains(row.Now, "deepseek") {
			t.Fatalf("a profile under its floor was picked: %+v", row)
		}
	}
}

// backtestQuotasFromEnv reads the quota reading the live picker would have: a
// Quotas JSON file, or "provider=percent used" pairs.
func backtestQuotasFromEnv(t *testing.T) *protocol.Quotas {
	t.Helper()
	if path := os.Getenv("AGENT_NET_PICKER_BACKTEST_QUOTAS"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var q protocol.Quotas
		if err := json.Unmarshal(raw, &q); err != nil {
			t.Fatal(err)
		}
		return &q
	}
	used := os.Getenv("AGENT_NET_PICKER_BACKTEST_USED")
	if used == "" {
		return nil
	}
	q := &protocol.Quotas{}
	for _, pair := range strings.Split(used, ",") {
		name, pct, ok := strings.Cut(strings.TrimSpace(pair), "=")
		v, err := strconv.ParseFloat(pct, 64)
		if !ok || err != nil {
			t.Fatalf("AGENT_NET_PICKER_BACKTEST_USED wants provider=percent pairs, got %q", pair)
		}
		q.Providers = append(q.Providers, protocol.QuotaProvider{Provider: name, Windows: []protocol.QuotaWindow{{ID: "weekly", Kind: "weekly", PercentUsed: v}}})
	}
	return q
}

// TestPickerCostBacktestReport prints the backtest of a copy of a real gateway
// database. It never opens the live database. It skips without the variable.
//
//	AGENT_NET_PICKER_BACKTEST_DB      a copy of gateway.db (required)
//	AGENT_NET_PICKER_BACKTEST_PRICES  a saved OpenRouter /api/v1/models JSON
//	AGENT_NET_PICKER_BACKTEST_USED    percent used per quota provider, for the
//	                                  worker floors, e.g. "codex=86,claude=40"
//	AGENT_NET_PICKER_BACKTEST_QUOTAS  or a Quotas JSON file instead of USED
//
// go test ./internal/gateway -run '^TestPickerCostBacktestReport$' -v
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
	var opts PickerCostBacktestOptions
	if p := os.Getenv("AGENT_NET_PICKER_BACKTEST_PRICES"); p != "" {
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		list, err := parseOpenRouterPrices(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		opts.Prices = protocol.NewPriceBook(list)
	}
	opts.Quotas = backtestQuotasFromEnv(t)
	res, err := s.PickerCostBacktest(40, opts)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("prices: %d models; tasks with a D: %d, of them with tokens on record: %d; quota reading: %v\n", res.Prices, res.Tasks, res.TasksWithUsage, opts.Quotas != nil)
	for _, line := range res.OutByFloor {
		fmt.Println("out by floor:", line)
	}
	var then, now float64
	fmt.Printf("%-3s %-5s %-46s %10s  %-46s %10s\n", "#", "D", "chosen then", "cost then", "cost rule now", "cost now")
	for i, r := range res.Rows {
		fmt.Printf("%-3d %-5.2f %-46s %10.4f  %-46s %10.4f\n", i+1, r.D, r.Then, r.ThenCost, r.Now, r.NowCost)
		then += r.ThenCost
		now += r.NowCost
	}
	fmt.Printf("total then $%.2f, now (expected per run) $%.2f over %d tasks\n", then, now, len(res.Rows))
}
