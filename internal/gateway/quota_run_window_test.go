package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// agyAccountFailure is an agy run's account quota failure, with a reset in 160h24m5s.
const agyAccountFailure = "Antigravity result ERROR error: Individual quota reached. Please upgrade your subscription to increase your limits. Resets in 160h24m5s."

// agyLiveReading is the agy reading the owner saw: Argon weekly at argonUsed
// percent, Gemini weekly at 24 and Gemini 5-hour at 0. Claude/GPT is exhausted.
func agyLiveReading(generated time.Time, argonUsed float64) *protocol.Quotas {
	reset := generated.Add(7 * 24 * time.Hour)
	return &protocol.Quotas{GeneratedAt: generated.Unix(), Providers: []protocol.QuotaProvider{{Provider: "agy", Windows: []protocol.QuotaWindow{
		{ID: "gemini_argon_weekly", Label: "Gemini Argon weekly", Kind: "weekly", PercentUsed: argonUsed, ResetsAt: reset.Unix()},
		{ID: "gemini_weekly", Label: "Gemini weekly", Kind: "weekly", PercentUsed: 24, ResetsAt: reset.Unix()},
		{ID: "gemini_5h", Label: "Gemini 5-hour", Kind: "session", PercentUsed: 0, ResetsAt: generated.Add(5 * time.Hour).Unix()},
		{ID: "claude_gpt_weekly", Label: "Claude/GPT weekly", Kind: "weekly", PercentUsed: 100, Exhausted: true, ResetsAt: reset.Unix()},
	}}}}
}

// agyGate is a gateway with one agy machine and the owner's session on it. Its
// clock starts at 2026-11-04 12:00 UTC.
type agyGate struct {
	g     *Gateway
	node  string
	clock *clock
	owner string
	nc    *nodeConn
}

func newAgyGate(t *testing.T) agyGate {
	t.Helper()
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
	if _, err := st.SetNodeHarnesses(node, []protocol.HarnessInfo{
		{Name: "agy", Models: []string{"gemini-3.8-flash-low", "gemini-4-argon-high", "claude-opus-5-5-high"}},
		{Name: "claude", Models: []string{"claude-opus-5-5"}},
	}); err != nil {
		t.Fatal(err)
	}
	owner, err := st.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	nc := &nodeConn{id: node}
	g.conns[node] = nc
	t.Cleanup(func() { delete(g.conns, node) })
	return agyGate{g: g, node: node, clock: c, owner: owner, nc: nc}
}

// start asks for a worker on harness and model, as an explicit start.
func (a agyGate) start(t *testing.T, harness, model string) error {
	t.Helper()
	_, err := a.g.workerStart(context.Background(), a.nc, protocol.WorkerStartFrame{Owner: a.owner, Level: "normal",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: "test", Harness: harness, Model: model}}, true)
	return err
}

// fail reports a worker run on model that ended with failure, as worker.stopped.
func (a agyGate) fail(t *testing.T, model, failure string) {
	t.Helper()
	worker, err := a.g.store.UpsertSession(a.node, protocol.SessionInfo{Harness: "agy", NativeID: "worker-" + model, Model: model, Worker: &protocol.WorkerInfo{ID: "w_" + model}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(protocol.WorkerStopped{SessionID: worker, WorkerID: "w_" + model, Project: "alpha", Harness: "agy", Model: model,
		Reason: protocol.StopRunFailed, Failure: failure})
	if _, err := a.g.handleWorker(context.Background(), a.nc, "worker.stopped", raw); err != nil {
		t.Fatal(err)
	}
}

// A run that reports the Argon window out of quota blocks that window only.
// Flash keeps its allowance, and the refusal names the window.
func TestAgyRunBlockOnArgonWindowLeavesFlash(t *testing.T) {
	a := newAgyGate(t)
	a.g.usage.set(a.node, a.clock.now(), protocol.UsageReport{Quotas: agyLiveReading(a.clock.now().Add(-10*time.Minute), 99)})
	a.fail(t, "gemini-4-argon-high", agyAccountFailure)
	if err := a.start(t, "agy", "gemini-3.8-flash-low"); err != nil {
		t.Fatalf("flash refused after an argon run reported quota: %v", err)
	}
	err := a.start(t, "agy", "gemini-4-argon-high")
	want := "agy Gemini Argon weekly used up until 2026-11-11 04:24 UTC; other agy models may still be used"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("argon start after its window was reported: %v; want %q", err, want)
	}
}

// A run on a model that no window of its provider serves blocks the whole
// provider, as before.
func TestAgyRunBlockWithoutMatchingWindowBlocksAgy(t *testing.T) {
	a := newAgyGate(t)
	a.g.usage.set(a.node, a.clock.now(), protocol.UsageReport{Quotas: agyLiveReading(a.clock.now().Add(-10*time.Minute), 40)})
	a.fail(t, "mystery-model", agyAccountFailure)
	for _, model := range []string{"gemini-3.8-flash-low", "gemini-4-argon-high"} {
		err := a.start(t, "agy", model)
		if err == nil || !strings.Contains(err.Error(), "agy quota used up until 2026-11-11 04:24 UTC") || !strings.Contains(err.Error(), "pick another harness") {
			t.Fatalf("%s after a run with no matching window: %v", model, err)
		}
	}
	if err := a.start(t, "claude", "claude-opus-5-5"); err != nil {
		t.Fatalf("another harness is refused too: %v", err)
	}
}

// A reading made after the run's failure that shows the window with allowance
// left wins over the run's report. A reading from before the failure does not.
func TestAgyRunBlockEndsWhenFresherReadingShowsWindowOpen(t *testing.T) {
	a := newAgyGate(t)
	a.g.usage.set(a.node, a.clock.now(), protocol.UsageReport{Quotas: agyLiveReading(a.clock.now().Add(-10*time.Minute), 99)})
	a.fail(t, "gemini-4-argon-high", agyAccountFailure)
	if err := a.start(t, "agy", "gemini-4-argon-high"); err == nil {
		t.Fatal("argon start admitted right after its run reported quota")
	}
	// A reading generated before the failure (12:00) does not end the block,
	// even when it arrives later.
	a.clock.t = a.clock.t.Add(2 * time.Minute)
	a.g.usage.set(a.node, a.clock.now(), protocol.UsageReport{Quotas: agyLiveReading(a.clock.now().Add(-7*time.Minute), 40)})
	if err := a.start(t, "agy", "gemini-4-argon-high"); err == nil {
		t.Fatal("a reading older than the run's report ended the block")
	}
	// A reading generated after the failure shows Argon at 40 percent.
	a.clock.t = a.clock.t.Add(5 * time.Minute)
	a.g.usage.set(a.node, a.clock.now(), protocol.UsageReport{Quotas: agyLiveReading(a.clock.now(), 40)})
	if err := a.start(t, "agy", "gemini-4-argon-high"); err != nil {
		t.Fatalf("argon refused after a fresher reading shows its window open: %v", err)
	}
}

// Blocks stored before the window column existed name no window, so the
// migration that adds it drops them once. The next run records its own block.
func TestQuotaBlocksMigrationDropsWholeProviderRows(t *testing.T) {
	path := seededStorePath(t, "gw.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP TABLE quota_blocks;
CREATE TABLE quota_blocks (node_id TEXT NOT NULL, provider TEXT NOT NULL, resets_at INTEGER NOT NULL,
 detail TEXT NOT NULL DEFAULT '', at INTEGER NOT NULL, PRIMARY KEY (node_id, provider));
INSERT INTO quota_blocks (node_id, provider, resets_at, detail, at) VALUES ('node', 'agy', 4102444800, 'old', 0);`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	setSchemaVersion(t, path, migrationVersion(t, "Quota blocks per window")-1)
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM quota_blocks`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("quota_blocks rows after the migration: %d, %v", rows, err)
	}
}
