package gateway

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Each recorded pick keeps what it read of the quota windows, and the run it
// chose adds its tokens and its provider's windows at the end (decision 0191).
// These tests make one automatic start through the gateway against the fake
// machine of the continue tests, then read the record.

// pickQuotaStart makes one automatic start at normal. Anthropic is under the
// owner's floor of 40 percent (its 5h window is 70 percent used); the Codex 5h
// window was reset when the reading was made, two minutes before the pick,
// and its week window is 20 percent used at 60 percent of the week. The
// DeepSeek models are paused, so the pick runs Codex. It returns the worker
// session and the time of the reading.
func pickQuotaStart(t *testing.T) (continueFixture, string, int64) {
	t.Helper()
	cf := continueGateway(t)
	cf.setScores(t, pausedScore("deepseek/deepseek-v4.1-flash"), pausedScore("opencode-go/deepseek-v4.1-flash"))
	if err := cf.g.store.setOwner(workerFloorsKey, protocol.WorkerFloors{Floors: map[string]int{"anthropic": 40}}); err != nil {
		t.Fatal(err)
	}
	now := cf.g.store.now()
	readAt := now.Add(-2 * time.Minute).Unix()
	cf.g.usage.set(cf.nc.id, now, protocol.UsageReport{Quotas: &protocol.Quotas{GeneratedAt: readAt, Providers: []protocol.QuotaProvider{
		{Provider: "claude", Windows: []protocol.QuotaWindow{
			{ID: "5h", Kind: "five_hour", PercentUsed: 70, ResetsAt: now.Add(2 * time.Hour).Unix()},
			{ID: "week", Kind: "week", PercentUsed: 30, ResetsAt: now.Add(72 * time.Hour).Unix()}}},
		{Provider: "codex", Windows: []protocol.QuotaWindow{
			{ID: "5h", Kind: "five_hour", PercentUsed: 0, ResetsAt: readAt + 5*3600},
			{ID: "week", Kind: "week", PercentUsed: 20, ResetsAt: readAt + int64(0.4*7*24*3600)}}}}}})
	ans, err := cf.g.workerStart(t.Context(), cf.nc, protocol.WorkerStartFrame{Owner: cf.owner,
		Assessment:         &protocol.WorkerAssessment{RunID: "r_pick", StartedAt: 1, AssessedAt: 1, Label: "normal", D: protocol.PickerD(nil, "normal"), Confidence: "high"},
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: "implement feature x"}})
	if err != nil || ans.SessionID == "" {
		t.Fatalf("start: %+v %v", ans, err)
	}
	if ans.Harness != "codex" {
		t.Fatalf("the pick ran %s %s; note %q", ans.Harness, ans.Model, ans.ModelNote)
	}
	return cf, ans.SessionID, readAt
}

// pickRecord reads what the pick of run_id r_pick read, and the position of
// each harness's row.
func pickRecord(t *testing.T, cf continueFixture) (protocol.PickQuota, map[string]int) {
	t.Helper()
	var raw string
	if err := cf.g.store.db.QueryRow(`SELECT read FROM pick_quota WHERE run_id='r_pick'`).Scan(&raw); err != nil {
		t.Fatalf("pick quota row: %v", err)
	}
	var read protocol.PickQuota
	if err := json.Unmarshal([]byte(raw), &read); err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	rows, err := cf.g.store.db.Query(`SELECT pos, harness, model FROM pick_candidates WHERE run_id='r_pick'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var p int
		var harness, model string
		if err := rows.Scan(&p, &harness, &model); err != nil {
			t.Fatal(err)
		}
		pos[harness+" "+model] = p
	}
	return read, pos
}

// candidateRead is the record of the row at pos.
func candidateRead(t *testing.T, read protocol.PickQuota, pos int) protocol.PickQuotaCandidate {
	t.Helper()
	for _, c := range read.Candidates {
		if c.Pos == pos {
			return c
		}
	}
	t.Fatalf("no record for row %d in %+v", pos, read.Candidates)
	return protocol.PickQuotaCandidate{}
}

// The pick keeps the windows it read, once per provider, with the reading
// time, and for each candidate the windows that serve it, the one that decided
// and the gate step that held it back.
func TestPickKeepsTheQuotaWindowsItRead(t *testing.T) {
	cf, _, readAt := pickQuotaStart(t)
	read, pos := pickRecord(t, cf)
	if read.ReadAt != readAt || read.At != cf.g.store.now().Unix() {
		t.Fatalf("reading at %d, pick at %d; want %d and %d", read.ReadAt, read.At, readAt, cf.g.store.now().Unix())
	}
	providers := map[string]protocol.PickQuotaProvider{}
	for _, p := range read.Providers {
		providers[p.Provider] = p
	}
	claude := providers["claude"]
	if len(read.Providers) != 2 || claude.ReadAt != readAt || len(claude.Windows) != 2 || claude.Windows[0].ID != "5h" || claude.Windows[0].Used != 70 ||
		claude.Windows[0].ResetsAt != cf.g.store.now().Add(2*time.Hour).Unix() || claude.Windows[0].Kind != "five_hour" {
		t.Fatalf("providers read: %+v", read.Providers)
	}
	opus := candidateRead(t, read, pos["claude "+opusModel])
	if opus.Provider != "claude" || opus.Check != "floor" || opus.Floor != 40 || opus.Decided != "5h" {
		t.Fatalf("opus read: %+v", opus)
	}
	terra := candidateRead(t, read, pos["codex "+terraModel])
	if terra.Provider != "codex" || terra.Check != "" || terra.Decided != "5h" || strings.Join(terra.Windows, ",") != "5h,week" {
		t.Fatalf("terra read: %+v", terra)
	}
	if paused := candidateRead(t, read, pos["opencode "+deepseekModel]); paused.Check != checkPaused {
		t.Fatalf("paused read: %+v", paused)
	}
}

// The run's end adds its tokens by kind and its provider's windows at the end
// to the same pick, and the CTO's read says it in plain words.
func TestPickKeepsWhatItsRunUsed(t *testing.T) {
	cf, worker, _ := pickQuotaStart(t)
	g := cf.g
	tokens := protocol.RunTokens{In: 1000, Out: 200, Reasoning: 50, CacheRead: 9000, CacheWrite: 300}
	raw, _ := json.Marshal(tokens)
	if _, err := g.store.db.Exec(`UPDATE sessions SET worker=json_set(worker,'$.usage',json(?)) WHERE id=?`, string(raw), worker); err != nil {
		t.Fatal(err)
	}
	cf.clock.advance(30 * time.Minute)
	now := g.store.now()
	g.usage.set(cf.nc.id, now, protocol.UsageReport{Quotas: &protocol.Quotas{GeneratedAt: now.Unix(), Providers: []protocol.QuotaProvider{
		{Provider: "codex", Windows: []protocol.QuotaWindow{{ID: "5h", Kind: "five_hour", PercentUsed: 12}, {ID: "week", Kind: "week", PercentUsed: 21}}}}}})
	stopped, _ := json.Marshal(protocol.WorkerStopped{SessionID: worker, Project: "alpha", Harness: "codex", Model: terraModel,
		StartedAt: now.Add(-30 * time.Minute).Unix(), Reason: protocol.StopRunFailed})
	if _, err := g.handleWorker(t.Context(), cf.nc, "worker.stopped", stopped); err != nil {
		t.Fatal(err)
	}
	views, err := g.store.PickQuotaViews([]string{worker})
	if err != nil {
		t.Fatal(err)
	}
	view := views[worker]
	if view.RunEnd == nil || view.RunEnd.Tokens != tokens || view.RunEnd.After == nil || view.RunEnd.After.Windows[0].Used != 12 || view.RunEnd.Provider != "codex" {
		t.Fatalf("run end: %+v", view.RunEnd)
	}
	text := strings.Join(view.Lines, "\n")
	// The quota_read a reviewer sees with -v, as agent_workers_find shows it.
	t.Logf("quota_read:\n%s", text)
	for _, want := range []string{
		"quota reading of 2026-11-04 11:58 UTC, 2 min before the pick",
		"claude: read 2 min before the pick; 5h window 70% used, resets 2026-11-04 14:00 UTC; week window 30% used",
		"held back by the gate (floor): anthropic has 30% left in its 5h window, under your 40% worker floor",
		"tokens 1000 in, 200 out, 50 reasoning, 9000 cache read, 300 cache write",
		"codex 5h 0% to 12% used, week 20% to 21% used",
		"deepseek: no quota reading for its provider; held back by the gate (paused): paused by the owner",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("plain words lack %q:\n%s", want, text)
		}
	}
}

// What the pace rule reads when a window was just reset: the window is 0
// percent used at 0 percent elapsed, so it reads as on pace (room 0, factor
// 1). The rule keeps the smallest room of a provider's windows, so the fresh
// 5h window decides, and the 40 points of room the week window has (20
// percent used at 60 percent elapsed, a factor of 4) are not read. The rule is
// not changed here; this records what it reads.
func TestPaceRuleReadsAJustResetWindowAsOnPace(t *testing.T) {
	cf, worker, _ := pickQuotaStart(t)
	read, pos := pickRecord(t, cf)
	at := pos["codex "+terraModel]
	if terra := candidateRead(t, read, at); terra.Decided != "5h" {
		t.Fatalf("terra was decided by %q", terra.Decided)
	}
	var room, factor float64
	if err := cf.g.store.db.QueryRow(`SELECT pace_room, pace_factor FROM pick_candidates WHERE run_id='r_pick' AND pos=?`, at).Scan(&room, &factor); err != nil {
		t.Fatal(err)
	}
	if room != 0 || factor != 1 {
		t.Fatalf("terra pace room %v, factor %v", room, factor)
	}
	// The week window alone gives 40 points of room.
	week := &protocol.Quotas{GeneratedAt: read.ReadAt, Providers: []protocol.QuotaProvider{{Provider: "codex", Windows: []protocol.QuotaWindow{
		{ID: "week", Kind: "week", PercentUsed: 20, ResetsAt: read.ReadAt + int64(0.4*7*24*3600)}}}}}
	if weekRoom, _, _ := protocol.WorkerQuotaPace(week, "codex", terraModel); weekRoom < 39.9 || weekRoom > 40.1 {
		t.Fatalf("week room %v", weekRoom)
	}
	views, err := cf.g.store.PickQuotaViews([]string{worker})
	if err != nil {
		t.Fatal(err)
	}
	if want := "codex 5h, week read; decided by 5h (0% used), on pace, pace factor 1.00"; !strings.Contains(strings.Join(views[worker].Lines, "\n"), want) {
		t.Fatalf("plain words lack %q:\n%s", want, strings.Join(views[worker].Lines, "\n"))
	}
}

// BenchmarkRecordPick measures the pick's one write with and without what it
// read of the quota windows: the same transaction, one more insert.
func BenchmarkRecordPick(b *testing.B) {
	s := orgReadFixture(b)
	quotas := &protocol.Quotas{GeneratedAt: 1, Providers: []protocol.QuotaProvider{
		{Provider: "claude", Windows: []protocol.QuotaWindow{{ID: "5h", Kind: "five_hour", PercentUsed: 40, ResetsAt: 7200}, {ID: "week", Kind: "week", PercentUsed: 30, ResetsAt: 86400}}},
		{Provider: "codex", Windows: []protocol.QuotaWindow{{ID: "5h", Kind: "five_hour", PercentUsed: 10, ResetsAt: 7200}, {ID: "week", Kind: "week", PercentUsed: 50, ResetsAt: 86400}}}}}
	rows := make([]protocol.PickCandidate, 12)
	for i := range rows {
		harness := []string{"claude", "codex", "opencode"}[i%3]
		rows[i] = protocol.PickCandidate{Name: fmt.Sprint("m", i), Harness: harness, Model: fmt.Sprint("model-", i), Rule: protocol.PickRuleCell, PaceFactor: 1}
	}
	for _, withQuota := range []bool{false, true} {
		b.Run(fmt.Sprint("quota=", withQuota), func(b *testing.B) {
			for i := 0; b.Loop(); i++ {
				a := protocol.WorkerAssessment{RunID: fmt.Sprintf("r_%v_%d", withQuota, i), SessionID: "s", StartedAt: int64(i), PickRows: rows}
				if withQuota {
					a.PickQuota = protocol.PickQuotaRead(quotas, protocol.WorkerFloors{}, rows, nil, 1)
				}
				if err := s.RecordWorkerStartAssessment(a); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
