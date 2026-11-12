package gateway

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/niparis/agent-net/internal/protocol"
)

// The compact list is for the CTO. It answers "what is there, and how big is
// it" in one row per item, so the CTO can read one item in full by its ID.
const (
	toolRowsDefault = 25
	toolRowsMax     = 100
	// toolRowsBytes is the most bytes of rows one page holds. The CTO reads
	// the answer in one go, so a page stops early, with next_cursor, rather
	// than pass 16 KB.
	toolRowsBytes = 16 << 10
	// toolRowDescription is the most bytes of a description one row shows.
	// At 100 bytes, 50 rows with 26-character names pass 16 KB; 80 leaves room.
	toolRowDescription = 80
)

// toolsNamed keeps the items whose name contains text, ignoring case. An empty
// text keeps every item.
func toolsNamed(items []protocol.ToolItem, text string) []protocol.ToolItem {
	if text == "" {
		return items
	}
	want := strings.ToLower(text)
	out := []protocol.ToolItem{}
	for _, it := range items {
		if strings.Contains(strings.ToLower(it.Name), want) {
			out = append(out, it)
		}
	}
	return out
}

// toolRowsPage pages the compact rows of items. Cursor is the position of the
// first row, as the previous page's NextCursor gave it. A page holds at most
// Limit rows and at most toolRowsBytes of them.
func toolRowsPage(items []protocol.ToolItem, f protocol.ToolsFilter) (protocol.ToolRowsPage, error) {
	page := protocol.ToolRowsPage{Rows: []protocol.ToolRow{}, Total: len(items)}
	if f.Offset != 0 {
		return page, errors.New("compact tools_items pages with cursor, not offset")
	}
	start := 0
	if f.Cursor != "" {
		n, err := strconv.Atoi(f.Cursor)
		if err != nil || n < 0 {
			return page, errors.New("unknown or malformed cursor. Start again without cursor and use next_cursor from that result")
		}
		start = n
	}
	limit := f.Limit
	if limit <= 0 {
		limit = toolRowsDefault
	}
	if limit > toolRowsMax {
		limit = toolRowsMax
	}
	if start > len(items) {
		start = len(items)
	}
	size := 0
	end := start
	for ; end < len(items) && end-start < limit; end++ {
		row := toolRow(items[end])
		b, err := json.Marshal(row)
		if err != nil {
			return page, err
		}
		// The first row always fits, so every page makes progress.
		if end > start && size+len(b)+1 > toolRowsBytes {
			break
		}
		size += len(b) + 1
		page.Rows = append(page.Rows, row)
	}
	if end < len(items) {
		page.NextCursor = strconv.Itoa(end)
	}
	return page, nil
}

// toolRow is the compact form of one item. FileCount is the largest count
// among its copies, so a copy with the full tree shows the whole size.
func toolRow(it protocol.ToolItem) protocol.ToolRow {
	description, trimmed := toolRowText(it.Description)
	row := protocol.ToolRow{
		ID:          it.ID,
		Name:        it.Name,
		Kind:        it.Kind,
		Machines:    len(it.Machines),
		Trimmed:     trimmed,
		Description: description,
	}
	for _, l := range it.Locations {
		row.FileCount = max(row.FileCount, l.FileCount)
	}
	if it.State != protocol.InventoryOK {
		row.State = it.State
	}
	return row
}

// toolRowText joins the description onto one line and cuts it to the row's
// length, at a character boundary. It reports whether anything was cut.
func toolRowText(s string) (string, bool) {
	line := strings.Join(strings.Fields(s), " ")
	if len(line) <= toolRowDescription {
		return line, false
	}
	cut := toolRowDescription
	for !utf8.RuneStart(line[cut]) {
		cut--
	}
	return line[:cut], true
}
