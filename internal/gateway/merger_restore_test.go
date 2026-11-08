package gateway

import (
	"github.com/niparis/agent-net/internal/protocol"
	"reflect"
	"testing"
)

func TestMergerRestorePreservesRoutingForUndo(t *testing.T) {
	r := newDeliveryRig(t)
	original := protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: r.sonnet}
	if err := r.s.SetMerger(original, "cto"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "merge", Node: "@merger"}, "owner"); err != nil {
		t.Fatal(err)
	}
	before, err := r.s.visibleGraph(true)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.s.RestoreMergerRegistrations("demo", nil); err != nil {
		t.Fatal(err)
	}
	after, err := r.s.visibleGraph(true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Edges, after.Edges) {
		t.Fatalf("restore changed routes: %+v -> %+v", before.Edges, after.Edges)
	}
	if err := r.s.RestoreMergerRegistrations("demo", []protocol.MergerSetRequest{original}); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := r.s.db.QueryRow(`SELECT session_id FROM project_mergers WHERE project='demo' AND node_id=?`, r.a).Scan(&id); err != nil || id != r.sonnet {
		t.Fatalf("restore: %q %v", id, err)
	}
	if err := r.s.RestoreMergerRegistrations("demo", []protocol.MergerSetRequest{{Project: "other", NodeID: r.a, SessionID: r.sonnet}}); err == nil {
		t.Fatal("cross-project restore accepted")
	}
}
