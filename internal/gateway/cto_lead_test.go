package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// ctoLeadFixture is a store with two agent sessions and a registered CTO.
func ctoLeadFixture(t *testing.T) (*Store, *clock, string, string, string) {
	t.Helper()
	s, c := newTestStore(t)
	node, alice, bob := conversationFixture(t, s)
	if _, err := s.db.Exec(`UPDATE graph_ref_gate SET enabled=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.loadGraphRefs(); err != nil {
		t.Fatal(err)
	}
	cto, err := s.RegisterCTOSession(node)
	if err != nil {
		t.Fatal(err)
	}
	return s, c, cto, alice, bob
}

// setAgentFor gives a session a lasting Agent. The agent row is created when
// the test did not make one already.
func setAgentFor(t *testing.T, s *Store, session, agent string) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES(?,?,?,0,0,0)`, agent, agent, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET agent_id=? WHERE id=?`, agent, session); err != nil {
		t.Fatal(err)
	}
}

func grantLeadLevel(t *testing.T, s *Store, agent, actor string) {
	t.Helper()
	ref := protocol.GraphRef{Kind: "agent", ID: agent}
	if err := s.SetLead(protocol.LeadSetRequest{Ref: ref, Lead: &ref}, actor, false); err != nil {
		t.Fatal(err)
	}
}

func TestCTOLeadLevelRequestAllowed(t *testing.T) {
	s, _, cto, alice, _ := ctoLeadFixture(t)
	setAgentFor(t, s, alice, "lead-agent")
	grantLeadLevel(t, s, "lead-agent", "owner")

	m, _, err := s.StoreMessage(protocol.MsgSend{Sender: alice, Recipient: cto, Body: "May I use your tool?", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatalf("lead-level request refused: %v", err)
	}
	if m.State != protocol.StateStored {
		t.Fatalf("state = %s, want stored", m.State)
	}
}

func TestCTOLeadLevelNotificationRefused(t *testing.T) {
	s, _, cto, alice, _ := ctoLeadFixture(t)
	setAgentFor(t, s, alice, "lead-agent")
	grantLeadLevel(t, s, "lead-agent", "owner")

	_, _, err := s.StoreMessage(protocol.MsgSend{Sender: alice, Recipient: cto, Body: "just so you know", Type: protocol.TypeNotification})
	if err == nil || !strings.Contains(err.Error(), "The CTO only takes replies. Ask your owner, or wait for the CTO to ask you.") {
		t.Fatalf("notification from a lead-level agent: %v", err)
	}
}

func TestCTOLeadLevelNonLeadRefused(t *testing.T) {
	s, _, cto, alice, _ := ctoLeadFixture(t)
	setAgentFor(t, s, alice, "plain-agent")

	_, _, err := s.StoreMessage(protocol.MsgSend{Sender: alice, Recipient: cto, Body: "hello", Type: protocol.TypeRequest})
	if err == nil || !strings.Contains(err.Error(), "The CTO only takes replies. Ask your owner, or wait for the CTO to ask you.") {
		t.Fatalf("non-lead request: %v", err)
	}
}

func TestCTOLeadLevelTeamLeadAndChief(t *testing.T) {
	t.Run("team lead", func(t *testing.T) {
		s, _, cto, alice, _ := ctoLeadFixture(t)
		setAgentFor(t, s, alice, "team-lead")
		if _, err := s.db.Exec(`INSERT INTO teams(id,name,owner_agent_id,active,created_at,updated_at) VALUES('t1','Crew','team-lead',1,0,0)`); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: alice, Recipient: cto, Body: "question", Type: protocol.TypeRequest}); err != nil {
			t.Fatalf("team lead request refused: %v", err)
		}
	})
	t.Run("chief of staff", func(t *testing.T) {
		s, _, cto, _, bob := ctoLeadFixture(t)
		setAgentFor(t, s, bob, "chief-agent")
		if err := s.SetChief(protocol.ChiefSetRequest{SessionID: bob}, "owner"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: bob, Recipient: cto, Body: "question", Type: protocol.TypeRequest}); err != nil {
			t.Fatalf("chief request refused: %v", err)
		}
	})
}

func TestCTOLeadRequestRateHoldReleases(t *testing.T) {
	s, c, cto, alice, _ := ctoLeadFixture(t)
	disableInboxLimit(t, s)
	setAgentFor(t, s, alice, "lead-agent")
	grantLeadLevel(t, s, "lead-agent", "owner")

	for i := 0; i < 3; i++ {
		if m, _, err := s.StoreMessage(protocol.MsgSend{Sender: alice, Recipient: cto, Body: "question", Type: protocol.TypeRequest}); err != nil || m.State != protocol.StateStored {
			t.Fatalf("request %d: %+v %v", i+1, m, err)
		}
	}
	m, _, route, err := s.StoreLeadMessage(protocol.MsgSend{Sender: alice, Recipient: cto, Body: "one more", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	if m.State != protocol.StateHeld {
		t.Fatalf("4th request state = %s, want held", m.State)
	}
	want := "held: over 3 per hour; it goes at 13:00"
	if !strings.Contains(route, want) {
		t.Fatalf("sender note = %q, want %q", route, want)
	}
	if held, err := s.HeldRequests(); err != nil || len(held) != 0 {
		t.Fatalf("rate hold became an owner ask: %+v %v", held, err)
	}

	c.t = c.t.Add(59 * time.Minute)
	if released, err := s.ReleaseCTOHolds(c.now()); err != nil || len(released) != 0 {
		t.Fatalf("released before the window: %+v %v", released, err)
	}
	c.t = c.t.Add(time.Minute)
	released, err := s.ReleaseCTOHolds(c.now())
	if err != nil || len(released) != 1 || released[0].ID != m.ID {
		t.Fatalf("released after the window: %+v %v", released, err)
	}
	got, ok, err := s.Message(m.ID)
	if err != nil || !ok || got.State != protocol.StateStored {
		t.Fatalf("released message state: %+v %v", got, err)
	}
}

func TestCTOAgentLeadLevelGrantUndo(t *testing.T) {
	s, _, cto, alice, _ := ctoLeadFixture(t)
	setAgentFor(t, s, alice, "lead-agent")
	grantLeadLevel(t, s, "lead-agent", "cto")

	history, err := s.CTOGraphHistory(0)
	if err != nil || len(history) != 1 {
		t.Fatalf("history: %+v %v", history, err)
	}
	if err = s.GraphUndo(history[0].ID, "owner"); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.StoreMessage(protocol.MsgSend{Sender: alice, Recipient: cto, Body: "hello", Type: protocol.TypeRequest})
	if err == nil || !strings.Contains(err.Error(), "The CTO only takes replies.") {
		t.Fatalf("lead level survived undo: %v", err)
	}
}

// The Graph and the CTO gate both read the lead level through leadBasis, so
// one test table pins the order: the grant, then the chief, then a team lead.
func TestLeadBasisOrder(t *testing.T) {
	cases := []struct {
		grant, chief, teamLead bool
		want                   string
	}{
		{false, false, false, ""},
		{false, false, true, protocol.LeadLevelTeamLead},
		{false, true, true, protocol.LeadLevelChief},
		{false, true, false, protocol.LeadLevelChief},
		{true, true, true, protocol.LeadLevelGrant},
		{true, false, false, protocol.LeadLevelGrant},
	}
	for _, c := range cases {
		if got := leadBasis(c.grant, c.chief, c.teamLead); got != c.want {
			t.Errorf("leadBasis(grant=%v, chief=%v, team=%v) = %q, want %q", c.grant, c.chief, c.teamLead, got, c.want)
		}
	}
}

// The graph read shows the grant on the Agent node, and the node loses it when
// the grant is cleared. The CTO gate reads the same grant.
func TestGraphShowsGrantedLeadLevel(t *testing.T) {
	s, _, _, alice, _ := ctoLeadFixture(t)
	setAgentFor(t, s, alice, "lead-agent")
	ref := protocol.GraphRef{Kind: "agent", ID: "lead-agent"}

	if got := agentGraphLeadLevel(t, s, ref); got != "" {
		t.Fatalf("lead level before the grant = %q", got)
	}
	grantLeadLevel(t, s, "lead-agent", "owner")
	if got := agentGraphLeadLevel(t, s, ref); got != protocol.LeadLevelGrant {
		t.Fatalf("lead level after the grant = %q, want %q", got, protocol.LeadLevelGrant)
	}
	if _, lead, err := ctoLeadLevel(s.db, alice); err != nil || !lead {
		t.Fatalf("CTO gate after the grant: lead=%v err=%v", lead, err)
	}
	if err := s.SetLead(protocol.LeadSetRequest{Ref: ref}, "owner", false); err != nil {
		t.Fatal(err)
	}
	if got := agentGraphLeadLevel(t, s, ref); got != "" {
		t.Fatalf("lead level after clearing = %q, want none", got)
	}
	if _, lead, err := ctoLeadLevel(s.db, alice); err != nil || lead {
		t.Fatalf("CTO gate after clearing: lead=%v err=%v", lead, err)
	}
}

// A team lead shows "team lead" with no grant, and a chief of staff shows
// "chief of staff". A grant on top of either shows "grant".
func TestGraphShowsTeamLeadAndChiefLeadLevel(t *testing.T) {
	s, _, _, alice, bob := ctoLeadFixture(t)
	setAgentFor(t, s, alice, "team-lead")
	if _, err := s.db.Exec(`INSERT INTO teams(id,name,owner_agent_id,active,created_at,updated_at) VALUES('t1','Crew','team-lead',1,0,0)`); err != nil {
		t.Fatal(err)
	}
	ref := protocol.GraphRef{Kind: "agent", ID: "team-lead"}
	if got := agentGraphLeadLevel(t, s, ref); got != protocol.LeadLevelTeamLead {
		t.Fatalf("team lead = %q, want %q", got, protocol.LeadLevelTeamLead)
	}
	grantLeadLevel(t, s, "team-lead", "owner")
	if got := agentGraphLeadLevel(t, s, ref); got != protocol.LeadLevelGrant {
		t.Fatalf("team lead with a grant = %q, want %q", got, protocol.LeadLevelGrant)
	}

	setAgentFor(t, s, bob, "chief-agent")
	if err := s.SetChief(protocol.ChiefSetRequest{SessionID: bob}, "owner"); err != nil {
		t.Fatal(err)
	}
	chief := protocol.GraphRef{Kind: "agent", ID: "chief-agent"}
	if got := agentGraphLeadLevel(t, s, chief); got != protocol.LeadLevelChief {
		t.Fatalf("chief of staff = %q, want %q", got, protocol.LeadLevelChief)
	}
}

// agentGraphLeadLevel reads the one Agent node from the typed graph.
func agentGraphLeadLevel(t *testing.T, s *Store, ref protocol.GraphRef) string {
	t.Helper()
	g, err := s.GraphForRef(&ref)
	if err != nil || len(g.Nodes) != 1 {
		t.Fatalf("graph read of %s: %+v %v", ref.ID, g, err)
	}
	return g.Nodes[0].LeadLevel
}
