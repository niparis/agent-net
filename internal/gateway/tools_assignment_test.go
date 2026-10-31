package gateway

import (
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestToolAssignmentRevisions(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	item := protocol.InventoryItem{Kind: "skill", Name: "sample", Hash: "abc", State: "ok"}
	if err = s.SetInventory("machine", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{item}}); err != nil {
		t.Fatal(err)
	}
	id := `["skill","sample","abc"]`
	if _, err = s.db.Exec(`INSERT INTO projects(node_id,path,name,added_at,checked_at) VALUES('machine','/repo','demo',0,0)`); err != nil {
		t.Fatal(err)
	}
	a, err := s.AssignTool(id, protocol.ToolAssignment{Scope: "projects", Projects: []string{"demo"}}, "owner", "test")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.AssignTool(id, protocol.ToolAssignment{Scope: "uninstalled"}, "cto", "test")
	if err != nil {
		t.Fatal(err)
	}
	if a.Revision != 1 || b.Revision != 2 || b.Actor != "cto" || b.Assignment.Scope != "uninstalled" {
		t.Fatalf("revisions: %+v %+v", a, b)
	}
	if _, err = s.AssignTool(id, protocol.ToolAssignment{Scope: "projects", Projects: []string{"missing"}}, "owner", "test"); err == nil {
		t.Fatal("accepted unregistered project")
	}
	if _, err = s.AssignTool(id, protocol.ToolAssignment{Scope: "global"}, "agent", "test"); err == nil {
		t.Fatal("accepted agent actor")
	}
}
