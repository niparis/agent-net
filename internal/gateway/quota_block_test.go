package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// An agy worker that fails with the account's "Individual quota reached"
// error blocks every agy model on that machine until the reset it names.
func TestAgyAccountQuotaFailureRefusesNextAgyStart(t *testing.T) {
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
	if _, err := st.SetNodeHarnesses(node, []protocol.HarnessInfo{{Name: "agy", Models: []string{"gemini-4-argon-high", "claude-opus-5-5-high"}}, {Name: "claude", Models: []string{"claude-opus-5-5"}}}); err != nil {
		t.Fatal(err)
	}
	owner, err := st.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	nc := &nodeConn{id: node}
	g.conns[node] = nc
	t.Cleanup(func() { delete(g.conns, node) })
	start := func(harness, model string) error {
		t.Helper()
		_, err := g.workerStart(context.Background(), nc, protocol.WorkerStartFrame{Owner: owner, Level: "normal",
			WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: "test", Harness: harness, Model: model}}, true)
		return err
	}
	if err := start("agy", "claude-opus-5-5-high"); err != nil {
		t.Fatalf("start before the failure: %v", err)
	}

	worker, err := st.UpsertSession(node, protocol.SessionInfo{Harness: "agy", NativeID: "worker", Model: "gemini-4-argon-high", Worker: &protocol.WorkerInfo{ID: "w_argon"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(protocol.WorkerStopped{SessionID: worker, WorkerID: "w_argon", Project: "alpha", Harness: "agy", Model: "gemini-4-argon-high",
		Reason: protocol.StopRunFailed, Failure: "Antigravity result ERROR error: Individual quota reached. Please upgrade your subscription to increase your limits. Resets in 160h24m5s."})
	if _, err := g.handleWorker(context.Background(), nc, "worker.stopped", raw); err != nil {
		t.Fatal(err)
	}

	err = start("agy", "claude-opus-5-5-high")
	if err == nil || !strings.Contains(err.Error(), "agy quota used up until 2026-11-11 04:24 UTC") || !strings.Contains(err.Error(), "pick another harness") {
		t.Fatalf("explicit agy start after the quota failure: %v", err)
	}
	if err := start("claude", "claude-opus-5-5"); err != nil {
		t.Fatalf("another harness is refused too: %v", err)
	}
	// The block is stored: a restarted gateway keeps it.
	if q := g.nodeQuotas(node); protocol.QuotaRefusal(q, "agy", "gemini-4-argon-high") == "" {
		t.Fatalf("quota reading lacks the block: %+v", q)
	}
	// It ends at the reset time.
	c.t = c.t.Add(160*time.Hour + 24*time.Minute + 5*time.Second)
	if err := start("agy", "claude-opus-5-5-high"); err != nil {
		t.Fatalf("start after the reset: %v", err)
	}
}

func TestQuotaBlockSurvivesStoreRestart(t *testing.T) {
	path := seededStorePath(t, "gw.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	if err := s.RecordQuotaBlock("node", protocol.QuotaBlock{Provider: "agy", Until: now.Add(time.Hour)}, "Individual quota reached"); err != nil {
		t.Fatal(err)
	}
	// A window of the same provider keeps its own block.
	if err := s.RecordQuotaBlock("node", protocol.QuotaBlock{Provider: "agy", WindowID: "gemini_argon_weekly", WindowLabel: "Gemini Argon weekly", Until: now.Add(2 * time.Hour)}, "Individual quota reached"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.now = func() time.Time { return now }
	blocks, err := s.QuotaBlocks("node")
	if err != nil || len(blocks) != 2 {
		t.Fatalf("blocks %v, %v", blocks, err)
	}
	for _, b := range blocks {
		switch {
		case b.Provider != "agy":
			t.Fatalf("block of another provider: %+v", b)
		case b.WindowID == "" && b.Until.Equal(now.Add(time.Hour)):
		case b.WindowID == "gemini_argon_weekly" && b.Until.Equal(now.Add(2*time.Hour)) && b.WindowLabel == "Gemini Argon weekly":
		default:
			t.Fatalf("block %+v", b)
		}
	}
	if other, err := s.QuotaBlocks("other"); err != nil || len(other) != 0 {
		t.Fatalf("other node: %v, %v", other, err)
	}
	s.now = func() time.Time { return now.Add(time.Hour) }
	if blocks, err := s.QuotaBlocks("node"); err != nil || len(blocks) != 1 || blocks[0].WindowID != "gemini_argon_weekly" {
		t.Fatalf("after the provider block ends: %v, %v", blocks, err)
	}
	s.now = func() time.Time { return now.Add(2 * time.Hour) }
	if blocks, err := s.QuotaBlocks("node"); err != nil || len(blocks) != 0 {
		t.Fatalf("expired block: %v, %v", blocks, err)
	}
}
