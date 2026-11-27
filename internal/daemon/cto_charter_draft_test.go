package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// ctoDraftFixture starts a gateway with the CTO, the owner session and one
// repo whose session drafts charter fields. It returns the session's client
// and its id.
func ctoDraftFixture(t *testing.T) (*testGateway, *node, *localClient, string) {
	t.Helper()
	ctx := context.Background()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	// Any owner call registers the owner session the CTO acts through.
	cliClient(t, n).mustCall("learned_list", nil)
	eventually(t, "owner session online at the gateway", func() bool {
		a, ok, err := g.gw.Store().Agent(n.d.ownerSessionID())
		return err == nil && ok && a.Online
	})
	repo := newRepo(t, "demo")
	client, id := helloDelivery(t, n, "caller", repo, "claude-opus-4")
	eventually(t, "session and repo registered", func() bool {
		a, ok, err := g.gw.Store().Agent(id)
		return err == nil && ok && a.Repo.Project == "demo"
	})
	if err := g.gw.Store().SetCharter("demo", "maturity", "early", "owner"); err != nil {
		t.Fatal(err)
	}
	return g, n, client, id
}

// draftRow is one charter draft as cto_owner_items_find shows it.
type draftRow struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Level     string `json:"level"`
	Scope     string `json:"scope"`
	Attr      string `json:"attr"`
	Value     string `json:"value"`
	Source    string `json:"source"`
	DraftedBy string `json:"drafted_by"`
	Status    string `json:"status"`
	Answer    string `json:"answer"`
}

// ctoDraftRows reads the pending draft rows (cd_ ids) the CTO sees, in the
// order the list returns them.
func ctoDraftRows(t *testing.T, n *node) []draftRow {
	t.Helper()
	out, err := n.d.readCTOTool(context.Background(), "cto_owner_items_find", json.RawMessage(`{"kind":"draft"}`))
	if err != nil {
		t.Fatalf("cto_owner_items_find draft: %v", err)
	}
	raw, _ := json.Marshal(out)
	var page struct {
		Items []draftRow `json:"items"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	var rows []draftRow
	for _, r := range page.Items {
		if strings.HasPrefix(r.ID, "cd_") {
			rows = append(rows, r)
		}
	}
	return rows
}

// The CTO reads a pending charter draft in full, by list and by id, then
// applies one and rejects another with cto_op learned_answer. An apply is
// undone with the graph_undo the notice names.
func TestCTOReadsAndDecidesCharterDrafts(t *testing.T) {
	ctx := context.Background()
	g, n, client, id := ctoDraftFixture(t)
	var purpose, refuses protocol.CharterDraftResult
	for _, q := range []struct {
		req protocol.CharterDraftRequest
		out *protocol.CharterDraftResult
	}{
		{protocol.CharterDraftRequest{Level: "repo", Attr: "purpose", Value: "A small demo repo"}, &purpose},
		{protocol.CharterDraftRequest{Level: "repo", Attr: "refuses", Value: "live trading"}, &refuses},
	} {
		if err := json.Unmarshal(client.mustCall(protocol.OpCharterDraft, q.req), q.out); err != nil || q.out.ID == 0 {
			t.Fatalf("draft: %v", err)
		}
	}

	read := func(args string) json.RawMessage {
		t.Helper()
		out, err := n.d.readCTOTool(ctx, "cto_owner_items_find", json.RawMessage(args))
		if err != nil {
			t.Fatalf("cto_owner_items_find %s: %v", args, err)
		}
		raw, _ := json.Marshal(out)
		return raw
	}
	listed := ctoDraftRows(t, n)
	if len(listed) != 2 {
		t.Fatalf("draft list: %+v", listed)
	}
	first := listed[0]
	if first.Level != "repo" || first.Scope != "demo" || first.Attr != "purpose" || first.Value != "A small demo repo" ||
		first.Source != protocol.CharterDraftSource || first.DraftedBy != id || first.Status != "pending" ||
		!strings.Contains(first.Answer, "learned_answer") {
		t.Fatalf("draft row: %+v", first)
	}
	var one draftRow
	if err := json.Unmarshal(read(`{"id":"`+first.ID+`"}`), &one); err != nil || one.Value != first.Value || one.Attr != "purpose" {
		t.Fatalf("read by id: %+v %v", one, err)
	}

	decide := func(draft int64, action string) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"area": "operator", "method": "learned_answer", "arguments": map[string]any{"suggestion_id": -draft, "action": action}})
		out, err := n.d.runCTOTool(ctx, "settings_change", raw)
		if err != nil {
			t.Fatalf("learned_answer %s: %v", action, err)
		}
		body, _ := json.Marshal(out)
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		return m
	}
	applied := decide(purpose.ID, "accept")
	if applied["status"] != "accepted" || applied["decided_by"] != "cto" || applied["decided_at"] == nil || applied["history_id"] == nil {
		t.Fatalf("apply result is not the stored row: %v", applied)
	}
	if c, _ := g.gw.Store().EffectiveCharter("demo", "demo", ""); c.Purpose != "A small demo repo" {
		t.Fatalf("purpose %q", c.Purpose)
	}
	rejected := decide(refuses.ID, "reject")
	if rejected["status"] != "rejected" || rejected["decided_by"] != "cto" || rejected["history_id"] != nil {
		t.Fatalf("reject result: %v", rejected)
	}
	if c, _ := g.gw.Store().EffectiveCharter("demo", "demo", ""); strings.Contains(c.Refuses, "live trading") {
		t.Fatalf("a rejected draft was written: %q", c.Refuses)
	}
	if left := ctoDraftRows(t, n); len(left) != 0 {
		t.Fatalf("decided drafts still listed: %+v", left)
	}
	// The apply is undone with graph_undo on its history entry.
	history := int64(applied["history_id"].(float64))
	undo, _ := json.Marshal(map[string]any{"action": "undo", "id": history})
	if _, err := n.d.runCTOTool(ctx, "graph_change", undo); err != nil {
		t.Fatalf("graph_undo: %v", err)
	}
	if c, _ := g.gw.Store().EffectiveCharter("demo", "demo", ""); c.Purpose == "A small demo repo" {
		t.Fatalf("undo left the purpose %q", c.Purpose)
	}
}

// Drafts are listed oldest first. The second draft is made in a later
// second than the first, so a list that orders by the second-precision
// timestamp, newest first, puts the second draft ahead.
func TestCTODraftsListOldestFirstAcrossSeconds(t *testing.T) {
	_, n, client, _ := ctoDraftFixture(t)
	var purpose, refuses protocol.CharterDraftResult
	if err := json.Unmarshal(client.mustCall(protocol.OpCharterDraft, protocol.CharterDraftRequest{Level: "repo", Attr: "purpose", Value: "A small demo repo"}), &purpose); err != nil || purpose.ID == 0 {
		t.Fatalf("draft purpose: %v", err)
	}
	start := time.Now().Unix()
	eventually(t, "clock moves into the next second", func() bool { return time.Now().Unix() > start })
	if err := json.Unmarshal(client.mustCall(protocol.OpCharterDraft, protocol.CharterDraftRequest{Level: "repo", Attr: "refuses", Value: "live trading"}), &refuses); err != nil || refuses.ID == 0 {
		t.Fatalf("draft refuses: %v", err)
	}
	listed := ctoDraftRows(t, n)
	if len(listed) != 2 || listed[0].Attr != "purpose" || listed[1].Attr != "refuses" {
		t.Fatalf("draft order: %+v", listed)
	}
}

// Drafts made in the same second keep the order of their numbers. The
// numbers are compared as numbers: draft 9 comes before draft 10.
func TestCharterDraftsOrderByTimeThenNumber(t *testing.T) {
	drafts := []protocol.CharterDraft{
		{ID: 10, CreatedAt: 5_000_000_000},
		{ID: 9, CreatedAt: 5_000_000_000},
		{ID: 2, CreatedAt: 4_000_000_000},
	}
	sortCharterDraftsOldestFirst(drafts)
	want := []int64{2, 9, 10}
	for i, d := range drafts {
		if d.ID != want[i] {
			t.Fatalf("draft %d is %d, want %d", i, d.ID, want[i])
		}
	}
}
