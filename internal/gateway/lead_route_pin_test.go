package gateway

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// leadRig is one Repo with a pinned lead session, two member sessions of the
// same Agent, and a sender outside the Repo. It matches the homelab routing
// fault: members share the lead Agent, and the pinned lead session is the one
// that must get a redirected request.
type leadRig struct {
	s                          *Store
	clk                        *clock
	node, lead, agent          string
	member1, member2, outsider string
}

func newLeadRig(t *testing.T) *leadRig {
	t.Helper()
	s, clk := newTestStore(t)
	s.db.Exec(`UPDATE graph_ref_gate SET enabled=1 WHERE id=1`)
	if err := s.loadGraphRefs(); err != nil {
		t.Fatal(err)
	}
	ids, node := graphSessions(t, s, "demo", "other")
	lead := ids["demo"]
	var agent string
	if err := s.db.QueryRow(`SELECT agent_id FROM sessions WHERE id=?`, lead).Scan(&agent); err != nil {
		t.Fatal(err)
	}
	r := &leadRig{s: s, clk: clk, node: node, lead: lead, agent: agent, outsider: ids["other"]}
	for i, name := range []string{"homelab-agent 2", "homelab-agent 3"} {
		id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "member-" + name,
			Cwd: "/w/demo/" + name, Name: name, Repo: protocol.RepoInfo{Root: "/w/demo", Project: "demo"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`UPDATE sessions SET agent_id=?,last_seen=? WHERE id=?`, agent, clk.now().Add(time.Duration(2-i)*time.Second).UnixNano(), id); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			r.member1 = id
		} else {
			r.member2 = id
		}
	}
	if _, err := s.db.Exec(`UPDATE agents SET lead_session=? WHERE id=?`, lead, agent); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *leadRig) splitLeadSession(t *testing.T) {
	t.Helper()
	if err := r.s.AddArchitect("homelab-lead", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET agent_id='homelab-lead' WHERE id=?`, r.lead); err != nil {
		t.Fatal(err)
	}
}

func (r *leadRig) setWaiting(t *testing.T, id string, waiting bool) {
	t.Helper()
	value := ""
	if waiting {
		raw, _ := json.Marshal(protocol.Waiting{Kind: protocol.WaitingInput, Text: "deploy approval", Since: r.clk.now().Unix()})
		value = string(raw)
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET online=1,waiting=? WHERE id=?`, value, id); err != nil {
		t.Fatal(err)
	}
}

// A message from one member's Agent to another member of the same Agent must
// not bounce between members: it goes to the pinned lead session.
func TestLeadRouteMemberToMemberUsesPinnedLeadSession(t *testing.T) {
	r := newLeadRig(t)
	// The live homelab split: the pinned lead session belongs to its own Agent
	// node while the members still belong to the lead Agent.
	r.splitLeadSession(t)
	m, _, note, err := r.s.StoreLeadMessage(protocol.MsgSend{Sender: r.outsider, Recipient: r.member1, Body: "deploy it"})
	if err != nil {
		t.Fatal(err)
	}
	if m.To != r.lead {
		t.Fatalf("message went to %s, want the pinned lead %s (members %s, %s)", m.To, r.lead, r.member1, r.member2)
	}
	if !strings.Contains(note, "Redirected") || !strings.Contains(m.Body, "You lead ") {
		t.Fatalf("redirect note %q or lead line missing: %q", note, m.Body)
	}
	if m.To == r.member2 {
		t.Fatal("message bounced to the other member")
	}
}

// A message addressed to the pinned lead session stays with it even when the
// session now belongs to another Agent node. The Agent that pins it must not
// redirect the message away to one of its own members.
func TestLeadRouteKeepsMessageToPinnedLeadSessionOfAnotherAgent(t *testing.T) {
	r := newLeadRig(t)
	r.splitLeadSession(t)
	m, _, note, err := r.s.StoreLeadMessage(protocol.MsgSend{Sender: r.outsider, Recipient: r.lead, Body: "deploy it"})
	if err != nil {
		t.Fatal(err)
	}
	if m.To != r.lead || note != "" {
		t.Fatalf("message went to %s with note %q, want it to stay with %s", m.To, note, r.lead)
	}
}

// An offline pinned lead session still gets the request, queued for its wake.
// Another live member of the same Agent must not take it.
func TestLeadRouteOfflinePinnedLeadGetsTheMessage(t *testing.T) {
	r := newLeadRig(t)
	if err := r.s.SetSessionOffline(r.node, r.lead); err != nil {
		t.Fatal(err)
	}
	m, _, note, err := r.s.StoreLeadMessage(protocol.MsgSend{Sender: r.outsider, Recipient: r.member1, Body: "deploy it"})
	if err != nil {
		t.Fatal(err)
	}
	if m.To != r.lead {
		t.Fatalf("message went to %s, want the offline pinned lead %s (note %q)", m.To, r.lead, note)
	}
	if m.State != protocol.StateStored {
		t.Fatalf("state %s, want stored for the wake", m.State)
	}
}

// A lead session that waits for its user cannot take the request. Another
// live session of the lead Agent gets it, and it is not addressed as the lead.
func TestLeadRouteSkipsWaitingLeadSession(t *testing.T) {
	r := newLeadRig(t)
	r.setWaiting(t, r.lead, true)
	m, _, note, err := r.s.StoreLeadMessage(protocol.MsgSend{Sender: r.outsider, Recipient: r.member2, Body: "deploy it"})
	if err != nil {
		t.Fatal(err)
	}
	if m.To != r.member1 {
		t.Fatalf("message went to %s, want the other live session %s (note %q)", m.To, r.member1, note)
	}
	if strings.Contains(m.Body, "You lead ") {
		t.Fatalf("a fallback session was told it leads: %q", m.Body)
	}
	if !strings.Contains(m.Body, "waiting for its user") {
		t.Fatalf("fallback body does not say why: %q", m.Body)
	}
}

// With no other live session, a waiting lead means the message is delivered as
// addressed and the sender's note says why.
func TestLeadRouteWaitingLeadFallsBackToAddressedWithNote(t *testing.T) {
	r := newLeadRig(t)
	r.setWaiting(t, r.lead, true)
	for _, id := range []string{r.member1, r.member2} {
		if err := r.s.SetSessionOffline(r.node, id); err != nil {
			t.Fatal(err)
		}
	}
	m, _, note, err := r.s.StoreLeadMessage(protocol.MsgSend{Sender: r.outsider, Recipient: r.member2, Body: "deploy it"})
	if err != nil {
		t.Fatal(err)
	}
	if m.To != r.member2 {
		t.Fatalf("message went to %s, want the addressed session %s", m.To, r.member2)
	}
	if strings.Contains(note, "Redirected") || !strings.Contains(note, "waiting for its user") {
		t.Fatalf("note %q does not say the lead waits", note)
	}
}

// redirectReminder returns agent-net's notices of one kind whose key names the
// request.
func redirectReminder(t *testing.T, s *Store, request string) []protocol.Message {
	t.Helper()
	rows, err := s.db.Query(`SELECT id,sender,recipient,type,body,idem_key FROM messages
		WHERE sender='agent-net' AND idem_key LIKE ? ORDER BY rowid`, "redirect-reminder:%"+request)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []protocol.Message
	for rows.Next() {
		var m protocol.Message
		if err := rows.Scan(&m.ID, &m.From, &m.To, &m.Type, &m.Body, &m.IdempotencyKey); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

// The new nudge reuses the chief's nudge pause: a paused Agent gets no
// redirect notice.
func TestRedirectReminderRespectsPause(t *testing.T) {
	r := newLeadRig(t)
	chief, err := r.s.UpsertSession(r.node, protocol.SessionInfo{Harness: "claude", NativeID: "chief", Name: "chief", Cwd: "/w/chief"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	var agent string
	if err := r.s.db.QueryRow(`SELECT agent_id FROM sessions WHERE id=?`, r.outsider).Scan(&agent); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.PauseNudge(r.node, protocol.NudgePauseRequest{SessionID: chief, NudgePauseArgs: protocol.NudgePauseArgs{Kind: "redirect_reminder", Agent: agent, Mode: "apply"}}); err != nil {
		t.Fatal(err)
	}
	m, _, _, err := r.s.StoreLeadMessage(protocol.MsgSend{Sender: r.outsider, Recipient: r.member1, Body: "deploy it"})
	if err != nil {
		t.Fatal(err)
	}
	r.clk.t = r.clk.t.Add(reminderGrace + time.Minute)
	if _, err := r.s.remindPendingBefore(r.s.now(), nil); err != nil {
		t.Fatal(err)
	}
	if got := redirectReminder(t, r.s, m.ID); len(got) != 0 {
		t.Fatalf("paused notice: %+v", got)
	}
}

// A redirect notice waits for the same grace as the reply reminder, also when
// the pass looks an hour ahead for batching.
func TestRedirectReminderWaitsForGrace(t *testing.T) {
	r := newLeadRig(t)
	m, _, _, err := r.s.StoreLeadMessage(protocol.MsgSend{Sender: r.outsider, Recipient: r.member1, Body: "deploy it"})
	if err != nil {
		t.Fatal(err)
	}
	r.clk.t = r.clk.t.Add(reminderGrace - time.Minute)
	if _, err := r.s.remindPendingBefore(r.s.now().Add(nudgeLookahead), nil); err != nil {
		t.Fatal(err)
	}
	if got := redirectReminder(t, r.s, m.ID); len(got) != 0 {
		t.Fatalf("notice before the grace: %+v", got)
	}
	r.clk.t = r.clk.t.Add(2 * time.Minute)
	if _, err := r.s.remindPendingBefore(r.s.now().Add(nudgeLookahead), nil); err != nil {
		t.Fatal(err)
	}
	if got := redirectReminder(t, r.s, m.ID); len(got) != 1 {
		t.Fatalf("notice after the grace: %+v", got)
	}
}

// A redirected request that nobody answers reaches its sender as one nudge
// after the reminder grace, using the existing nudge machinery.
func TestRedirectReminderNotifiesSender(t *testing.T) {
	r := newLeadRig(t)
	m, _, _, err := r.s.StoreLeadMessage(protocol.MsgSend{Sender: r.outsider, Recipient: r.member1, Body: "deploy it"})
	if err != nil {
		t.Fatal(err)
	}
	r.clk.t = r.clk.t.Add(reminderGrace + time.Minute)
	if _, err := r.s.remindPendingBefore(r.s.now(), nil); err != nil {
		t.Fatal(err)
	}
	got := redirectReminder(t, r.s, m.ID)
	if len(got) != 1 || got[0].To != r.outsider || got[0].Type != protocol.TypeNotification {
		t.Fatalf("redirect reminder: %+v", got)
	}
	if !strings.Contains(got[0].Body, "no reply") || !strings.Contains(got[0].Body, m.ID) {
		t.Fatalf("body: %q", got[0].Body)
	}
	var fires int
	if err := r.s.db.QueryRow(`SELECT COUNT(*) FROM nudge_fires WHERE message_id=? AND kind='redirect_reminder'`, got[0].ID).Scan(&fires); err != nil || fires != 1 {
		t.Fatalf("nudge fire row: %d %v", fires, err)
	}
	if _, err := r.s.remindPendingBefore(r.s.now(), nil); err != nil {
		t.Fatal(err)
	}
	if again := redirectReminder(t, r.s, m.ID); len(again) != 1 {
		t.Fatalf("second notice stored: %+v", again)
	}
}

// When the owner approved the request, the chief of staff is told as well as
// the sender.
func TestRedirectReminderNotifiesChiefForOwnerApprovedRequest(t *testing.T) {
	r := newLeadRig(t)
	chief, err := r.s.UpsertSession(r.node, protocol.SessionInfo{Harness: "claude", NativeID: "chief", Name: "chief", Cwd: "/w/chief"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	m, _, _, err := r.s.StoreLeadMessage(protocol.MsgSend{Sender: r.outsider, Recipient: r.member1, Body: "deploy it"})
	if err != nil {
		t.Fatal(err)
	}
	// This is what the owner's approval writes on a held request.
	if _, err := r.s.db.Exec(`UPDATE messages SET owner_approved=1 WHERE id=?`, m.ID); err != nil {
		t.Fatal(err)
	}
	r.clk.t = r.clk.t.Add(reminderGrace + time.Minute)
	if _, err := r.s.remindPendingBefore(r.s.now(), nil); err != nil {
		t.Fatal(err)
	}
	rows, err := r.s.db.Query(`SELECT recipient,idem_key FROM messages WHERE sender='agent-net' AND idem_key IN (?,?) ORDER BY idem_key`,
		"redirect-reminder:"+m.ID, "redirect-reminder-chief:"+m.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var to, key string
		if err := rows.Scan(&to, &key); err != nil {
			t.Fatal(err)
		}
		got = append(got, to)
	}
	if len(got) != 2 || got[0] != chief || got[1] != r.outsider {
		t.Fatalf("reminder recipients: %v, want chief %s and sender %s", got, chief, r.outsider)
	}
}
