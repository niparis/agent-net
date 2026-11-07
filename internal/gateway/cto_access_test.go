package gateway

import (
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
)

func TestCTOOnlyAcceptsReplies(t *testing.T) {
	s, _ := newTestStore(t)
	node, agent, _ := conversationFixture(t, s)
	orchestrator, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "orchestrator", Name: "orchestrator"})
	if err != nil {
		t.Fatal(err)
	}
	cto, err := s.RegisterCTOSession(node)
	if err != nil {
		t.Fatal(err)
	}
	merger, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "merger", Repo: protocol.RepoInfo{Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO project_mergers(project,node_id,session_id) VALUES(?,?,?)`, "demo", node, merger); err != nil {
		t.Fatal(err)
	}
	for _, sender := range []string{agent, orchestrator, merger} {
		_, _, err := s.StoreMessage(protocol.MsgSend{Sender: sender, Recipient: cto, Body: "hello"})
		if err == nil || !strings.Contains(err.Error(), "The CTO only takes replies.") {
			t.Errorf("new message from %s: %v", sender, err)
		}
	}
	root, _, err := s.StoreMessage(protocol.MsgSend{Sender: cto, Recipient: agent, Body: "question", FromCTO: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []protocol.MsgSend{
		{Sender: agent, Recipient: cto, Body: "threaded request", ParentID: root.ID, Type: protocol.TypeRequest},
		{Sender: agent, Recipient: cto, Body: "threaded notice", ConversationID: root.ConversationID, Type: protocol.TypeNotification},
	} {
		if _, _, err = s.StoreMessage(request); err == nil || !strings.Contains(err.Error(), "The CTO only takes replies.") {
			t.Errorf("threaded non-reply accepted: %v", err)
		}
	}
	first, _, err := s.StoreMessage(protocol.MsgSend{Sender: agent, Recipient: cto, Body: "answer", ParentID: root.ID})
	if err != nil {
		t.Fatal(err)
	}
	follow, _, err := s.StoreMessage(protocol.MsgSend{Sender: cto, Recipient: agent, Body: "follow up", ParentID: first.ID, FromCTO: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.StoreMessage(protocol.MsgSend{Sender: agent, Recipient: cto, Body: "another answer", ParentID: follow.ID}); err != nil {
		t.Fatal(err)
	}
}

func TestCTOOwnerAndWorkerNotice(t *testing.T) {
	s, _ := newTestStore(t)
	node, agent, _ := conversationFixture(t, s)
	cto, err := s.RegisterCTOSession(node)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetEdge(protocol.Edge{From: "*", To: protocol.CTONodeName, Kind: "depends", Rule: "never"}, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.StoreMessage(protocol.MsgSend{Sender: owner, Recipient: cto, Body: "owner"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.StoreMessage(protocol.MsgSend{Sender: agent, Recipient: cto, Body: "forged notice", WorkerNotice: true}); err == nil {
		t.Fatal("agent forged worker notice")
	}
	worker, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "worker", Worker: &protocol.WorkerInfo{ID: "w_test", Owner: cto}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.StoreMessage(protocol.MsgSend{Sender: worker, Recipient: cto, Body: "unsolicited worker message"}); err == nil {
		t.Fatal("worker started a conversation")
	}
	if _, _, err = s.StoreMessage(protocol.MsgSend{Sender: worker, Recipient: cto, Body: "worker notice", WorkerNotice: true}); err != nil {
		t.Fatal(err)
	}
}

func TestCTONodeSettingGraphUndo(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("alpha", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeSetting("alpha", protocol.SettingDeployRule, protocol.DeployOwner, "cto"); err != nil {
		t.Fatal(err)
	}
	history, err := s.CTOGraphHistory(0)
	if err != nil || len(history) != 1 {
		t.Fatalf("history %+v %v", history, err)
	}
	if err = s.GraphUndo(history[0].ID, "owner"); err != nil {
		t.Fatal(err)
	}
	settings, err := s.NodeSettings("alpha")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(settings)
	if strings.Contains(string(raw), `"deploy_rule":"owner"`) {
		t.Fatalf("setting not undone: %s", raw)
	}
}

func TestCTOLeadGraphUndo(t *testing.T) {
	s := teamStore(t)
	team := protocol.GraphRef{Kind: "team", ID: "alpha"}
	a := protocol.GraphRef{Kind: "agent", ID: "demo"}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha"})
	if err := s.SetLead(protocol.LeadSetRequest{Ref: team, Lead: &a}, "cto", false); err != nil {
		t.Fatal(err)
	}
	history, err := s.CTOGraphHistory(0)
	if err != nil || len(history) != 1 {
		t.Fatalf("history %+v %v", history, err)
	}
	if err = s.GraphUndo(history[0].ID, "owner"); err != nil {
		t.Fatal(err)
	}
	graph, err := s.GraphForRef(&team)
	if err != nil || graph.Nodes[0].Lead != nil {
		t.Fatalf("lead not undone: %+v %v", graph, err)
	}
}

func TestCTORepoLeadUndoRestoresDefault(t *testing.T) {
	s := teamStore(t)
	repo := protocol.GraphRef{Kind: "repo", ID: "demo"}
	other := protocol.GraphRef{Kind: "agent", ID: "other"}
	if _, err := s.db.Exec(`UPDATE sessions SET agent_id='other' WHERE repo_id='demo'`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLead(protocol.LeadSetRequest{Ref: repo, Lead: &other}, "cto", false); err != nil {
		t.Fatal(err)
	}
	history, err := s.CTOGraphHistory(0)
	if err != nil || len(history) != 1 {
		t.Fatalf("history %+v %v", history, err)
	}
	if err = s.GraphUndo(history[0].ID, "owner"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM repo_leads WHERE repo_id='demo'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("default lead override remains: %d %v", count, err)
	}
}

func TestCTONodeRemoveStillRequiresApproval(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("alpha", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveArchitect("alpha", "cto"); err == nil {
		t.Fatal("CTO removed a node without approval")
	}
	if err := s.RemoveArchitect("alpha", "owner (CTO proposal p_1)"); err != nil {
		t.Fatal(err)
	}
}

func TestCTONodeSettingUndoCanBeUndone(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("alpha", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeSetting("alpha", protocol.SettingDeployRule, protocol.DeployOwner, "cto"); err != nil {
		t.Fatal(err)
	}
	history, err := s.CTOGraphHistory(0)
	if err != nil || len(history) != 1 {
		t.Fatal(history, err)
	}
	if err = s.GraphUndo(history[0].ID, "cto"); err != nil {
		t.Fatal(err)
	}
	history, err = s.CTOGraphHistory(0)
	if err != nil || len(history) != 2 {
		t.Fatalf("undo not recorded: %+v %v", history, err)
	}
	if err = s.GraphUndo(history[1].ID, "owner"); err != nil {
		t.Fatal(err)
	}
	settings, err := s.NodeSettings("alpha")
	raw, _ := json.Marshal(settings)
	if err != nil || !strings.Contains(string(raw), `"deploy_rule":"owner"`) {
		t.Fatalf("redo: %s %v", raw, err)
	}
}

func TestCTOTeamCreationUndoCanBeUndone(t *testing.T) {
	s := teamStore(t)
	if err := s.WriteTeam(protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha"}, "cto"); err != nil {
		t.Fatal(err)
	}
	history, err := s.CTOGraphHistory(0)
	if err != nil || len(history) != 1 {
		t.Fatal(history, err)
	}
	if err = s.GraphUndo(history[0].ID, "cto"); err != nil {
		t.Fatal(err)
	}
	teamHistory, err := s.TeamHistory("alpha", 10)
	if err != nil || len(teamHistory) != 2 {
		t.Fatalf("team audit: %+v %v", teamHistory, err)
	}
	history, err = s.CTOGraphHistory(0)
	if err != nil || len(history) != 2 {
		t.Fatal(history, err)
	}
	if err = s.GraphUndo(history[1].ID, "owner"); err != nil {
		t.Fatal(err)
	}
	teams, err := s.Teams()
	if err != nil || len(teams) != 1 || !teams[0].Active {
		t.Fatalf("redo: %+v %v", teams, err)
	}
}
