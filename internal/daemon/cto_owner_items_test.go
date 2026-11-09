package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A charter draft Inbox item has no live asking session. The CTO must be able
// to accept it through the answer tool, exactly as the owner does in the TUI.
func TestCTOAnswerCharterDraft(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	repo := newRepo(t, "demo")
	client, id := helloDelivery(t, n, "caller", repo, "claude-opus-4")
	eventually(t, "session and repo registered", func() bool {
		a, ok, err := g.gw.Store().Agent(id)
		return err == nil && ok && a.Repo.Project == "demo"
	})
	if err := g.gw.Store().SetCharter("demo", "maturity", "early", "owner"); err != nil {
		t.Fatal(err)
	}
	var draft protocol.CharterDraftResult
	raw := client.mustCall(protocol.OpCharterDraft, protocol.CharterDraftRequest{Level: "repo", Attr: "purpose", Value: "A small demo repo"})
	if err := json.Unmarshal(raw, &draft); err != nil || draft.ID == 0 {
		t.Fatalf("draft: %s %v", raw, err)
	}
	asks, err := g.gw.Store().ListAsks()
	if err != nil {
		t.Fatal(err)
	}
	var itemID string
	for _, a := range asks {
		if a.Kind == protocol.AskKindDraft {
			itemID = a.ID
		}
	}
	if itemID == "" {
		t.Fatalf("no draft ask: %+v", asks)
	}
	answer, _ := json.Marshal(map[string]string{"kind": "inbox", "id": itemID, "answer": "accept"})
	if _, err := n.d.runCTOToolRaw(context.Background(), "answer", answer); err != nil {
		t.Fatalf("CTO answer: %v", err)
	}
	if c, _ := g.gw.Store().EffectiveCharter("demo", "demo", ""); c.Purpose != "A small demo repo" {
		t.Fatalf("accepted draft purpose %q", c.Purpose)
	}
	asks, err = g.gw.Store().ListAsks()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range asks {
		if a.ID == itemID {
			t.Fatalf("draft ask is still open: %+v", a)
		}
	}
}

// An answer for an offline asking session is stored, not refused. The session
// gets it when it wakes.
func TestCTOAnswerOfflineQuestion(t *testing.T) {
	ctx := context.Background()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	st := g.gw.Store()
	agent, err := st.UpsertSession(w.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: "offline-question"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := st.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: agent, Text: "Can you check this?"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSessionOffline(w.cfg.NodeID, agent); err != nil {
		t.Fatal(err)
	}
	answer, _ := json.Marshal(map[string]string{"kind": "inbox", "id": item.ID, "answer": "yes"})
	if _, err := w.d.runCTOToolRaw(ctx, "answer", answer); err != nil {
		t.Fatalf("offline answer: %v", err)
	}
	items, err := st.ListInbox()
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range items {
		if got.ID == item.ID {
			if got.Status != "answered" || got.Answer != "yes" {
				t.Fatalf("answer lost: %+v", got)
			}
			return
		}
	}
	t.Fatalf("question item %s missing", item.ID)
}

// Every pending owner item is readable in one list, with the exact answer call
// that resolves it. Check notices and other people's work are left out.
func TestCTOOwnerItemsFind(t *testing.T) {
	ctx := context.Background()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	if err := w.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	st := g.gw.Store()
	agent, err := st.UpsertSession(w.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: "owner-items", Name: "builder", Status: "idle"})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := st.CreateInboxItem(protocol.InboxItem{
		Kind: protocol.AskKindDraft, SessionID: "agent-net", FromKind: protocol.AskFromGateway,
		AskerName: "charter draft", SourceRef: "draft:1", Text: "Charter draft for demo purpose: Demo?",
		Options: []string{"accept", "reject"},
	})
	if err != nil {
		t.Fatal(err)
	}
	question, err := st.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: agent, Text: "Which branch?", Options: []string{"main", "next"}})
	if err != nil {
		t.Fatal(err)
	}
	approval, err := st.CreateInboxItem(protocol.InboxItem{Kind: "approval", SessionID: agent, Text: "deploy demo"})
	if err != nil {
		t.Fatal(err)
	}
	// A check notice is not an ask and must not appear.
	if _, err := st.CreateInboxItem(protocol.InboxItem{Kind: "check", SessionID: agent, Text: "applied the tools", FromKind: protocol.AskFromCTO}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.d.st.db.Exec(`INSERT INTO cto_proposals(tool,action,args,reason,created_at,status,note,result) VALUES('cto_new','',?,'start a clean conversation',?,'open','','')`, `{}`, time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	out, err := w.d.runCTOToolRaw(ctx, "cto_proposals_find", json.RawMessage(`{"kind":"all"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(out)
	var page struct {
		Items []struct {
			ID      string   `json:"id"`
			Kind    string   `json:"kind"`
			From    string   `json:"from"`
			Summary string   `json:"summary"`
			Options []string `json:"options"`
			Answer  string   `json:"answer"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("%s: %v", body, err)
	}
	kinds := map[string]bool{}
	for _, item := range page.Items {
		kinds[item.Kind] = true
		if item.ID == "" || item.From == "" || item.Summary == "" || item.Answer == "" || !strings.Contains(item.Answer, " ") {
			t.Errorf("incomplete row: %+v", item)
		}
		switch item.Kind {
		case protocol.AskKindDraft:
			if item.ID != draft.ID || len(item.Options) != 2 {
				t.Errorf("draft row: %+v", item)
			}
		case "question":
			if item.ID != question.ID || len(item.Options) != 2 {
				t.Errorf("question row: %+v", item)
			}
		case "approval":
			if item.ID != approval.ID {
				t.Errorf("approval row: %+v", item)
			}
		case protocol.AskKindProposal:
		default:
			t.Errorf("unexpected kind %q in %s", item.Kind, body)
		}
	}
	for _, kind := range []string{protocol.AskKindDraft, protocol.AskKindProposal, "question", "approval"} {
		if !kinds[kind] {
			t.Errorf("missing %s in %s", kind, body)
		}
	}
	if kinds[protocol.AskKindCheck] {
		t.Errorf("a check notice appeared: %s", body)
	}
	filtered, err := w.d.runCTOToolRaw(ctx, "cto_proposals_find", json.RawMessage(`{"kind":"draft"}`))
	if err != nil {
		t.Fatal(err)
	}
	filteredBody, _ := json.Marshal(filtered)
	var onlyDrafts struct {
		Items []struct {
			Kind string `json:"kind"`
		} `json:"items"`
	}
	if err := json.Unmarshal(filteredBody, &onlyDrafts); err != nil {
		t.Fatal(err)
	}
	if len(onlyDrafts.Items) != 1 || onlyDrafts.Items[0].Kind != protocol.AskKindDraft {
		t.Fatalf("draft filter: %s", filteredBody)
	}
}

// The read must not turn a malformed stored proposal into a failed list.
func TestCTOOwnerItemsFindOpenProposalHasAnswer(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	if _, err := w.d.st.db.Exec(`INSERT INTO cto_proposals(tool,action,args,reason,created_at,status,note,result) VALUES('cto_new','',?,'',?,'open','','')`, `{}`, time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	out, err := w.d.runCTOToolRaw(context.Background(), "cto_proposals_find", json.RawMessage(`{"kind":"proposal"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(out)
	for _, want := range []string{`"action":""`, `"reason":""`, `"created":"`, "proposal_ask"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("proposal row lacks %s: %s", want, body)
		}
	}
}

// The CTO reads a pending major learned proposal and a pending charter
// suggestion through cto_proposals_find, and gets the exact call that decides
// each. An ordinary agent session cannot read the list.
func TestCTOReadsPendingLearnedAndDraft(t *testing.T) {
	ctx := context.Background()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	if err := w.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	st := g.gw.Store()
	learned, err := st.CreateInboxItem(protocol.InboxItem{
		Kind: protocol.AskKindLearned, SessionID: "agent-net", FromKind: protocol.AskFromGateway,
		AskerName: "learned", SourceRef: "learned:41", AboutKind: protocol.AskAboutProject, AboutID: "demo",
		Text: "Add to demo accepts: builds widgets", Options: []string{"accept", "reject"},
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := st.CreateInboxItem(protocol.InboxItem{
		Kind: protocol.AskKindDraft, SessionID: "agent-net", FromKind: protocol.AskFromGateway,
		AskerName: "charter draft", SourceRef: "draft:7",
		Text: "Charter draft for demo purpose: Demo?", Options: []string{"accept", "reject"},
	})
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		ID      string   `json:"id"`
		Kind    string   `json:"kind"`
		Answer  string   `json:"answer"`
		Action  string   `json:"action"`
		Options []string `json:"options"`
	}
	read := func(kind string) []row {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"kind": kind})
		out, err := w.d.runCTOToolRaw(ctx, "cto_proposals_find", raw)
		if err != nil {
			t.Fatalf("kind %s: %v", kind, err)
		}
		body, _ := json.Marshal(out)
		var page struct {
			Items []row `json:"items"`
			Total int   `json:"total"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		return page.Items
	}

	items := read("all")
	byID := map[string]row{}
	for _, item := range items {
		byID[item.ID] = item
	}
	gotLearned, ok := byID[learned.ID]
	if !ok || gotLearned.Kind != protocol.AskKindLearned {
		t.Fatalf("learned proposal missing from all: %+v", items)
	}
	if gotLearned.Action != "accept" || !strings.Contains(gotLearned.Answer, "learned_answer") || !strings.Contains(gotLearned.Answer, "41") {
		t.Fatalf("learned answer call = %+v", gotLearned)
	}
	gotDraft, ok := byID[draft.ID]
	if !ok || gotDraft.Kind != protocol.AskKindDraft || !strings.Contains(gotDraft.Answer, `"kind":"inbox"`) {
		t.Fatalf("charter suggestion row = %+v", gotDraft)
	}
	if got := read("learned"); len(got) != 1 || got[0].ID != learned.ID {
		t.Fatalf("learned filter = %+v", got)
	}

	agent, _ := helloPi(t, w.node, "not-the-cto", "/w/not-the-cto", true)
	if _, err := agent.call("cto_proposals_find", map[string]any{"kind": "all"}); err == nil {
		t.Fatal("an agent read the CTO owner items")
	}
}
