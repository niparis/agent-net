package gateway

import (
	"encoding/json"
	"path/filepath"
	"strings"
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

func TestInventoryPagesAreBounded(t *testing.T) {
	reports := []protocol.InventoryReport{{Machine: "one", Items: []protocol.InventoryItem{}}}
	for i := 0; i < 1000; i++ {
		reports[0].Items = append(reports[0].Items, protocol.InventoryItem{Name: strings.Repeat("x", 1000)})
	}
	page, err := inventoryPage(reports, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(page)
	if len(b) > 256<<10 || page.Next == 0 {
		t.Fatalf("page size=%d next=%d", len(b), page.Next)
	}
	page2, err := inventoryPage(reports, page.Next)
	if err != nil || len(page2.Reports) == 0 {
		t.Fatalf("second page: %+v %v", page2, err)
	}
}
