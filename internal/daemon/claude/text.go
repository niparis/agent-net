package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/redact"
)

// This file reads the conversation of a Claude Code session from its
// transcript (~/.claude/projects/<folder>/<session id>.jsonl) for the
// "session_text" method. The file is only appended to, so a byte offset
// names a line for good. An entry's cursor is "<offset of its line>:<index>"
// (a line can hold several blocks: text, then tool calls).
//
// The file is read from the end, in blocks, and only as far as the page
// needs. Lines that are not user or assistant turns (Claude Code writes many
// other kinds, some of them huge) are skipped without being parsed when
// they do not mention a turn at all.
const (
	// textToolResultMax is how much of a tool result is kept.
	textToolResultMax = 600
	// textToolInputMax is how much of a tool call's input summary is kept.
	textToolInputMax = 300
	// textReadMax bounds one read of the file, in bytes.
	textReadMax = 8 << 20
)

// Text returns a page of the conversation of the session. It implements
// daemon.TextProvider.
func (a *Adapter) Text(ctx context.Context, info protocol.SessionInfo, q protocol.TextQuery) (protocol.TextPage, error) {
	path := a.models.transcript(info.NativeID, info.Cwd)
	if path == "" {
		return protocol.TextPage{}, errors.New("no transcript file was found for this Claude Code session")
	}
	return readTranscript(path, q)
}

type cursorPos struct {
	off int64
	idx int
}

func parseCursor(s string) (cursorPos, bool) {
	a, b, ok := strings.Cut(s, ":")
	if !ok {
		return cursorPos{}, false
	}
	off, err1 := strconv.ParseInt(a, 10, 64)
	idx, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil || off < 0 || idx < 0 {
		return cursorPos{}, false
	}
	return cursorPos{off, idx}, true
}

func (c cursorPos) String() string { return fmt.Sprintf("%d:%d", c.off, c.idx) }

func (c cursorPos) less(o cursorPos) bool {
	return c.off < o.off || (c.off == o.off && c.idx < o.idx)
}

// readTranscript builds one page. The limit is applied by the caller's
// sanitiser as well; here it only bounds the work.
func readTranscript(path string, q protocol.TextQuery) (protocol.TextPage, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = protocol.TextDefaultLimit
	}
	limit = min(limit, protocol.TextMaxLimit)
	f, err := os.Open(path)
	if err != nil {
		return protocol.TextPage{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return protocol.TextPage{}, err
	}
	size := fi.Size()
	if q.Before != "" && q.After != "" {
		return protocol.TextPage{}, errors.New("give before or after, not both")
	}
	switch {
	case q.After != "":
		c, ok := parseCursor(q.After)
		if !ok {
			return protocol.TextPage{}, errors.New("bad cursor")
		}
		return readForward(f, size, c, limit, q.Sidechain)
	case q.Before != "":
		c, ok := parseCursor(q.Before)
		if !ok {
			return protocol.TextPage{}, errors.New("bad cursor")
		}
		return readBackward(f, size, &c, limit, q.Sidechain)
	}
	return readBackward(f, size, nil, limit, q.Sidechain)
}

// endOf is a cursor that sorts after every entry of the lines that end at
// offset size, and before every entry of a later line.
func endOf(size int64) string {
	if size <= 0 {
		return ""
	}
	return cursorPos{size - 1, 1 << 20}.String()
}

// collect parses the lines of body (which starts at file offset base) and
// returns their entries that come before the cursor (all of them when before is nil).
func collect(body []byte, base int64, before *cursorPos, sidechain bool) []protocol.TextEntry {
	var out []protocol.TextEntry
	for _, ln := range splitLines(body, base) {
		for i, e := range entriesOfLine(ln.text, sidechain) {
			c := cursorPos{ln.off, i}
			if before != nil && !c.less(*before) {
				continue
			}
			e.Cursor = c.String()
			out = append(out, e)
		}
	}
	return out
}

// readBackward returns the limit entries that come just before the cursor
// (or the newest ones when before is nil).
func readBackward(f *os.File, size int64, before *cursorPos, limit int, sidechain bool) (protocol.TextPage, error) {
	end := size
	if before != nil {
		// Include the whole line the cursor is in: its earlier blocks count.
		if before.off >= size {
			return protocol.TextPage{}, errors.New("cursor is past the end of the transcript")
		}
		end = lineEnd(f, before.off, size)
	}
	var tail []byte // the file from start to end
	start := end
	block := int64(64 << 10)
	var got []protocol.TextEntry
	var firstLine int64 // offset of the first complete line that was parsed
	for {
		newStart := max(start-block, 0)
		buf := make([]byte, start-newStart)
		if _, err := f.ReadAt(buf, newStart); err != nil && err != io.EOF {
			return protocol.TextPage{}, err
		}
		tail = append(buf, tail...)
		start = newStart
		body, base := tail, start
		if start > 0 {
			// The first line may be cut off: leave it for the next round.
			nl := bytes.IndexByte(tail, '\n')
			if nl < 0 {
				if int64(len(tail)) >= textReadMax {
					break
				}
				block = min(block*2, textReadMax)
				continue
			}
			body, base = tail[nl+1:], start+int64(nl)+1
		}
		firstLine = base
		got = collect(body, base, before, sidechain)
		if len(got) > limit || start == 0 || int64(len(tail)) >= textReadMax {
			break
		}
		block = min(block*2, textReadMax)
	}
	more := start > 0
	if len(got) > limit {
		got, more = got[len(got)-limit:], true
	}
	page := protocol.TextPage{Entries: got, Newer: endOf(size)}
	if before != nil {
		page.Newer = "" // paging back does not move the follow position
	}
	if len(got) > 0 {
		if before == nil {
			page.Newer = endOf(size)
		}
		if more {
			page.Older = got[0].Cursor
		}
	} else if more {
		page.Older = cursorPos{firstLine, 0}.String()
	}
	return page, nil
}

// lineEnd returns the offset just after the line that starts at off.
func lineEnd(f *os.File, off, size int64) int64 {
	buf := make([]byte, 32<<10)
	for pos := off; pos < size; {
		n, _ := f.ReadAt(buf, pos)
		if n == 0 {
			break
		}
		if i := bytes.IndexByte(buf[:n], '\n'); i >= 0 {
			return pos + int64(i) + 1
		}
		pos += int64(n)
		if pos-off > textReadMax {
			break
		}
	}
	return size
}

// readForward returns up to limit entries after the cursor, oldest first.
func readForward(f *os.File, size int64, after cursorPos, limit int, sidechain bool) (protocol.TextPage, error) {
	page := protocol.TextPage{Newer: after.String()}
	if after.off >= size {
		return page, nil
	}
	start := after.off
	if size-start > textReadMax {
		// The caller is far behind. Give the newest page instead of reading megabytes.
		return readBackward(f, size, nil, limit, sidechain)
	}
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return page, err
	}
	// Only complete lines: a line still being written is read on the next call.
	i := bytes.LastIndexByte(buf, '\n')
	if i < 0 {
		return page, nil
	}
	buf = buf[:i+1]
	seen := start + int64(len(buf))
	for _, ln := range splitLines(buf, start) {
		for i, e := range entriesOfLine(ln.text, sidechain) {
			c := cursorPos{ln.off, i}
			if !after.less(c) {
				continue
			}
			e.Cursor = c.String()
			page.Entries = append(page.Entries, e)
			page.Newer = e.Cursor
			if len(page.Entries) == limit {
				return page, nil
			}
		}
	}
	// Everything up to the last complete line has been seen, so the next call
	// need not read it again.
	if len(page.Entries) == 0 || after.less(cursorPos{seen - 1, 1 << 20}) {
		page.Newer = endOf(seen)
	}
	return page, nil
}

type fileLine struct {
	off  int64
	text []byte
}

// splitLines cuts buf (which starts at file offset base) into lines.
func splitLines(buf []byte, base int64) []fileLine {
	var out []fileLine
	off := int64(0)
	for len(buf) > 0 {
		i := bytes.IndexByte(buf, '\n')
		var ln []byte
		if i < 0 {
			ln, buf = buf, nil
		} else {
			ln, buf = buf[:i], buf[i+1:]
		}
		if len(bytes.TrimSpace(ln)) > 0 {
			out = append(out, fileLine{base + off, ln})
		}
		off += int64(len(ln)) + 1
	}
	return out
}

var (
	markUser      = []byte(`"user"`)
	markAssistant = []byte(`"assistant"`)
)

// entriesOfLine turns one transcript line into entries. Lines that are not a
// user or assistant turn give none.
func entriesOfLine(line []byte, sidechain bool) []protocol.TextEntry {
	if !bytes.Contains(line, markUser) && !bytes.Contains(line, markAssistant) {
		return nil
	}
	var e struct {
		Type        string          `json:"type"`
		IsSidechain bool            `json:"isSidechain"`
		IsMeta      bool            `json:"isMeta"`
		Timestamp   string          `json:"timestamp"`
		Message     json.RawMessage `json:"message"`
	}
	if json.Unmarshal(line, &e) != nil || (e.Type != "user" && e.Type != "assistant") {
		return nil
	}
	if e.IsSidechain && !sidechain {
		return nil
	}
	var msg struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(e.Message, &msg) != nil || len(msg.Content) == 0 {
		return nil
	}
	var at int64
	if t, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil {
		at = t.UnixMilli()
	}
	mk := func(role, tool, text string) protocol.TextEntry {
		return protocol.TextEntry{Time: at, Role: role, Tool: tool, Text: text, Sidechain: e.IsSidechain}
	}
	base := protocol.RoleUser
	if e.Type == "assistant" {
		base = protocol.RoleAssistant
	}
	var s string
	if json.Unmarshal(msg.Content, &s) == nil {
		if strings.TrimSpace(s) == "" {
			return nil
		}
		return []protocol.TextEntry{mk(base, "", s)}
	}
	var blocks []struct {
		Type    string          `json:"type"`
		Text    string          `json:"text"`
		Name    string          `json:"name"`
		Input   json.RawMessage `json:"input"`
		Content json.RawMessage `json:"content"`
		IsError bool            `json:"is_error"`
	}
	if json.Unmarshal(msg.Content, &blocks) != nil {
		return nil
	}
	var out []protocol.TextEntry
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				out = append(out, mk(base, "", b.Text))
			}
		case "image":
			out = append(out, mk(base, "", "[image]"))
		case "tool_use":
			out = append(out, mk(protocol.RoleToolCall, b.Name, b.Name+": "+summarizeInput(b.Name, b.Input)))
		case "tool_result":
			text := resultText(b.Content)
			if b.IsError {
				text = "error: " + text
			}
			ent := mk(protocol.RoleToolResult, "", text)
			if len([]rune(ent.Text)) > textToolResultMax {
				ent.Text, ent.Cut = redact.Cut(ent.Text, textToolResultMax), true
			}
			out = append(out, ent)
		}
		// thinking and other blocks are not part of the conversation shown.
	}
	return out
}

// resultText reads the content of a tool result: a string or a list of blocks.
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "image":
			parts = append(parts, "[image]")
		}
	}
	return strings.Join(parts, "\n")
}

// summarizeInput says what a tool call does in a few words.
func summarizeInput(tool string, raw json.RawMessage) string {
	var in map[string]json.RawMessage
	if json.Unmarshal(raw, &in) != nil {
		return ""
	}
	str := func(keys ...string) string {
		for _, k := range keys {
			var s string
			if v, ok := in[k]; ok && json.Unmarshal(v, &s) == nil && s != "" {
				return s
			}
		}
		return ""
	}
	var s string
	switch tool {
	case "Bash":
		s = str("command")
	case "Read", "Edit", "Write", "MultiEdit", "NotebookEdit":
		s = str("file_path", "notebook_path")
	case "Grep":
		s = str("pattern")
		if p := str("path"); p != "" {
			s += " in " + p
		}
	case "Glob":
		s = str("pattern")
	case "WebFetch":
		s = str("url")
	case "WebSearch":
		s = str("query")
	case "Task", "Agent":
		s = str("description", "prompt")
	}
	if s == "" {
		// Any other tool: its first text field, in key order, or the whole input.
		keys := make([]string, 0, len(in))
		for k := range in {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if v := str(k); v != "" {
				s = v
				break
			}
		}
		if s == "" {
			s = string(raw)
		}
	}
	s = strings.Join(strings.Fields(s), " ")
	return redact.Cut(s, textToolInputMax)
}
