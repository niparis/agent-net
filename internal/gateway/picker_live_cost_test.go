package gateway

import (
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// livePickerFixture is a gateway database as the live one looked when the cost
// rule failed to show a price: the runs, the judge results and the prices are
// stored, and the tokens are only the "tokens:" line of each stored worker
// notice. With noticeTokens false the notices carry no tokens, which is the
// state just after a start, before any run reported usage. It returns the store
// reopened the way the gateway opens it at start, so the picker table is the
// cached one a live pick reads.
func livePickerFixture(t *testing.T, noticeTokens bool) (*Store, []protocol.WorkerModelCandidate) {
	t.Helper()
	path := seededStorePath(t, "gateway.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	first := s
	t.Cleanup(func() { first.Close() })
	if err := s.ReplacePrices([]protocol.ModelPrice{
		{ID: "anthropic/claude-opus-5.5", Prompt: 4e-6, Completion: 2e-5, CacheRead: 2e-7, CacheWrite: 5e-6},
		{ID: "deepseek/deepseek-v4.1-flash-1017", Prompt: 3e-7, Completion: 1.2e-6, CacheRead: 6e-9},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.saveWorkerProfiles([]protocol.WorkerProfile{
		{Name: "opus", Harness: "claude", Model: "claude-opus-5-5", Effort: "high"},
		{Name: "deepseek", Harness: "opencode", Model: "alibaba-token-plan/deepseek-v4.1-flash-1017", Effort: "high"},
	}); err != nil {
		t.Fatal(err)
	}
	node, _, err := s.CreateNode("h", "linux")
	if err != nil {
		t.Fatal(err)
	}
	candidates := []protocol.WorkerModelCandidate{
		{Name: "opus", Harness: "claude", Model: "claude-opus-5-5:high"},
		{Name: "deepseek", Harness: "opencode", Model: "alibaba-token-plan/deepseek-v4.1-flash-1017:high"},
	}
	for i := 0; i < 8; i++ {
		c := candidates[i%len(candidates)]
		session := fmt.Sprintf("s_live_%d", i)
		tokens := "tokens: 100000 in, 20000 out, 3000000 cache read\n"
		if !noticeTokens {
			tokens = ""
		}
		body := fmt.Sprintf("[agent-net worker notice]\nstatus: done\nworker: %s (%s, %s), project p on h\nrun time: 1m\n%s", session, c.Harness, c.Model, tokens)
		if _, err := s.db.Exec(`INSERT INTO messages(id,conversation_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state) VALUES(?,?,?,?,?,?,0,?,?,?,?)`,
			fmt.Sprintf("m_live_%d", i), "c_live", session, "o", "notice", body, fmt.Sprintf("k_live_%d", i), int64(i), int64(1<<40), "delivered"); err != nil {
			t.Fatal(err)
		}
		pickerRun(t, s, node, session, c.Harness, c.Model, protocol.OutcomeComplete, 0, int64(1000+i))
		pickerAssessment(t, s, session, 2.83)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	return s, candidates
}

// A live pick prices its tasks from the stored notices of runs that ended
// before the daemon reported usage by kind, and its note carries the same cost
// as the backtest over the same data.
func TestLivePickerPricesTasksFromStoredNoticeTokens(t *testing.T) {
	s, candidates := livePickerFixture(t, true)
	defer s.Close()
	g := &Gateway{store: s}
	choice, _, err := g.chooseAutomaticWorkerAt(candidates, "normal", 2.83, "", nil, nil, nil, nil, protocol.ProtectedProviders{}, false)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.PickerCostBacktest(0, PickerCostBacktestOptions{Prices: s.priceBook()})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Buckets) != 1 {
		t.Fatalf("backtest buckets: %+v", res.Buckets)
	}
	rows := res.Buckets[0].Rows
	if len(rows) != len(candidates) {
		t.Fatalf("backtest rows: %+v", rows)
	}
	cheapest := rows[0]
	for _, row := range rows {
		if !row.CostKnown {
			t.Fatalf("%s has no cost from its stored notice: %+v", row.Name, row)
		}
		text := fmt.Sprintf(" $%.2f/run, p %.2f, $%.2f expected", row.Cost, row.Pass, row.Expected)
		if !strings.Contains(choice.PickNote, text) {
			t.Errorf("pick note has no %q for %s: %s", text, row.Name, choice.PickNote)
		}
		if row.Expected < cheapest.Expected {
			cheapest = row
		}
	}
	if got := protocol.PickerModelKey(choice.Harness, choice.Model); got != cheapest.Key {
		t.Fatalf("picked %s, want the lowest expected cost %s (%+v): %s", got, cheapest.Key, cheapest, choice.PickNote)
	}
	t.Logf("pick note: %s", choice.PickNote)
}

// The live gateway showed no cost at start and costs minutes later without a
// restart: its first table held no task with tokens, and a write that added one
// rebuilt it. The cached table must follow that write and price the new run,
// while a cell with no priced run takes the bucket average.
func TestPickerCostAppearsAfterAWriteWithoutARestart(t *testing.T) {
	s, candidates := livePickerFixture(t, false)
	defer s.Close()
	g := &Gateway{store: s}
	first, _, err := g.chooseAutomaticWorkerAt(candidates, "normal", 2.83, "", nil, nil, nil, nil, protocol.ProtectedProviders{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(first.PickNote, "/run") {
		t.Fatalf("the fixture already has a cost: %s", first.PickNote)
	}
	before := s.pickerTable().Revision
	node, _, err := s.CreateNode("h2", "linux")
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: "live-new", Model: "alibaba-token-plan/deepseek-v4.1-flash-1017:high"})
	if err != nil {
		t.Fatal(err)
	}
	setSessionUsage(t, s, session, protocol.RunTokens{In: 1e6, Out: 1e5, CacheRead: 1e7})
	pickerRun(t, s, node, session, "opencode", "alibaba-token-plan/deepseek-v4.1-flash-1017:high", protocol.OutcomeComplete, 0, 2000)
	pickerAssessment(t, s, session, 2.83)
	if s.pickerTable().Revision <= before {
		t.Fatal("the write did not rebuild the cached table")
	}
	second, _, err := g.chooseAutomaticWorkerAt(candidates, "normal", 2.83, "", nil, nil, nil, nil, protocol.ProtectedProviders{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(second.PickNote, "/run") || !strings.Contains(second.PickNote, " est $") {
		t.Fatalf("the later pick has no cost, or no estimate for the unpriced cell: %s", second.PickNote)
	}
	t.Logf("first note: %s", first.PickNote)
	t.Logf("second note: %s", second.PickNote)
}
