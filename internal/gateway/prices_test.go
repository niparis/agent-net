package gateway

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A trimmed copy of OpenRouter's /models answer: string prices, a ':batch'
// entry, a router with a negative price and a model with no cache write price.
const openRouterFixture = `{"data":[
 {"id":"anthropic/claude-opus-5.5","pricing":{"prompt":"0.000004","completion":"0.00002","input_cache_read":"0.0000002","input_cache_write":"0.000005"}},
 {"id":"anthropic/claude-opus-5.5:batch","pricing":{"prompt":"0.000002","completion":"0.00001"}},
 {"id":"deepseek/deepseek-v4.1-flash-1017","pricing":{"prompt":"0.0000003","completion":"0.0000012","input_cache_read":"0.000000006"}},
 {"id":"openrouter/auto","pricing":{"prompt":"-1","completion":"-1"}},
 {"id":"broken/model","pricing":{"prompt":"free","completion":"0.1"}}]}`

func TestParseOpenRouterPrices(t *testing.T) {
	list, err := parseOpenRouterPrices(strings.NewReader(openRouterFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("%d prices, want the 3 readable ones: %+v", len(list), list)
	}
	if p := list[0]; p.ID != "anthropic/claude-opus-5.5" || p.Prompt != 4e-6 || p.Completion != 2e-5 || p.CacheRead != 2e-7 || p.CacheWrite != 5e-6 {
		t.Fatalf("opus: %+v", p)
	}
	if p := list[2]; p.CacheWrite != 0 {
		t.Fatalf("deepseek lists no cache write price: %+v", p)
	}
	if _, err := parseOpenRouterPrices(strings.NewReader(`{"data":[]}`)); err == nil {
		t.Fatal("an empty list must be an error")
	}
}

func priceServer(t *testing.T, body string, status int) (*httptest.Server, *int) {
	t.Helper()
	hits := new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		// The request carries nothing about the owner.
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("price request carries credentials: %v", r.Header)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, hits
}

// A failed fetch keeps the old prices; a good one replaces them and rebuilds the
// picker table, which reads them.
func TestRefreshPricesKeepsTheLastGoodCopy(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	good, _ := priceServer(t, openRouterFixture, http.StatusOK)
	if err := g.refreshPrices(context.Background(), good.URL); err != nil {
		t.Fatal(err)
	}
	if p, ok := s.priceBook().Lookup("claude-opus-5-5:high"); !ok || p.Prompt != 4e-6 {
		t.Fatalf("after a good fetch: %+v %v", p, ok)
	}
	before := s.pickerTable().Revision
	bad, _ := priceServer(t, "boom", http.StatusBadGateway)
	if err := g.refreshPrices(context.Background(), bad.URL); err == nil {
		t.Fatal("a 502 must be an error")
	}
	if p, ok := s.priceBook().Lookup("claude-opus-5-5"); !ok || p.Prompt != 4e-6 {
		t.Fatalf("a failed fetch lost the prices: %+v %v", p, ok)
	}
	if s.pickerTable().Revision != before {
		t.Fatal("a failed fetch rebuilt the picker table")
	}
	// The copy survives a restart: a new Store reads it from the table.
	list, _, err := s.storedPrices()
	if err != nil || len(list) != 3 {
		t.Fatalf("stored prices: %v %v", list, err)
	}
}

func setSessionUsage(t *testing.T, s *Store, session string, u protocol.RunTokens) {
	t.Helper()
	raw := `{"in":` + ftoa(u.In) + `,"out":` + ftoa(u.Out) + `,"cache_read":` + ftoa(u.CacheRead) + `}`
	if _, err := s.db.Exec(`UPDATE sessions SET worker=json_set(CASE WHEN json_valid(worker) THEN worker ELSE '{}' END,'$.usage',json(?)) WHERE id=?`, raw, session); err != nil {
		t.Fatal(err)
	}
}

func ftoa(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// The tokens of a worker session are kept with its run, a task's cost is those
// tokens at API prices, and a new price list changes the cost.
func TestRunUsageIsKeptAndPricesTheTaskAndTheWorkerLog(t *testing.T) {
	r := newDeliveryRig(t)
	s := r.s
	if err := s.ReplacePrices([]protocol.ModelPrice{{ID: "anthropic/claude-opus-5.5", Prompt: 4e-6, Completion: 2e-5, CacheRead: 2e-7, CacheWrite: 5e-6}}); err != nil {
		t.Fatal(err)
	}
	usage := protocol.RunTokens{In: 1e6, Out: 1e5, CacheRead: 1e7}
	setSessionUsage(t, s, r.worker, usage)
	pickerRun(t, s, r.a, r.worker, "claude", "claude-opus-5-5:high", protocol.OutcomeComplete, 0, 1000)
	pickerAssessment(t, s, r.worker, 2.5)

	got := pickerTasksByKey(t, s)["claude-opus-5-5:high"]
	want := 1e6*4e-6 + 1e5*2e-5 + 1e7*2e-7
	if len(got) != 1 || got[0].Tokens != usage || math.Abs(got[0].Cost-want) > 1e-9 {
		t.Fatalf("task: %+v, want tokens %+v and cost %v", got, usage, want)
	}
	if avg, n := s.pickerTable().AvgCost("claude-opus-5-5:high", s.pickerTable().Bucket(2.5)); n != 1 || math.Abs(avg-want) > 1e-9 {
		t.Fatalf("table average cost %v over %d tasks, want %v", avg, n, want)
	}

	log, err := s.WorkerLog(10)
	if err != nil {
		t.Fatal(err)
	}
	var entry *protocol.WorkerLogEntry
	for i := range log {
		if log[i].SessionID == r.worker && log[i].Status == "done" {
			entry = &log[i]
		}
	}
	if entry == nil || entry.Tokens == nil || *entry.Tokens != usage || math.Abs(entry.Cost-want) > 1e-9 || entry.PriceUnknown {
		t.Fatalf("worker log entry: %+v", entry)
	}

	// Prices double: the table and the log follow.
	if err := s.ReplacePrices([]protocol.ModelPrice{{ID: "anthropic/claude-opus-5.5", Prompt: 8e-6, Completion: 4e-5, CacheRead: 4e-7, CacheWrite: 1e-5}}); err != nil {
		t.Fatal(err)
	}
	if avg, _ := s.pickerTable().AvgCost("claude-opus-5-5:high", s.pickerTable().Bucket(2.5)); math.Abs(avg-2*want) > 1e-9 {
		t.Fatalf("after the price change the average cost is %v, want %v", avg, 2*want)
	}
	log, _ = s.WorkerLog(10)
	seen := 0
	for _, e := range log {
		if e.Status == "done" {
			seen++
			if math.Abs(e.Cost-2*want) > 1e-9 {
				t.Fatalf("worker log cost after the price change: %v", e.Cost)
			}
		}
	}
	if seen != 1 {
		t.Fatalf("%d finished runs in the log, want 1", seen)
	}
}

func TestRunCostOfAnUnlistedModelUsesTheMiddlePrice(t *testing.T) {
	s, _ := newTestStore(t)
	if _, _, known := s.RunCost("claude-opus-5-5", protocol.RunTokens{In: 1}); known {
		t.Fatal("an empty price book must not know a cost")
	}
	if err := s.ReplacePrices([]protocol.ModelPrice{{ID: "a/one", Prompt: 1e-6, Completion: 1e-6}, {ID: "a/two", Prompt: 3e-6, Completion: 3e-6}, {ID: "a/three", Prompt: 9e-6, Completion: 9e-6}}); err != nil {
		t.Fatal(err)
	}
	cost, listed, known := s.RunCost("mystery-9", protocol.RunTokens{In: 1000})
	if !known || listed || math.Abs(cost-3e-3) > 1e-12 {
		t.Fatalf("cost %v listed %v known %v, want the middle price", cost, listed, known)
	}
}
