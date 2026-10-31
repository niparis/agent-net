package gateway

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/inventory"
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

func TestInventoryIdentitiesAndReplacement(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	item := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "hash-a", Harness: "codex", Scope: "user", Location: "/a", State: "ok"}
	if err = s.SetInventory("one", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{item}}); err != nil {
		t.Fatal(err)
	}
	item.Harness = "claude"
	item.Location = "/b"
	if err = s.SetInventory("two", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{item}}); err != nil {
		t.Fatal(err)
	}
	var identities, locations int
	if err = s.db.QueryRow(`SELECT count(*) FROM tool_identities`).Scan(&identities); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT count(*) FROM tool_locations`).Scan(&locations); err != nil {
		t.Fatal(err)
	}
	if identities != 1 || locations != 2 {
		t.Fatalf("identities=%d locations=%d", identities, locations)
	}
	item.Hash = "hash-b"
	if err = s.SetInventory("one", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{item}}); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT count(*) FROM tool_identities`).Scan(&identities); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT count(*) FROM tool_locations`).Scan(&locations); err != nil {
		t.Fatal(err)
	}
	if identities != 2 || locations != 2 {
		t.Fatalf("replacement identities=%d locations=%d", identities, locations)
	}
}

func TestInventoryOfflineIsStale(t *testing.T) {
	reports := []protocol.InventoryReport{{Machine: "node", State: protocol.InventoryOK, Items: []protocol.InventoryItem{{State: protocol.InventoryOK}}}}
	markInventoryOffline(&reports[0])
	if reports[0].State != protocol.InventoryStale || reports[0].Items[0].State != protocol.InventoryStale {
		t.Fatalf("not stale: %+v", reports)
	}
}

func TestToolsItemsVariantsAndProjects(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.db.Exec(`INSERT INTO projects(node_id,path,name,added_at,checked_at) VALUES('one','/repo','demo',0,0)`); err != nil {
		t.Fatal(err)
	}
	a := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "a", Harness: "codex", Scope: "project", Project: "demo", ProjectRoot: "/repo", Location: "/repo/.agents/skills/demo", State: "ok", Files: []string{"SKILL.md", "one.txt"}}
	b := a
	b.Hash = "b"
	b.Files = []string{"SKILL.md", "two.txt"}
	if err = s.SetInventory("one", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{a, b}}); err != nil {
		t.Fatal(err)
	}
	items, err := s.ToolsItems(protocol.ToolsFilter{Project: "demo"}, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Variants != 2 || items[0].Locations[0].GraphNode != "demo" {
		t.Fatalf("items: %+v", items)
	}
	detail, err := s.ToolsItem(items[0].ID, func(string) bool { return true })
	if err != nil || len(detail.VariantDiffs) != 1 {
		t.Fatalf("detail: %+v %v", detail, err)
	}
}

func TestInventoryNativeSecretsStayOutOfStoreAndJSON(t *testing.T) {
	home := t.TempDir()
	secret := "fixture-secret-12345"
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers":{"demo":{"command":"node","args":["--token","`+secret+`"],"env":{"API_KEY":"`+secret+`"},"url":"https://example.invalid/path?token=`+secret+`"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	report := inventory.Scan(inventory.Options{Home: home})
	s, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.SetInventory("one", report); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err = s.db.QueryRow(`SELECT group_concat(observation) FROM tool_locations`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	items, err := s.ToolsItems(protocol.ToolsFilter{}, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	if err = s.db.QueryRow(`SELECT report FROM inventory_reports WHERE node_id='one'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, secret) || strings.Contains(stored, secret) || strings.Contains(string(b), secret) {
		t.Fatal("secret in stored inventory or JSON")
	}
}

func TestInventoryMigrationBackfillsReports(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.db.Exec(`DROP TABLE skill_versions; DROP TABLE tool_locations; DROP TABLE tool_identities; DELETE FROM schema_version WHERE version >= (SELECT MAX(version)-1 FROM schema_version); INSERT INTO inventory_reports VALUES('node','{"state":"ok","items":[{"kind":"skill","name":"demo","hash":"abc"}]}')`); err != nil {
		t.Fatal(err)
	}
	if err = s.migrate(); err != nil {
		t.Fatal(err)
	}
	items, err := s.ToolsItems(protocol.ToolsFilter{}, func(string) bool { return false })
	if err != nil || len(items) != 1 || items[0].State != protocol.InventoryStale {
		t.Fatalf("backfill: %+v %v", items, err)
	}
}

func TestToolsFilterReturnsAllLocationsOfMatchingIdentity(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, h := range []string{"codex", "claude"} {
		item := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "same", Scope: "user", Harness: h, State: "ok"}
		if err = s.SetInventory(h, protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{item}}); err != nil {
			t.Fatal(err)
		}
	}
	items, err := s.ToolsItems(protocol.ToolsFilter{Harness: "codex"}, func(string) bool { return true })
	if err != nil || len(items) != 1 || len(items[0].Locations) != 2 {
		t.Fatalf("filtered locations: %+v %v", items, err)
	}
}

func TestIdenticalScannedSkillsShareIdentityAcrossMachines(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	homes := []string{t.TempDir(), t.TempDir()}
	for i, home := range homes {
		dir := filepath.Join(home, ".claude/skills/demo")
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("same skill"), 0600); err != nil {
			t.Fatal(err)
		}
		report := inventory.Scan(inventory.Options{Home: home})
		if report.State != protocol.InventoryOK {
			t.Fatalf("scan: %+v", report)
		}
		if err = s.SetInventory(fmt.Sprintf("node-%d", i), report); err != nil {
			t.Fatal(err)
		}
	}
	items, err := s.ToolsItems(protocol.ToolsFilter{Kind: protocol.InventorySkill, Harness: "claude"}, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Machines) != 2 {
		t.Fatalf("identical skills: %+v", items)
	}
}
