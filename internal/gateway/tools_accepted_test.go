package gateway

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestAcceptedDifferencesSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	g, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err = g.store.SetToolAccepted("drift:code-review", true); err != nil {
		t.Fatal(err)
	}
	if err = g.store.SetToolAccepted("dupe:a", true); err != nil {
		t.Fatal(err)
	}
	if err = g.store.SetToolAccepted("dupe:a", false); err != nil {
		t.Fatal(err)
	}
	g.Close()
	g, err = New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	keys, err := g.store.ToolsAccepted()
	if err != nil || !reflect.DeepEqual(keys, []string{"drift:code-review"}) {
		t.Fatalf("keys %v err %v", keys, err)
	}
	raw, err := g.handle(context.Background(), &nodeConn{id: "n"}, "query.tools_accepted", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(raw)
	if string(b) != `["drift:code-review"]` {
		t.Fatalf("query answered %s", b)
	}
}

func TestAcceptDifferenceRequiresOwnerSession(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	node, _, err := g.store.CreateNode("fixture", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.handle(context.Background(), &nodeConn{id: node}, "tools.accept", []byte(`{"key":"drift:x","accepted":true,"owner":"s_none"}`)); err == nil {
		t.Fatal("gateway accepted a difference without an owner session")
	}
	if keys, _ := g.store.ToolsAccepted(); len(keys) != 0 {
		t.Fatalf("stored %v", keys)
	}
}
