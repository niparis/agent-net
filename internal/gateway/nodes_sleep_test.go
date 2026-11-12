package gateway

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// machineAsks counts the open CTO alerts about a machine (decision 0152).
func machineAsks(t *testing.T, s *Store, node string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE kind=? AND about_kind=? AND about_id=? AND status='open'`,
		protocol.AskKindCTOAlert, protocol.AskAboutMachine, node).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

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
	if n := machineAsks(t, s, node); n != 0 {
		t.Fatalf("%d Desk items after an explained sleep, want none", n)
	}
}

func TestUnexplainedGapRaisesOneAsk(t *testing.T) {
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
	if n := machineAsks(t, s, node); n != 1 {
		t.Fatalf("%d open Desk items, want exactly one", n)
	}
	// A later check must not raise a second item for the same outage.
	if marked, err := s.MarkSilentNodes(drop.Add(40 * time.Minute)); err != nil || len(marked) != 0 {
		t.Fatalf("second check: marked %d, err %v", len(marked), err)
	}
	if n := machineAsks(t, s, node); n != 1 {
		t.Fatalf("%d open Desk items after a second check, want one", n)
	}
	// The machine returns: the item closes.
	if err := s.NodeLinkUp(node); err != nil {
		t.Fatal(err)
	}
	if n := machineAsks(t, s, node); n != 0 {
		t.Fatalf("%d open Desk items after return, want none", n)
	}
	var status, reason string
	if err := s.db.QueryRow(`SELECT status,answer FROM inbox_items WHERE kind=?`, protocol.AskKindCTOAlert).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != protocol.AskClosed {
		t.Fatalf("item status %q, want closed", status)
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
	if _, err := s.db.Exec(`UPDATE node_silence SET silent_at=0 WHERE node_id=?`, node); err != nil {
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
