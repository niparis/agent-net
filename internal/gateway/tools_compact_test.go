package gateway

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/niparis/agent-net/internal/protocol"
)

// wikiDescription is the kind of description a real wiki skill carries: Chinese
// text, so each character takes three bytes.
const wikiDescription = "阿里云百炼平台技术文档知识库（LLM Wiki）。当用户查询百炼模型列表、API 参数、错误码、应用开发等百炼相关问题时激活。"

// manyToolsItems holds the huge wiki skill on eight locations of four nodes,
// plus n-1 small skills with long English descriptions. It returns the items
// the way tools_items builds them.
func manyToolsItems(t *testing.T, n int) []protocol.ToolItem {
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
	perNode := map[string][]protocol.InventoryItem{}
	for _, node := range []string{"one", "two", "three", "four"} {
		for _, h := range []string{"claude", "codex"} {
			perNode[node] = append(perNode[node], protocol.InventoryItem{Kind: "skill", Name: "bailian-docs-llm-wiki", Hash: fmt.Sprintf("%064x", 1), Harness: h, Scope: "user", Location: "/home/n/." + h + "/skills/bailian-docs-llm-wiki", State: "ok", Description: wikiDescription, Files: files})
		}
	}
	for i := 1; i < n; i++ {
		name := fmt.Sprintf("design-taste-frontend-%02d", i)
		desc := fmt.Sprintf("Skill number %d. It is used when a task needs step by step help with the topic in this name, and it lists the files that hold the detail. Read it first.", i)
		perNode["one"] = append(perNode["one"], protocol.InventoryItem{Kind: "skill", Name: name, Hash: fmt.Sprintf("%064x", i+1), Harness: "claude", Scope: "user", Location: "/home/n/.claude/skills/" + name, State: "ok", Description: desc, Files: []string{"SKILL.md"}})
	}
	for node, items := range perNode {
		if err = s.SetInventory(node, protocol.InventoryReport{State: "ok", Items: items}); err != nil {
			t.Fatal(err)
		}
	}
	items, err := s.ToolsItems(protocol.ToolsFilter{}, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func TestToolRowsPageOf50ItemsFitsUnder16KBAndShowsFileCount(t *testing.T) {
	items := manyToolsItems(t, 50)
	page, err := toolRowsPage(items, protocol.ToolsFilter{Compact: true, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 50 {
		t.Fatalf("rows: %d", len(page.Rows))
	}
	huge := page.Rows[0]
	if huge.Name != "bailian-docs-llm-wiki" || huge.FileCount != 2665 || huge.Machines != 4 {
		t.Fatalf("huge row: %+v", huge)
	}
	// Every description is Chinese, the widest text a row can carry.
	chinese := make([]protocol.ToolItem, len(items))
	copy(chinese, items)
	for i := range chinese {
		chinese[i].Description = strings.Repeat("阿", 200)
	}
	widest, err := toolRowsPage(chinese, protocol.ToolsFilter{Compact: true, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]protocol.ToolRowsPage{"mixed": page, "chinese": widest} {
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s compact page of 50 rows is %d bytes", name, len(b))
		if len(b) >= 16<<10 {
			t.Fatalf("%s compact page of 50 rows is %d bytes, want under 16 KB", name, len(b))
		}
	}
}

func TestToolRowShowsOneLineDescriptionAndLargestFileCount(t *testing.T) {
	item := protocol.ToolItem{
		ID:          "skill-id",
		Kind:        "skill",
		Name:        "docs",
		Description: "Writes  docs.\n\nUse it for   wiki pages.",
		Locations:   []protocol.ToolLocation{{FileCount: 3}, {FileCount: 5}},
		Machines:    []string{"one", "two"},
	}
	page, err := toolRowsPage([]protocol.ToolItem{item}, protocol.ToolsFilter{Compact: true})
	if err != nil {
		t.Fatal(err)
	}
	row := page.Rows[0]
	if row.Description != "Writes docs. Use it for wiki pages." {
		t.Fatalf("description: %q", row.Description)
	}
	if row.FileCount != 5 || row.Machines != 2 || row.Trimmed || row.ID != "skill-id" {
		t.Fatalf("row: %+v", row)
	}
}

func TestToolRowCutsLongDescriptionToEightyBytesAtACharacter(t *testing.T) {
	item := manyToolsItems(t, 2)[0]
	for _, desc := range []string{strings.Repeat("a", 300), strings.Repeat("阿", 60)} {
		item.Description = desc
		page, err := toolRowsPage([]protocol.ToolItem{item}, protocol.ToolsFilter{Compact: true})
		if err != nil {
			t.Fatal(err)
		}
		row := page.Rows[0]
		if !utf8.ValidString(row.Description) || len(row.Description) > 80 || !row.Trimmed {
			t.Fatalf("description %q (%d bytes), trimmed %v", row.Description, len(row.Description), row.Trimmed)
		}
	}
}

func TestToolRowsPageStopsAtByteBudgetAndCursorContinues(t *testing.T) {
	items := manyToolsItems(t, 50)
	for i := range items {
		items[i].Name = strings.Repeat("n", 150)
	}
	page, err := toolRowsPage(items, protocol.ToolsFilter{Compact: true, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) >= 16<<10 {
		t.Fatalf("page is %d bytes, want under 16 KB", len(b))
	}
	if len(page.Rows) == 0 || len(page.Rows) >= 50 || page.NextCursor != strconv.Itoa(len(page.Rows)) {
		t.Fatalf("rows %d, next %q: a long-name page should stop early and say where to resume", len(page.Rows), page.NextCursor)
	}
}

func TestToolRowsCursorWalksEveryRowOnce(t *testing.T) {
	items := manyToolsItems(t, 50)
	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		page, err := toolRowsPage(items, protocol.ToolsFilter{Compact: true, Limit: 16, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 50 {
			t.Fatalf("total: %d", page.Total)
		}
		pages++
		for _, row := range page.Rows {
			if seen[row.ID] {
				t.Fatalf("row %s came twice", row.Name)
			}
			seen[row.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 50 || pages != 4 {
		t.Fatalf("saw %d rows in %d pages", len(seen), pages)
	}
}

func TestToolRowsLimitDefaultsToTwentyFiveAndStopsAtOneHundred(t *testing.T) {
	items := manyToolsItems(t, 150)
	def, err := toolRowsPage(items, protocol.ToolsFilter{Compact: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(def.Rows) != 25 || def.NextCursor != "25" {
		t.Fatalf("default page: %d rows, next %q", len(def.Rows), def.NextCursor)
	}
	// Full ids make each row too big for 100 rows in one page, so the byte
	// budget ends this page first. The page still never passes 100 rows.
	big, err := toolRowsPage(items, protocol.ToolsFilter{Compact: true, Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if len(big.Rows) == 0 || len(big.Rows) > 100 || big.NextCursor != strconv.Itoa(len(big.Rows)) {
		t.Fatalf("limit 500 gave %d rows, next %q", len(big.Rows), big.NextCursor)
	}
}

func TestToolRowsRefusesMalformedCursorAndOffset(t *testing.T) {
	items := manyToolsItems(t, 3)
	if _, err := toolRowsPage(items, protocol.ToolsFilter{Compact: true, Cursor: "later"}); err == nil {
		t.Fatal("a cursor that is not a number was accepted")
	}
	if _, err := toolRowsPage(items, protocol.ToolsFilter{Compact: true, Offset: 2}); err == nil {
		t.Fatal("offset with compact was accepted; compact pages use cursor")
	}
}

func TestToolsNameFilterMatchesAnyCaseSubstring(t *testing.T) {
	items := manyToolsItems(t, 5)
	got := toolsNamed(items, "DOCS-llm")
	if len(got) != 1 || got[0].Name != "bailian-docs-llm-wiki" {
		t.Fatalf("filtered names: %d items", len(got))
	}
	page, err := toolRowsPage(toolsNamed(items, "FRONTEND-0"), protocol.ToolsFilter{Compact: true})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 4 || len(page.Rows) != 4 {
		t.Fatalf("frontend-0 total %d rows %d, want 4", page.Total, len(page.Rows))
	}
}

// The gateway refuses unknown arguments, so the compact fields must decode.
func TestToolsItemsQueryAcceptsCompactArguments(t *testing.T) {
	f, err := decodeOperator[protocol.ToolsFilter]([]byte(`{"compact":true,"limit":50,"cursor":"50","name":"wiki"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !f.Compact || f.Limit != 50 || f.Cursor != "50" || f.Name != "wiki" {
		t.Fatalf("decoded %+v", f)
	}
}
