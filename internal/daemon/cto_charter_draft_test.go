package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The CTO reads a pending charter draft in full, by list and by id, then
// applies one and rejects another with cto_op learned_answer. An apply is
// undone with the graph_undo the notice names.
func TestCTOReadsAndDecidesCharterDrafts(t *testing.T) {
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

	type row struct {
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
	read := func(args string) json.RawMessage {
		t.Helper()
		out, err := n.d.readCTOTool(ctx, "cto_proposals_find", json.RawMessage(args))
		if err != nil {
			t.Fatalf("cto_proposals_find %s: %v", args, err)
		}
		raw, _ := json.Marshal(out)
		return raw
	}
	// The list holds the draft rows (cd_ ids) and the Desk ask about them.
	drafts := func() []row {
		t.Helper()
		var page struct {
			Items []row `json:"items"`
		}
		if err := json.Unmarshal(read(`{"kind":"draft"}`), &page); err != nil {
			t.Fatal(err)
		}
		var out []row
		for _, r := range page.Items {
			if strings.HasPrefix(r.ID, "cd_") {
				out = append(out, r)
			}
		}
		return out
	}
	listed := drafts()
	if len(listed) != 2 {
		t.Fatalf("draft list: %+v", listed)
	}
	first := listed[0]
	if first.Level != "repo" || first.Scope != "demo" || first.Attr != "purpose" || first.Value != "A small demo repo" ||
		first.Source != protocol.CharterDraftSource || first.DraftedBy != id || first.Status != "pending" ||
		!strings.Contains(first.Answer, "learned_answer") {
		t.Fatalf("draft row: %+v", first)
	}
	var one row
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
	if left := drafts(); len(left) != 0 {
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
