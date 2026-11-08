package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestLeadRouteOutsideAndCrew(t *testing.T) {
	s := teamStore(t)
	var lead, outsider, node string
	if err := s.db.QueryRow(`SELECT id,node_id FROM sessions WHERE repo_id='demo'`).Scan(&lead, &node); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='other'`).Scan(&outsider); err != nil {
		t.Fatal(err)
	}
	if err := s.AddArchitect("helper", "owner"); err != nil {
		t.Fatal(err)
	}
	target, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "helper", Repo: protocol.RepoInfo{Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE sessions SET agent_id='helper',name='Helper' WHERE id=?`, target); err != nil {
		t.Fatal(err)
	}
	send := func(from string) (protocol.Message, string) {
		t.Helper()
		m, _, route, e := s.StoreLeadMessage(protocol.MsgSend{Sender: from, Recipient: target, Body: "hello"})
		if e != nil {
			t.Fatal(e)
		}
		return m, route
	}
	m, route := send(outsider)
	if m.To != lead || !strings.Contains(m.Body, "originally sent to Helper") || !strings.Contains(route, "Redirected") {
		t.Fatalf("redirect: %+v %q", m, route)
	}
	m, route = send(lead)
	if m.To != target || strings.Contains(route, "Redirected") {
		t.Fatalf("crew redirected: %+v %q", m, route)
	}
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "owner", NativeID: protocol.OwnerNativeID})
	if err != nil {
		t.Fatal(err)
	}
	m, _ = send(owner)
	if m.To != target {
		t.Fatal("owner redirected")
	}
	cto, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "cto"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES('cto_session',?,0) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, cto); err != nil {
		t.Fatal(err)
	}
	m, _ = send(cto)
	if m.To != target {
		t.Fatal("CTO redirected")
	}
	if _, err = s.db.Exec(`UPDATE sessions SET online=0 WHERE id=?`, lead); err != nil {
		t.Fatal(err)
	}
	m, route = send(outsider)
	if m.To != target || !strings.Contains(route, "no live") {
		t.Fatalf("dead lead: %+v %q", m, route)
	}
}

func TestTeamLeadRoute(t *testing.T) {
	s := teamStore(t)
	var lead, target, outsider string
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='demo'`).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='other'`).Scan(&lead); err != nil {
		t.Fatal(err)
	}
	ids, _ := graphSessions(t, s, "outside")
	outsider = ids["outside"]
	repo := protocol.GraphRef{Kind: "repo", ID: "demo"}
	team := protocol.GraphRef{Kind: "team", ID: "alpha"}
	agent := protocol.GraphRef{Kind: "agent", ID: "other"}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha", Owner: &agent})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &repo})
	if err := s.SetLead(protocol.LeadSetRequest{Ref: repo}, "owner", false); err != nil {
		t.Fatal(err)
	}
	m, _, _, err := s.StoreLeadMessage(protocol.MsgSend{Sender: outsider, Recipient: target, Body: "hello"})
	if err != nil || m.To != lead || !strings.Contains(m.Body, "lead of Alpha") {
		t.Fatalf("team route: %+v %v", m, err)
	}
	_ = team
}

func TestLeadRouteWorkerCallerAndNoLead(t *testing.T) {
	s := teamStore(t)
	var caller, node, outsider string
	if err := s.db.QueryRow(`SELECT id,node_id FROM sessions WHERE repo_id='demo'`).Scan(&caller, &node); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='other'`).Scan(&outsider); err != nil {
		t.Fatal(err)
	}
	if err := s.AddArchitect("helper", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET agent_id='helper' WHERE id=?`, caller); err != nil {
		t.Fatal(err)
	}
	worker, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "worker", Repo: protocol.RepoInfo{Project: "other"}, Worker: &protocol.WorkerInfo{ID: "w", Owner: caller}})
	if err != nil {
		t.Fatal(err)
	}
	m, _, _, err := s.StoreLeadMessage(protocol.MsgSend{Sender: worker, Recipient: caller, Body: "finished"})
	if err != nil || m.To != caller {
		t.Fatalf("worker caller: %+v %v", m, err)
	}
	if err = s.SetLead(protocol.LeadSetRequest{Ref: protocol.GraphRef{Kind: "repo", ID: "demo"}}, "owner", false); err != nil {
		t.Fatal(err)
	}
	m, _, note, err := s.StoreLeadMessage(protocol.MsgSend{Sender: outsider, Recipient: caller, Body: "hello"})
	if err != nil || m.To != caller || !strings.Contains(note, "No lead") {
		t.Fatalf("no lead: %+v %q %v", m, note, err)
	}
}

func TestLeadRouteIdempotentReceipt(t *testing.T) {
	s := teamStore(t)
	var lead, node, outsider string
	if err := s.db.QueryRow(`SELECT id,node_id FROM sessions WHERE repo_id='demo'`).Scan(&lead, &node); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='other'`).Scan(&outsider); err != nil {
		t.Fatal(err)
	}
	if err := s.AddArchitect("helper", "owner"); err != nil {
		t.Fatal(err)
	}
	target, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "helper", Repo: protocol.RepoInfo{Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE sessions SET agent_id='helper' WHERE id=?`, target); err != nil {
		t.Fatal(err)
	}
	q := protocol.MsgSend{Sender: outsider, Recipient: target, Body: "hello", IdempotencyKey: "same"}
	first, _, note, err := s.StoreLeadMessage(q)
	if err != nil || first.To != lead || note == "" {
		t.Fatalf("first: %+v %q %v", first, note, err)
	}
	second, created, again, err := s.StoreLeadMessage(q)
	if err != nil || created || first.ID != second.ID || note != again {
		t.Fatalf("retry: %+v %q %v", second, again, err)
	}
}

// A reply must reach the session that sent the message it answers, even when
// the sender is outside the recipient's team. Only new requests and
// notifications still go to the lead.
func TestTeamLeadRouteReplyReachesParentSender(t *testing.T) {
	s := teamStore(t)
	var target, lead string
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='demo'`).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='other'`).Scan(&lead); err != nil {
		t.Fatal(err)
	}
	ids, _ := graphSessions(t, s, "outside")
	outsider := ids["outside"]
	repo := protocol.GraphRef{Kind: "repo", ID: "demo"}
	agent := protocol.GraphRef{Kind: "agent", ID: "other"}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha", Owner: &agent})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &repo})
	if err := s.SetLead(protocol.LeadSetRequest{Ref: repo}, "owner", false); err != nil {
		t.Fatal(err)
	}

	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: target, Recipient: outsider, Body: "question"})
	if err != nil {
		t.Fatal(err)
	}
	reply, _, note, err := s.StoreLeadMessage(protocol.MsgSend{Sender: outsider, Recipient: target, ParentID: req.ID, Body: "answer"})
	if err != nil || reply.To != target || note != "" {
		t.Fatalf("reply went to %s instead of %s (note %q): %v", reply.To, target, note, err)
	}

	// A new request from the same outside session still goes to the lead.
	m, _, note, err := s.StoreLeadMessage(protocol.MsgSend{Sender: outsider, Recipient: target, Body: "new request"})
	if err != nil || m.To != lead || !strings.Contains(note, "Redirected") {
		t.Fatalf("new request: %+v %q %v", m, note, err)
	}
}

// The same holds for a Repo lead.
func TestRepoLeadRouteReplyReachesParentSender(t *testing.T) {
	s := teamStore(t)
	var lead, outsider, node string
	if err := s.db.QueryRow(`SELECT id,node_id FROM sessions WHERE repo_id='demo'`).Scan(&lead, &node); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='other'`).Scan(&outsider); err != nil {
		t.Fatal(err)
	}
	if err := s.AddArchitect("helper", "owner"); err != nil {
		t.Fatal(err)
	}
	target, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "helper", Repo: protocol.RepoInfo{Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE sessions SET agent_id='helper',name='Helper' WHERE id=?`, target); err != nil {
		t.Fatal(err)
	}

	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: target, Recipient: outsider, Body: "question"})
	if err != nil {
		t.Fatal(err)
	}
	reply, _, note, err := s.StoreLeadMessage(protocol.MsgSend{Sender: outsider, Recipient: target, ParentID: req.ID, Body: "answer"})
	if err != nil || reply.To != target || note != "" {
		t.Fatalf("reply went to %s instead of %s (note %q): %v", reply.To, target, note, err)
	}

	// A new request from the same outside session still goes to the lead.
	m, _, note, err := s.StoreLeadMessage(protocol.MsgSend{Sender: outsider, Recipient: target, Body: "new request"})
	if err != nil || m.To != lead || !strings.Contains(note, "Redirected") {
		t.Fatalf("new request: %+v %q %v", m, note, err)
	}
}

func TestLeadAddressResolution(t *testing.T) {
	s := teamStore(t)
	var demo string
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='demo'`).Scan(&demo); err != nil {
		t.Fatal(err)
	}
	agent := protocol.GraphRef{Kind: "agent", ID: "demo"}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha", Owner: &agent})
	for _, address := range []string{"repo:demo", "team:Alpha"} {
		got, err := s.ResolveLeadAddress(address)
		if err != nil || got != demo {
			t.Fatalf("%s: %s %v", address, got, err)
		}
	}
	if _, err := s.db.Exec(`UPDATE sessions SET online=0 WHERE id=?`, demo); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveLeadAddress("repo:demo"); err == nil || !strings.Contains(err.Error(), "no live") {
		t.Fatalf("dead lead: %v", err)
	}
}

func TestLeadExplicitRepoAddressStaysWithRepoLead(t *testing.T) {
	s := teamStore(t)
	var repoLead, teamLead string
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='demo'`).Scan(&repoLead); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='other'`).Scan(&teamLead); err != nil {
		t.Fatal(err)
	}
	ids, _ := graphSessions(t, s, "outside")
	outsider := ids["outside"]
	teamAgent := protocol.GraphRef{Kind: "agent", ID: "other"}
	repo := protocol.GraphRef{Kind: "repo", ID: "demo"}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha", Owner: &teamAgent})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &repo})
	m, _, _, err := s.StoreLeadMessage(protocol.MsgSend{Sender: outsider, Recipient: repoLead, LeadAddress: "repo:demo", Body: "hello"})
	if err != nil || m.To != repoLead {
		t.Fatalf("repo address routed to %s instead of %s (team %s): %v", m.To, repoLead, teamLead, err)
	}
	if _, _, _, err = s.StoreLeadMessage(protocol.MsgSend{Sender: outsider, Recipient: teamLead, LeadAddress: "repo:demo", Body: "forged"}); err == nil {
		t.Fatal("forged lead address accepted")
	}
}
