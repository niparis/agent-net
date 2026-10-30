package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

type renameOp struct {
	fakeOp
	nodes []protocol.NodeView
	id    string
	name  string
	calls int
}

func (r *renameOp) Snapshot(context.Context) (operator.Snapshot, error) {
	return operator.Snapshot{Nodes: r.nodes}, nil
}

func (r *renameOp) RenameNode(_ context.Context, id, name string) (protocol.NodeRenameResult, error) {
	r.id, r.name, r.calls = id, name, r.calls+1
	return protocol.NodeRenameResult{NodeID: id, Hostname: name, DisplayName: name}, nil
}

func TestNodesRenameByNameOrID(t *testing.T) {
	op := &renameOp{nodes: []protocol.NodeView{
		{NodeID: "n_1", Hostname: "Johns-MacBook-Pro-950", Reported: "Johns-MacBook-Pro-950"},
		{NodeID: "n_2", Hostname: "box", Reported: "box"},
	}}
	var out bytes.Buffer
	if err := runNodesRename(context.Background(), op, &out, []string{"johns-macbook-pro-950", "laptop-1"}); err != nil {
		t.Fatal(err)
	}
	if op.id != "n_1" || op.name != "laptop-1" || !strings.Contains(out.String(), "laptop-1") {
		t.Errorf("by name: %q %q %q", op.id, op.name, out.String())
	}
	if err := runNodesRename(context.Background(), op, &out, []string{"n_2", "desk"}); err != nil || op.id != "n_2" {
		t.Errorf("by id: %q %v", op.id, err)
	}
	// No new name clears it.
	if err := runNodesRename(context.Background(), op, &out, []string{"n_2"}); err != nil || op.name != "" {
		t.Errorf("clear: %q %v", op.name, err)
	}
}

func TestNodesRenameRefusesBadInput(t *testing.T) {
	op := &renameOp{nodes: []protocol.NodeView{{NodeID: "n_1", Hostname: "box"}, {NodeID: "n_2", Hostname: "BOX"}}}
	ctx := context.Background()
	var out bytes.Buffer
	for _, args := range [][]string{nil, {"n_1", "a", "b"}, {"n_1", "bad name"}, {"n_1", "-x"}, {"nope", "x"}, {"box", "x"}} {
		if err := runNodesRename(ctx, op, &out, args); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
	if op.calls != 0 {
		t.Errorf("a refused command called the daemon %d times", op.calls)
	}
}
