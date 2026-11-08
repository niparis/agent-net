package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// placeRig is a team "Alpha" led by the agent "other", with the repo "demo" as
// its member. The lead session is the one of "other"; the member session is
// the one of "demo"; "outside" belongs to no team.
type placeRig struct {
	s                     *Store
	lead, member, outside string
}

func newPlaceRig(t *testing.T) placeRig {
	t.Helper()
	s := teamStore(t)
	r := placeRig{s: s}
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='other'`).Scan(&r.lead); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='demo'`).Scan(&r.member); err != nil {
		t.Fatal(err)
	}
	ids, _ := graphSessions(t, s, "outside")
	r.outside = ids["outside"]
	if _, err := s.db.Exec(`UPDATE sessions SET name='forge-lead' WHERE id=?`, r.lead); err != nil {
		t.Fatal(err)
	}
	agent := protocol.GraphRef{Kind: "agent", ID: "other"}
	repo := protocol.GraphRef{Kind: "repo", ID: "demo"}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha", Purpose: "Build  the forge\nand keep it green.", Owner: &agent})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &repo})
	return r
}

func TestPlaceTextLead(t *testing.T) {
	r := newPlaceRig(t)
	got, err := r.s.placeText(r.lead)
	if err != nil {
		t.Fatal(err)
	}
	want := "[agent-net] Your place, from agent-net:\n" +
		"You lead team Alpha (mode: charter first). Members: demo.\n" +
		"You report to the owner.\n" +
		"Requests from outside Alpha to a member come to you. Answer each one, or hand it off with agent_message_handoff so the member answers the original sender directly.\n" +
		"Team purpose: Build the forge and keep it green."
	if got != want {
		t.Fatalf("lead place:\n%s\nwant:\n%s", got, want)
	}
}

func TestPlaceTextMember(t *testing.T) {
	r := newPlaceRig(t)
	got, err := r.s.placeText(r.member)
	if err != nil {
		t.Fatal(err)
	}
	want := "[agent-net] Your place, from agent-net:\n" +
		"You are in team Alpha, led by forge-lead (mode: charter first).\n" +
		"An order from your lead binds you unless your charter refuses it; then say so, quote the charter line, and it goes up.\n" +
		"Escalate to your lead first, not to the owner."
	if got != want {
		t.Fatalf("member place:\n%s\nwant:\n%s", got, want)
	}
}

func TestPlaceTextNoTeam(t *testing.T) {
	r := newPlaceRig(t)
	got, err := r.s.placeText(r.outside)
	if err != nil || got != "" {
		t.Fatalf("session in no team: %q %v", got, err)
	}
	// An archived team no longer places anyone.
	if _, err = r.s.db.Exec(`UPDATE teams SET active=0 WHERE id='alpha'`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{r.lead, r.member} {
		if got, err = r.s.placeText(id); err != nil || got != "" {
			t.Fatalf("archived team still places %s: %q %v", id, got, err)
		}
	}
}

// The text is read from live team data on every call.
func TestPlaceTextFollowsTeamChanges(t *testing.T) {
	r := newPlaceRig(t)
	teamWrite(t, r.s, protocol.TeamWrite{Action: "purpose", ID: "alpha", Purpose: "New aim"})
	other := protocol.GraphRef{Kind: "repo", ID: "outside"}
	teamWrite(t, r.s, protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &other})
	got, err := r.s.placeText(r.lead)
	if err != nil || !strings.Contains(got, "Members: demo, outside.") || !strings.HasSuffix(got, "Team purpose: New aim") {
		t.Fatalf("lead place: %q %v", got, err)
	}
	// A lead session pinned to another session of the agent is the only one told.
	var other2 string
	if err = r.s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, r.lead).Scan(&other2); err != nil {
		t.Fatal(err)
	}
	second, err := r.s.UpsertSession(other2, protocol.SessionInfo{Harness: "pi", NativeID: "second", Repo: protocol.RepoInfo{Project: "other"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.s.db.Exec(`UPDATE teams SET lead_session=? WHERE id='alpha'`, second); err != nil {
		t.Fatal(err)
	}
	if got, err = r.s.placeText(r.lead); err != nil || strings.Contains(got, "You lead") {
		t.Fatalf("unpinned session still leads: %q %v", got, err)
	}
	if got, err = r.s.placeText(second); err != nil || !strings.Contains(got, "You lead team Alpha (mode: charter first).") {
		t.Fatalf("pinned session: %q %v", got, err)
	}
}

// A repo lead is told about its repo when other agents work in it.
func TestPlaceTextRepoLead(t *testing.T) {
	s := teamStore(t)
	var lead, node string
	if err := s.db.QueryRow(`SELECT id,node_id FROM sessions WHERE repo_id='demo'`).Scan(&lead, &node); err != nil {
		t.Fatal(err)
	}
	if got, err := s.placeText(lead); err != nil || got != "" {
		t.Fatalf("lone repo owner: %q %v", got, err)
	}
	if err := s.AddArchitect("helper", "owner"); err != nil {
		t.Fatal(err)
	}
	helper, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "helper", Repo: protocol.RepoInfo{Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE sessions SET agent_id='helper',name='Helper' WHERE id=?`, helper); err != nil {
		t.Fatal(err)
	}
	got, err := s.placeText(lead)
	want := "[agent-net] Your place, from agent-net:\n" +
		"You lead repo demo (mode: charter first). Members: helper.\n" +
		"You report to the owner.\n" +
		"Requests from outside demo to a member come to you. Answer each one, or hand it off with agent_message_handoff so the member answers the original sender directly."
	if err != nil || got != want {
		t.Fatalf("repo lead place:\n%s\nwant:\n%s\n%v", got, want, err)
	}
	if got, err = s.placeText(helper); err != nil || got != "" {
		t.Fatalf("repo member: %q %v", got, err)
	}
}

// context.get carries the place text after the node charter.
func TestContextGetCarriesPlace(t *testing.T) {
	r := newPlaceRig(t)
	got, err := r.s.contextText(r.lead)
	if err != nil || !strings.Contains(got, "You lead team Alpha (mode: charter first).") {
		t.Fatalf("context: %q %v", got, err)
	}
	if got, err = r.s.contextText(r.outside); err != nil || strings.Contains(got, "Your place") {
		t.Fatalf("no team context: %q %v", got, err)
	}
}

func TestRedirectNoteSaysWhatToDo(t *testing.T) {
	r := newPlaceRig(t)
	if _, err := r.s.db.Exec(`UPDATE sessions SET name='ingest-forge' WHERE id=?`, r.member); err != nil {
		t.Fatal(err)
	}
	m, _, route, err := r.s.StoreLeadMessage(protocol.MsgSend{Sender: r.outside, Recipient: r.member, Body: "please ingest"})
	if err != nil {
		t.Fatal(err)
	}
	wantNote := "Redirected to other (lead of Alpha); originally sent to ingest-forge."
	if route != wantNote {
		t.Fatalf("receipt note %q", route)
	}
	wantBody := wantNote + "\nYou lead Alpha: answer this or hand it off to ingest-forge with agent_message_handoff.\nplease ingest"
	if m.To != r.lead || m.Body != wantBody {
		t.Fatalf("body:\n%q\nwant:\n%q (to %s)", m.Body, wantBody, m.To)
	}
	// An idempotent retry still returns the one-line receipt note.
	q := protocol.MsgSend{Sender: r.outside, Recipient: r.member, Body: "again", IdempotencyKey: "k1"}
	if _, _, _, err = r.s.StoreLeadMessage(q); err != nil {
		t.Fatal(err)
	}
	_, created, again, err := r.s.StoreLeadMessage(q)
	if err != nil || created || again != wantNote {
		t.Fatalf("retry note %q created=%v %v", again, created, err)
	}
}

// The lead passes a request to a member. The member sees the original sender,
// answers the asker directly, and the lead gets a copy of the answer.
func TestHandoffKeepsSenderAndCopiesReplyToLead(t *testing.T) {
	r := newPlaceRig(t)
	routed, _, _, err := r.s.StoreLeadMessage(protocol.MsgSend{Sender: r.outside, Recipient: r.member, Body: "please ingest"})
	if err != nil || routed.To != r.lead {
		t.Fatalf("routed: %+v %v", routed, err)
	}
	h, err := r.s.StoreHandoff(protocol.MsgHandoff{Sender: r.lead, MessageID: routed.ID, Recipient: r.member, Note: "this is yours"})
	if err != nil {
		t.Fatal(err)
	}
	if h.From != r.outside || h.To != r.member || h.Type != protocol.TypeRequest || h.ConversationID != routed.ConversationID {
		t.Fatalf("handoff keeps sender: %+v", h)
	}
	for _, want := range []string{"Handed off to you by forge-lead", "this is yours", "please ingest"} {
		if !strings.Contains(h.Body, want) {
			t.Fatalf("handoff body lacks %q:\n%s", want, h.Body)
		}
	}
	if strings.Contains(h.Body, "Redirected to") || strings.Contains(h.Body, "You lead Alpha") {
		t.Fatalf("handoff body keeps the lead's routing note:\n%s", h.Body)
	}
	if got, _, _ := r.s.Message(routed.ID); got.State != protocol.StateReplied {
		t.Fatalf("the lead's copy of the request stays open: %s", got.State)
	}

	reply, _, route, err := r.s.StoreLeadMessage(protocol.MsgSend{Sender: r.member, Recipient: r.outside, ParentID: h.ID, Body: "done, ingested"})
	if err != nil || reply.To != r.outside || reply.From != r.member || route != "" {
		t.Fatalf("reply goes to the asker: %+v %q %v", reply, route, err)
	}
	var copyTo, copyBody, copyType, copyFrom string
	err = r.s.db.QueryRow(`SELECT recipient,body,type,sender FROM messages WHERE conversation_id=? AND recipient=? AND id<>?`, h.ConversationID, r.lead, routed.ID).Scan(&copyTo, &copyBody, &copyType, &copyFrom)
	if err != nil {
		t.Fatalf("no copy for the lead: %v", err)
	}
	if copyType != protocol.TypeNotification || copyFrom != r.member || !strings.Contains(copyBody, "done, ingested") {
		t.Fatalf("copy: %s %s %q", copyType, copyFrom, copyBody)
	}
	// Pending delivery includes the copy: it is an ordinary stored message.
	var stored int
	if err = r.s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE recipient=? AND state='stored' AND type='notification'`, r.lead).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("stored copies %d %v", stored, err)
	}
}

// A reply to an ordinary request, one nobody handed off, makes no copy.
func TestReplyWithoutHandoffMakesNoCopy(t *testing.T) {
	r := newPlaceRig(t)
	req, _, err := r.s.StoreMessage(protocol.MsgSend{Sender: r.member, Recipient: r.outside, Body: "question"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = r.s.StoreMessage(protocol.MsgSend{Sender: r.outside, Recipient: r.member, ParentID: req.ID, Body: "answer"}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = r.s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE type='notification'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("copies %d %v", n, err)
	}
}

// Only the recipient of a message can hand it off, and only to a real session.
func TestHandoffNeedsTheMessageToBeYours(t *testing.T) {
	r := newPlaceRig(t)
	routed, _, _, err := r.s.StoreLeadMessage(protocol.MsgSend{Sender: r.outside, Recipient: r.member, Body: "please ingest"})
	if err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]protocol.MsgHandoff{
		"not the recipient": {Sender: r.member, MessageID: routed.ID, Recipient: r.lead},
		"unknown message":   {Sender: r.lead, MessageID: "m_nope", Recipient: r.member},
		"unknown session":   {Sender: r.lead, MessageID: routed.ID, Recipient: "s_nope"},
		"to the asker":      {Sender: r.lead, MessageID: routed.ID, Recipient: r.outside},
		"to yourself":       {Sender: r.lead, MessageID: routed.ID, Recipient: r.lead},
	} {
		if _, err := r.s.StoreHandoff(p); err == nil {
			t.Errorf("%s: handoff accepted", name)
		}
	}
	// A reply cannot be handed off: it already has an asker's thread to answer.
	req, _, err := r.s.StoreMessage(protocol.MsgSend{Sender: r.lead, Recipient: r.outside, Body: "q"})
	if err != nil {
		t.Fatal(err)
	}
	rep, _, err := r.s.StoreMessage(protocol.MsgSend{Sender: r.outside, Recipient: r.lead, ParentID: req.ID, Body: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.s.StoreHandoff(protocol.MsgHandoff{Sender: r.lead, MessageID: rep.ID, Recipient: r.member}); err == nil {
		t.Error("reply handed off")
	}
}

// A retry with the same key returns the first hand-off.
func TestHandoffIsIdempotent(t *testing.T) {
	r := newPlaceRig(t)
	routed, _, _, err := r.s.StoreLeadMessage(protocol.MsgSend{Sender: r.outside, Recipient: r.member, Body: "please ingest"})
	if err != nil {
		t.Fatal(err)
	}
	p := protocol.MsgHandoff{Sender: r.lead, MessageID: routed.ID, Recipient: r.member, IdempotencyKey: "same"}
	first, err := r.s.StoreHandoff(p)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.s.StoreHandoff(p)
	if err != nil || second.ID != first.ID {
		t.Fatalf("retry: %+v %v", second, err)
	}
}

// The request rule from the asker to the member still applies to a hand-off.
func TestHandoffKeepsRequestRules(t *testing.T) {
	r := newPlaceRig(t)
	routed, _, _, err := r.s.StoreLeadMessage(protocol.MsgSend{Sender: r.outside, Recipient: r.member, Body: "please ingest"})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.s.SetEdge(protocol.Edge{From: "outside", To: "demo", Kind: "depends", Rule: "ask"}, "owner"); err != nil {
		t.Fatal(err)
	}
	held, err := r.s.StoreHandoff(protocol.MsgHandoff{Sender: r.lead, MessageID: routed.ID, Recipient: r.member, IdempotencyKey: "a"})
	if err != nil || held.State != protocol.StateHeld {
		t.Fatalf("ask rule: %+v %v", held, err)
	}
	if err = r.s.SetEdge(protocol.Edge{From: "outside", To: "demo", Kind: "depends", Rule: "never"}, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err = r.s.StoreHandoff(protocol.MsgHandoff{Sender: r.lead, MessageID: routed.ID, Recipient: r.member, IdempotencyKey: "b"}); err == nil {
		t.Fatal("hand-off passed a never rule")
	}
}

// A reply threaded by conversation, without a parent ID, also reaches the asker
// and the lead.
func TestHandoffReplyThreadedByConversation(t *testing.T) {
	r := newPlaceRig(t)
	routed, _, _, err := r.s.StoreLeadMessage(protocol.MsgSend{Sender: r.outside, Recipient: r.member, Body: "please ingest"})
	if err != nil {
		t.Fatal(err)
	}
	h, err := r.s.StoreHandoff(protocol.MsgHandoff{Sender: r.lead, MessageID: routed.ID, Recipient: r.member})
	if err != nil {
		t.Fatal(err)
	}
	reply, _, err := r.s.StoreMessage(protocol.MsgSend{Sender: r.member, Recipient: r.outside, ConversationID: h.ConversationID, Body: "done"})
	if err != nil || reply.To != r.outside {
		t.Fatalf("threaded reply: %+v %v", reply, err)
	}
	cp, ok, err := r.s.HandoffCopy(reply.ID)
	if err != nil || !ok || cp.To != r.lead || !strings.Contains(cp.Body, "done") {
		t.Fatalf("lead copy: %+v %v %v", cp, ok, err)
	}
}
