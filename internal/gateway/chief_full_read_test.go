package gateway

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// readingAsk asks for the full read of one ask as the live chief. It is the
// same tool call the chief makes with agent_ask_sort and detail full.
func readingAsk(t *testing.T, s *Store, chief, node, id string) protocol.AskFull {
	t.Helper()
	res, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{Ask: id, Detail: "full"}})
	if err != nil {
		t.Fatalf("read ask %s: %v", id, err)
	}
	if res.Result != "read" || res.Full == nil {
		t.Fatalf("read ask %s: %+v", id, res)
	}
	return *res.Full
}

// The chief's batch shows only a summary. The full read of a draft-charter ask
// returns every field and its proposed text, the whole question, the
// recommendation and options, the source and the routing.
func TestAskFullReadDraftCharter(t *testing.T) {
	s, c, chief, asker, node := chiefRig(t)
	_ = c
	now := s.now().UnixNano()
	want := map[string]string{
		"repo purpose":           "Demo service",
		"repo maturity":          "early",
		"agent accepts":          "bug fixes and reviews",
		"agent feature_requests": "ideas from users",
	}
	for _, f := range [][3]string{
		{"repo", "purpose", want["repo purpose"]},
		{"repo", "maturity", want["repo maturity"]},
		{"agent", "accepts", want["agent accepts"]},
		{"agent", "feature_requests", want["agent feature_requests"]},
	} {
		if _, err := s.db.Exec(`INSERT INTO charter_drafts(level,scope,attr,value,source,source_hash,drafted_by,created_at) VALUES(?,?,?,?,'AGENTS.md','h',?,?)`,
			f[0], "demo", f[1], f[2], asker, now); err != nil {
			t.Fatal(err)
		}
	}
	text := "Charter draft for demo: 4 fields"
	detail := "Drafted from AGENTS.md:\n- repo purpose = Demo service\n- agent accepts = bug fixes and reviews"
	item, err := s.createInboxItem(s.db, protocol.InboxItem{
		Kind: protocol.AskKindDraft, SessionID: gatewayAsker, FromKind: protocol.AskFromGateway, Node: "demo",
		AskerName: "charter draft", SourceRef: "drafts:node:demo", AboutKind: protocol.AskAboutProject, AboutID: "demo",
		Text: text, Recommendation: "accept the drafts", Options: []string{"accept", "reject"}, Detail: detail,
	})
	if err != nil {
		t.Fatal(err)
	}

	full := readingAsk(t, s, chief, node, item.ID)
	if full.Kind != protocol.AskKindDraft || full.Status != "open" || full.State != protocol.AskWithSomeone {
		t.Fatalf("kind, status and state: %+v", full)
	}
	if full.Question != text || full.Detail != detail || full.Recommendation != "accept the drafts" {
		t.Fatalf("question, detail and recommendation: %+v", full)
	}
	if len(full.Options) != 2 || full.Options[0] != "accept" || full.Options[1] != "reject" {
		t.Fatalf("options: %v", full.Options)
	}
	if full.FromSession != gatewayAsker || full.FromKind != protocol.AskFromGateway {
		t.Fatalf("source: %+v", full)
	}
	if full.Next != protocol.AskNextChief || full.NextName != "chief of staff" {
		t.Fatalf("route: %+v", full)
	}
	got := map[string]string{}
	for _, f := range full.Charter {
		got[f.Level+" "+f.Attr] = f.Value
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("charter field %q = %q, want %q (all: %+v)", key, got[key], value, full.Charter)
		}
	}
}

// A learned proposal keeps its exact payload: the field, the phrase, the
// evidence and the value the graph would get.
func TestAskFullReadLearnedProposal(t *testing.T) {
	s, _, chief, _, node := chiefRig(t)
	now := s.now().UnixNano()
	res, err := s.db.Exec(`INSERT INTO learned_suggestions(node,field,edge_kind,kind,phrase,evidence,status,severity,created_at,ready_at) VALUES('demo','accepts','','add','billing','[{"id":"m1","excerpt":"billing is in the app"}]','pending','major',?,?)`,
		now, now-1)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	item, err := s.createInboxItem(s.db, protocol.InboxItem{
		Kind: protocol.AskKindLearned, SessionID: gatewayAsker, FromKind: protocol.AskFromGateway, Node: "demo",
		AskerName: "learned", SourceRef: "learned:" + strconv.FormatInt(id, 10), AboutKind: protocol.AskAboutProject, AboutID: "demo",
		Text: "Add billing to accepts", Recommendation: "accept", Options: []string{"accept", "reject"}, Detail: "said in a message",
	})
	if err != nil {
		t.Fatal(err)
	}

	full := readingAsk(t, s, chief, node, item.ID)
	if full.Learned == nil {
		t.Fatalf("no learned payload: %+v", full)
	}
	l := full.Learned
	if l.ID != id || l.Node != "demo" || l.Field != "accepts" || l.Phrase != "billing" || l.Kind != "add" || l.Severity != "major" {
		t.Fatalf("learned: %+v", l)
	}
	if len(l.Evidence) != 1 || l.Evidence[0].ID != "m1" || !strings.Contains(l.Evidence[0].Excerpt, "billing") {
		t.Fatalf("evidence: %+v", l.Evidence)
	}
	if !strings.Contains(l.NewValue, "billing") {
		t.Fatalf("new value: %q", l.NewValue)
	}
}

// The full question can name a token's scope and destination. The token value
// itself never leaves the gateway.
func TestAskFullReadHidesSecretValues(t *testing.T) {
	s, _, chief, asker, node := chiefRig(t)
	gh := "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	sk := "sk-abcdefghijklmnopqrstuvwxyz1234"
	id := chiefAskKind(t, s, asker, "approval",
		"Set the admin token.\nScope: nodes. Destination: ops.\nValue: "+gh)
	if _, err := s.db.Exec(`UPDATE inbox_items SET recommendation=? WHERE id=?`, "Use "+sk, id); err != nil {
		t.Fatal(err)
	}
	full := readingAsk(t, s, chief, node, id)
	raw, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	out := string(raw)
	for _, secret := range []string{gh, sk} {
		if strings.Contains(out, secret) {
			t.Fatalf("the full read returned a token: %s", out)
		}
	}
	if !strings.Contains(out, "Scope: nodes") || !strings.Contains(out, "Destination: ops") {
		t.Fatalf("the scope and destination were lost: %s", out)
	}
}
