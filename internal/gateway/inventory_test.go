package gateway

import (
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestInventoryStorePreservesFailedScan(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	report := protocol.InventoryReport{Machine: "untrusted", State: protocol.InventoryOK, Items: []protocol.InventoryItem{{Name: "test", State: protocol.InventoryOK}}}
	if err := s.SetInventory("node-a", report); err != nil {
		t.Fatal(err)
	}
	if err := s.SetInventory("node-a", protocol.InventoryReport{State: protocol.InventoryError}); err != nil {
		t.Fatal(err)
	}
	reports, err := s.Inventories()
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || len(reports[0].Items) != 1 || reports[0].Items[0].State != protocol.InventoryStale {
		t.Fatalf("reports: %+v", reports)
	}
}
