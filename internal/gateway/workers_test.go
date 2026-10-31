package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func owner(host string) protocol.SessionInfo {
	return protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID, Name: protocol.OwnerName(host), Status: "idle"}
}

// The owner pseudo-session is stored like a session, but it is a person: it is
// not in the plain agent list, it makes no session.new event, it has the one
// native ID, and it is no project.
func TestOwnerSessionIsAPersonNotAnAgent(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	id, err := s.UpsertSession(node, owner("mac"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p", Name: "pal"}); err != nil {
		t.Fatal(err)
	}
	plain, _ := s.Agents(protocol.QueryAgents{})
	if len(plain) != 1 || plain[0].Harness != "pi" {
		t.Errorf("plain list: %+v", plain)
	}
	with, _ := s.Agents(protocol.QueryAgents{IncludeOwners: true})
	if len(with) != 2 {
		t.Errorf("list with owners: %+v", with)
	}
	if byHarness, _ := s.Agents(protocol.QueryAgents{Harness: protocol.OwnerHarness}); len(byHarness) != 1 || byHarness[0].SessionID != id {
		t.Errorf("by harness: %+v", byHarness)
	}
	if got, ok, _ := s.Agent(id); !ok || got.Profile.Name != "you (on mac)" {
		t.Errorf("agent by id: %+v", got)
	}
	evs, _ := s.Events("session.new")
	if len(evs) != 1 || !strings.Contains(evs[0], "harness=pi") {
		t.Errorf("session.new events: %v", evs)
	}
	if ps, _ := s.Projects(""); len(ps) != 0 {
		t.Errorf("the owner became a project: %+v", ps)
	}
	// Any other native ID for an owner session is refused, and an owner is never a worker.
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: "mine"}); err == nil {
		t.Error("an owner session with another native ID was accepted")
	}
	in := owner("mac")
	in.Worker = &protocol.WorkerInfo{ID: "w", Owner: "s"}
	if _, err := s.UpsertSession(node, in); err != nil {
		t.Fatal(err)
	}
	if a, _, _ := s.Agent(id); a.Worker != nil {
		t.Errorf("the owner became a worker: %+v", a.Worker)
	}
}

// A worker is stored with its owner, comes back in the agent view, and its
// messages say they are from a worker.
func TestWorkerSessionAndMessages(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	oid, _ := s.UpsertSession(node, owner("mac"))
	in := protocol.SessionInfo{Harness: "opencode", NativeID: "ses_1", Name: "shop", Mode: protocol.ModeBackground,
		Worker: &protocol.WorkerInfo{ID: "w_1", Owner: oid, Project: "shop", StartedAt: 42}}
	wid, err := s.UpsertSession(node, in)
	if err != nil {
		t.Fatal(err)
	}
	a, _, _ := s.Agent(wid)
	if a.Worker == nil || *a.Worker != *in.Worker {
		t.Fatalf("worker view: %+v", a.Worker)
	}
	m, _, err := s.StoreMessage(protocol.MsgSend{Sender: wid, Recipient: oid, Body: "report", Type: protocol.TypeNotification})
	if err != nil {
		t.Fatal(err)
	}
	if !m.FromWorker || m.FromHarness != "opencode" {
		t.Errorf("worker message: %+v", m)
	}
	req, _, _ := s.StoreMessage(protocol.MsgSend{Sender: oid, Recipient: wid, Body: "do it"})
	if req.FromWorker || req.FromHarness != protocol.OwnerHarness || req.FromName != "you (on mac)" {
		t.Errorf("owner message: %+v", req)
	}
	// A reply to the owner's message works as for any session.
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: wid, Recipient: oid, Body: "done", ParentID: req.ID}); err != nil {
		t.Errorf("reply to the owner: %v", err)
	}
	// Clearing the flag on a later update: the daemon always sends it, so it is what it says.
	in.Worker = nil
	s.UpsertSession(node, in)
	if a, _, _ := s.Agent(wid); a.Worker != nil {
		t.Errorf("worker flag stayed: %+v", a.Worker)
	}
}

func TestNodeLaunchIsThreeValued(t *testing.T) {
	s, _ := newTestStore(t)
	id, _, _ := s.CreateNode("box", "linux")
	s.SetNodeHello(id, "box", "v0.4.0")
	if v, _ := s.NodeLaunch(id); v != nil {
		t.Fatalf("never reported: %v", *v)
	}
	if nodes, _ := s.Nodes(); nodes[0].Launch != nil {
		t.Fatal("nodes view must say not reported")
	}
	yes, no := true, false
	s.SetNodeLaunch(id, nil) // nil changes nothing
	if v, _ := s.NodeLaunch(id); v != nil {
		t.Fatal("nil changed the value")
	}
	s.SetNodeLaunch(id, &no)
	if v, _ := s.NodeLaunch(id); v == nil || *v {
		t.Fatalf("no: %v", v)
	}
	s.SetNodeLaunch(id, &yes)
	if nodes, _ := s.Nodes(); nodes[0].Launch == nil || !*nodes[0].Launch {
		t.Fatalf("yes: %+v", nodes[0].Launch)
	}
}

func TestFeedIncludesWorkerEvents(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	s.Event("worker.started", node, "s_w", "", "project=shop harness=opencode owner=s_o")
	s.Event("worker.stopped", node, "s_w", "", "reason=idle")
	s.Event("worker.refused", node, "s_o", "", "project=shop reason=limit")
	s.Event("mcp.added", node, "", "", "secret")
	evs, err := s.FeedEvents(0, 50)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	if strings.Join(kinds, ",") != "node.enrolled,worker.started,worker.stopped,worker.refused" {
		t.Errorf("feed kinds: %v", kinds)
	}
}

func TestResumeWorkerRebindsOnlyItsOfflineSession(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("one", "linux")
	other, _, _ := s.CreateNode("two", "linux")
	old := protocol.SessionInfo{Harness: "claude", NativeID: "old", Worker: &protocol.WorkerInfo{ID: "w_one", Owner: "s_owner"}}
	id, err := s.UpsertSession(node, old)
	if err != nil {
		t.Fatal(err)
	}
	incoming := old
	incoming.NativeID, incoming.SessionID = "new", id
	got, err := s.UpsertSession(node, incoming)
	if err != nil || got == id {
		t.Fatalf("online worker rebound: %s %v", got, err)
	}
	if err := s.SetSessionOffline(node, id); err != nil {
		t.Fatal(err)
	}
	incoming.NativeID = "other-node"
	got, err = s.UpsertSession(other, incoming)
	if err != nil || got == id {
		t.Fatalf("other node rebound: %s %v", got, err)
	}
	incoming.NativeID = "wrong-worker"
	incoming.Worker = &protocol.WorkerInfo{ID: "w_other"}
	got, err = s.UpsertSession(node, incoming)
	if err != nil || got == id {
		t.Fatalf("other worker rebound: %s %v", got, err)
	}
	incoming.NativeID, incoming.Worker = "resumed", old.Worker
	got, err = s.UpsertSession(node, incoming)
	if err != nil || got != id {
		t.Fatalf("worker did not retain ID: %s want %s: %v", got, id, err)
	}
}
