package gateway

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// hugeSkillStore holds one skill with thousands of files on four locations of
// two nodes (as a real wiki skill does), plus one small skill.
func hugeSkillStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(seededStorePath(t, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	files := make([]string, 2665)
	for i := range files {
		files[i] = fmt.Sprintf("raw/docs/section-%04d/%s.md", i, strings.Repeat("x", 60))
	}
	for _, node := range []string{"one", "two"} {
		var items []protocol.InventoryItem
		for _, h := range []string{"claude", "codex"} {
			items = append(items, protocol.InventoryItem{Kind: "skill", Name: "huge-wiki", Hash: "h1", Harness: h, Scope: "user", Location: "/home/n/." + h + "/skills/huge-wiki", State: "ok", Files: files})
		}
		if node == "one" {
			items = append(items, protocol.InventoryItem{Kind: "skill", Name: "small", Hash: "s1", Harness: "claude", Scope: "user", Location: "/home/n/.claude/skills/small", State: "ok", Files: []string{"SKILL.md"}})
		}
		if err = s.SetInventory(node, protocol.InventoryReport{State: "ok", Items: items}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestToolsItemsListSurvivesHugeSkill(t *testing.T) {
	s := hugeSkillStore(t)
	items, err := s.ToolsItems(protocol.ToolsFilter{}, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for offset := 0; ; {
		page, err := toolsPage(items, offset, 0)
		if err != nil {
			t.Fatalf("list failed: %v", err)
		}
		for _, it := range page.Items {
			seen = append(seen, it.Name)
			for _, l := range it.Locations {
				if len(l.Files) != 0 {
					t.Fatalf("%s: list item carries %d files", it.Name, len(l.Files))
				}
				if it.Name == "huge-wiki" && l.FileCount != 2665 {
					t.Fatalf("file count: %d", l.FileCount)
				}
			}
		}
		if page.Next == 0 {
			break
		}
		offset = page.Next
	}
	if strings.Join(seen, ",") != "huge-wiki,small" {
		t.Fatalf("items: %v", seen)
	}
}

func TestToolsItemListTrimsOneOversizedItemWithNote(t *testing.T) {
	s := hugeSkillStore(t)
	items, err := s.ToolsItems(protocol.ToolsFilter{}, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	// A description this large is not a file list, so only the trim saves it.
	items[0].Description = strings.Repeat("d", 300<<10)
	page, err := toolsPage(items, 0, 0)
	if err != nil {
		t.Fatalf("one huge item failed the list: %v", err)
	}
	if len(page.Items) != 2 || page.Items[0].Note == "" {
		t.Fatalf("page: %d items, note %q", len(page.Items), page.Items[0].Note)
	}
	if b, _ := json.Marshal(page); len(b) > 240<<10 {
		t.Fatalf("page is %d bytes", len(b))
	}
}

func TestToolsItemDetailCapsFilesWithNote(t *testing.T) {
	s := hugeSkillStore(t)
	items, err := s.ToolsItems(protocol.ToolsFilter{}, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	detail, err := s.ToolsItem(items[0].ID, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Item.Locations) != 4 {
		t.Fatalf("locations: %d", len(detail.Item.Locations))
	}
	for _, l := range detail.Item.Locations {
		if len(l.Files) == 0 || len(l.Files) >= 2665 || len(l.Files)+l.FilesMore != 2665 || l.FileCount != 2665 {
			t.Fatalf("files %d more %d count %d", len(l.Files), l.FilesMore, l.FileCount)
		}
	}
	if !strings.Contains(detail.Item.Note, "more files") {
		t.Fatalf("note: %q", detail.Item.Note)
	}
	b, err := json.Marshal(detail)
	if err != nil || len(b) > 1<<20 {
		t.Fatalf("detail is %d bytes, %v", len(b), err)
	}
}
