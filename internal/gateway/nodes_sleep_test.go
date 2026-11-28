package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestNodeSleepExplainsGapNoAsk(t *testing.T) {
	s, c := newTestStore(t)
	node, _, err := s.CreateNode("mac", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	drop := c.t
	if err := s.NodeLinkDown(node, drop); err != nil {
		t.Fatal(err)
	}
	// The daemon reports the sleep when it wakes, before the gateway could
	// call the gap unexplained.
	explained, err := s.NodeSlept(node, drop.Add(-time.Minute), drop.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !explained {
		t.Fatal("the sleep report did not explain the gap")
	}
	marked, err := s.MarkSilentNodes(drop.Add(16 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(marked) != 0 {
		t.Fatalf("marked %d machines silent, want none", len(marked))
	}
}

func TestUnexplainedGapMarksNodeSilentOnce(t *testing.T) {
	s, c := newTestStore(t)
	node, _, err := s.CreateNode("build", "linux")
	if err != nil {
		t.Fatal(err)
	}
	drop := c.t
	if err := s.NodeLinkDown(node, drop); err != nil {
		t.Fatal(err)
	}
	if marked, err := s.MarkSilentNodes(drop.Add(14 * time.Minute)); err != nil || len(marked) != 0 {
		t.Fatalf("before 15 minutes: marked %d, err %v", len(marked), err)
	}
	marked, err := s.MarkSilentNodes(drop.Add(16 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(marked) != 1 {
		t.Fatalf("marked %d machines, want one", len(marked))
	}
	// A later check must not mark the same outage again.
	if marked, err := s.MarkSilentNodes(drop.Add(40 * time.Minute)); err != nil || len(marked) != 0 {
		t.Fatalf("second check: marked %d, err %v", len(marked), err)
	}
	// The machine returns.
	if err := s.NodeLinkUp(node); err != nil {
		t.Fatal(err)
	}
}

func TestMachineStateNamesAsleepAndSilent(t *testing.T) {
	s, c := newTestStore(t)
	node, _, err := s.CreateNode("mac", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeHello(node, "mac", "v1"); err != nil {
		t.Fatal(err)
	}
	drop := c.t
	if err := s.NodeLinkDown(node, drop); err != nil {
		t.Fatal(err)
	}
	nodes, err := s.Nodes()
	if err != nil || len(nodes) != 1 {
		t.Fatalf("nodes = %d, err %v", len(nodes), err)
	}
	if nodes[0].DownState != "" {
		t.Fatalf("state %q before 15 minutes, want empty", nodes[0].DownState)
	}
	if _, err := s.MarkSilentNodes(drop.Add(16 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	nodes, _ = s.Nodes()
	if nodes[0].DownState != "silent" || nodes[0].DownSince != drop.Unix() {
		t.Fatalf("state = %q since %d, want silent since %d", nodes[0].DownState, nodes[0].DownSince, drop.Unix())
	}
	// The same row, with a sleep report that covers the drop, reads asleep.
	if _, err := s.db.Exec(`UPDATE nodes SET silence_silent_at=0 WHERE id=?`, node); err != nil {
		t.Fatal(err)
	}
	if _, err := s.NodeSlept(node, drop.Add(-time.Minute), drop.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	nodes, _ = s.Nodes()
	if nodes[0].DownState != "asleep" || nodes[0].DownSince != drop.Add(-time.Minute).Unix() {
		t.Fatalf("state = %q since %d, want asleep since %d", nodes[0].DownState, nodes[0].DownSince, drop.Add(-time.Minute).Unix())
	}
}

func TestMarkSilentNodesNotifiesChiefOnce(t *testing.T) {
	s, c := newTestStore(t)
	chiefNode, _, err := s.CreateNode("chief-box", "linux")
	if err != nil {
		t.Fatal(err)
	}
	chief, err := s.UpsertSession(chiefNode, protocol.SessionInfo{Harness: "claude", NativeID: "chief", Name: "chief"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	node, _, err := s.CreateNode("build", "linux")
	if err != nil {
		t.Fatal(err)
	}
	drop := c.t
	if err := s.NodeLinkDown(node, drop); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkSilentNodes(drop.Add(16 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	got := messagesTo(t, s, chief)
	if len(got) != 1 || got[0].typ != protocol.TypeNotification || !strings.Contains(got[0].body, "build") {
		t.Fatalf("want one silent-machine notification to chief, got %+v", got)
	}
	// Repeating MarkSilentNodes does not send a second notification.
	if _, err := s.MarkSilentNodes(drop.Add(30 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got2 := messagesTo(t, s, chief); len(got2) != 1 {
		t.Fatalf("repeat tick duplicated notice: %+v", got2)
	}
}
