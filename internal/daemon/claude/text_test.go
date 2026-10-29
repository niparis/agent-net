package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func jl(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

type m = map[string]any

func userLine(ts, text string) string {
	b, _ := json.Marshal(m{"type": "user", "timestamp": ts, "isSidechain": false,
		"message": m{"role": "user", "content": text}})
	return string(b) + "\n"
}

func assistantLine(ts string, side bool, blocks ...m) string {
	b, _ := json.Marshal(m{"type": "assistant", "timestamp": ts, "isSidechain": side,
		"message": m{"role": "assistant", "model": "claude-x", "content": blocks}})
	return string(b) + "\n"
}

func resultLine(ts, text string) string {
	b, _ := json.Marshal(m{"type": "user", "timestamp": ts,
		"message": m{"role": "user", "content": []m{{"type": "tool_result", "tool_use_id": "t1", "content": text}}}})
	return string(b) + "\n"
}

func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "")), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func texts(p protocol.TextPage) []string {
	var out []string
	for _, e := range p.Entries {
		out = append(out, e.Role+"|"+e.Text)
	}
	return out
}

func TestTextReadsRolesInOrderAndSkipsNoise(t *testing.T) {
	big := jl(t, m{"type": "api-request-blob", "message": m{"content": []m{{"type": "text", "text": strings.Repeat("x", 200000)}}}})
	path := writeTranscript(t,
		jl(t, m{"type": "permission-mode", "mode": "default"}),
		userLine("2026-10-29T10:00:00Z", "fix the build"),
		big,
		assistantLine("2026-10-29T10:00:01Z", false, m{"type": "thinking", "thinking": "hmm"}, m{"type": "text", "text": "Looking."},
			m{"type": "tool_use", "id": "t1", "name": "Bash", "input": m{"command": "go build ./...", "description": "build"}}),
		resultLine("2026-10-29T10:00:02Z", "ok"),
		assistantLine("2026-10-29T10:00:03Z", true, m{"type": "text", "text": "sub-agent chatter"}),
		assistantLine("2026-10-29T10:00:04Z", false, m{"type": "tool_use", "id": "t2", "name": "Read", "input": m{"file_path": "/src/main.go"}}),
		assistantLine("2026-10-29T10:00:05Z", false, m{"type": "text", "text": "Done."}),
	)
	pg, err := readTranscript(path, protocol.TextQuery{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"user|fix the build",
		"assistant|Looking.",
		"tool_call|Bash: go build ./...",
		"tool_result|ok",
		"tool_call|Read: /src/main.go",
		"assistant|Done.",
	}
	if got := texts(pg); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("entries =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if pg.Older != "" {
		t.Errorf("older = %q, but the start was reached", pg.Older)
	}
	if pg.Newer == "" {
		t.Error("no cursor to follow from")
	}
	if pg.Entries[0].Time != 1793268000000 {
		t.Errorf("time = %d", pg.Entries[0].Time)
	}
	if pg.Entries[2].Tool != "Bash" {
		t.Errorf("tool = %q", pg.Entries[2].Tool)
	}
	// With sidechain on, the sub-agent line appears, marked.
	pg, _ = readTranscript(path, protocol.TextQuery{Sidechain: true})
	var side int
	for _, e := range pg.Entries {
		if e.Sidechain {
			side++
		}
	}
	if len(pg.Entries) != 7 || side != 1 {
		t.Errorf("with sidechain: %d entries, %d marked", len(pg.Entries), side)
	}
}

func TestTextPagesBackWithoutGapsOrRepeats(t *testing.T) {
	var lines []string
	var all []string
	for i := 0; i < 40; i++ {
		lines = append(lines, userLine("2026-10-29T10:00:00Z", fmt.Sprintf("prompt %02d", i)))
		all = append(all, fmt.Sprintf("user|prompt %02d", i))
		lines = append(lines, assistantLine("2026-10-29T10:00:01Z", false,
			m{"type": "text", "text": fmt.Sprintf("answer %02d", i)},
			m{"type": "tool_use", "id": "x", "name": "Grep", "input": m{"pattern": fmt.Sprintf("p%02d", i), "path": "/src"}}))
		all = append(all, fmt.Sprintf("assistant|answer %02d", i), fmt.Sprintf("tool_call|Grep: p%02d in /src", i))
	}
	path := writeTranscript(t, lines...)

	var got []string
	q := protocol.TextQuery{Limit: 7}
	for rounds := 0; ; rounds++ {
		if rounds > 100 {
			t.Fatal("paging does not end")
		}
		pg, err := readTranscript(path, q)
		if err != nil {
			t.Fatal(err)
		}
		if len(pg.Entries) > 7 {
			t.Fatalf("page of %d entries", len(pg.Entries))
		}
		got = append(texts(pg), got...)
		if pg.Older == "" {
			break
		}
		q.Before = pg.Older
	}
	if strings.Join(got, "\n") != strings.Join(all, "\n") {
		t.Fatalf("paged entries differ:\n%s", strings.Join(got, "\n"))
	}
}

func TestTextPagesBackThroughALineLongerThanABlock(t *testing.T) {
	long := strings.Repeat("y", 300000)
	path := writeTranscript(t,
		userLine("2026-10-29T10:00:00Z", "first"),
		userLine("2026-10-29T10:00:00Z", long),
		userLine("2026-10-29T10:00:00Z", "last"),
	)
	pg, err := readTranscript(path, protocol.TextQuery{Limit: 1})
	if err != nil || len(pg.Entries) != 1 || pg.Entries[0].Text != "last" || pg.Older == "" {
		t.Fatalf("page = %+v err %v", pg, err)
	}
	pg, err = readTranscript(path, protocol.TextQuery{Limit: 1, Before: pg.Older})
	if err != nil || len(pg.Entries) != 1 || len(pg.Entries[0].Text) != 300000 {
		t.Fatalf("long entry page: %d entries, err %v", len(pg.Entries), err)
	}
	pg, err = readTranscript(path, protocol.TextQuery{Limit: 5, Before: pg.Older})
	if err != nil || len(pg.Entries) != 1 || pg.Entries[0].Text != "first" || pg.Older != "" {
		t.Fatalf("first page = %+v err %v", pg, err)
	}
}

func TestTextFollowReturnsOnlyNewEntries(t *testing.T) {
	path := writeTranscript(t,
		userLine("2026-10-29T10:00:00Z", "one"),
		assistantLine("2026-10-29T10:00:01Z", false, m{"type": "text", "text": "two"}),
	)
	pg, _ := readTranscript(path, protocol.TextQuery{})
	cur := pg.Newer
	again, err := readTranscript(path, protocol.TextQuery{After: cur})
	if err != nil || len(again.Entries) != 0 || again.Newer != cur {
		t.Fatalf("nothing new: %+v err %v", again, err)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	// A noise line, a complete entry, and a half-written line.
	f.WriteString(jl(t, m{"type": "mode", "mode": "x"}))
	f.WriteString(userLine("2026-10-29T10:00:02Z", "three"))
	half := userLine("2026-10-29T10:00:03Z", "four")
	f.WriteString(half[:len(half)-10])
	f.Close()
	next, err := readTranscript(path, protocol.TextQuery{After: cur})
	if err != nil || strings.Join(texts(next), ",") != "user|three" {
		t.Fatalf("new entries = %v err %v", texts(next), err)
	}
	// The half line completes; only it comes next.
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(half[len(half)-10:])
	f.Close()
	last, err := readTranscript(path, protocol.TextQuery{After: next.Newer})
	if err != nil || strings.Join(texts(last), ",") != "user|four" {
		t.Fatalf("last entries = %v err %v", texts(last), err)
	}
	// A cursor taken after only noise does not rescan it, and still finds later entries.
	none, _ := readTranscript(path, protocol.TextQuery{After: last.Newer})
	if len(none.Entries) != 0 {
		t.Fatalf("entries = %v", texts(none))
	}
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(userLine("2026-10-29T10:00:04Z", "five"))
	f.Close()
	five, _ := readTranscript(path, protocol.TextQuery{After: none.Newer})
	if strings.Join(texts(five), ",") != "user|five" {
		t.Fatalf("after noise cursor: %v", texts(five))
	}
}

func TestTextShortensToolResultsAndSummarisesInputs(t *testing.T) {
	path := writeTranscript(t,
		assistantLine("2026-10-29T10:00:01Z", false,
			m{"type": "tool_use", "id": "a", "name": "mcp__x__thing", "input": m{"zeta": "last", "alpha": "first value"}},
			m{"type": "tool_use", "id": "b", "name": "Task", "input": m{"description": "explore the repo", "prompt": "long prompt"}}),
		resultLine("2026-10-29T10:00:02Z", strings.Repeat("r", 5000)),
	)
	pg, _ := readTranscript(path, protocol.TextQuery{})
	if len(pg.Entries) != 3 {
		t.Fatalf("entries = %v", texts(pg))
	}
	if pg.Entries[0].Text != "mcp__x__thing: first value" || pg.Entries[1].Text != "Task: explore the repo" {
		t.Errorf("summaries = %q, %q", pg.Entries[0].Text, pg.Entries[1].Text)
	}
	res := pg.Entries[2]
	if !res.Cut || len([]rune(res.Text)) != textToolResultMax {
		t.Errorf("result: cut %v, %d chars", res.Cut, len([]rune(res.Text)))
	}
}

func TestTextRejectsBadCursors(t *testing.T) {
	path := writeTranscript(t, userLine("2026-10-29T10:00:00Z", "one"))
	for _, q := range []protocol.TextQuery{{Before: "nonsense"}, {After: "1:x"}, {Before: "0:0", After: "0:0"}, {Before: "99999:0"}} {
		if _, err := readTranscript(path, q); err == nil {
			t.Errorf("query %+v accepted", q)
		}
	}
}

func TestAdapterTextFindsTheTranscript(t *testing.T) {
	proj := t.TempDir()
	dir := filepath.Join(proj, transcriptDir("/work/app"))
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "sess1.jsonl"), []byte(userLine("2026-10-29T10:00:00Z", "hello")), 0o600)
	a := &Adapter{models: newModelReader(proj)}
	pg, err := a.Text(context.Background(), protocol.SessionInfo{NativeID: "sess1", Cwd: "/work/app"}, protocol.TextQuery{})
	if err != nil || len(pg.Entries) != 1 || pg.Entries[0].Text != "hello" {
		t.Fatalf("page = %+v err %v", pg, err)
	}
	if _, err := a.Text(context.Background(), protocol.SessionInfo{NativeID: "missing", Cwd: "/work/app"}, protocol.TextQuery{}); err == nil {
		t.Error("a missing transcript should be an error")
	}
}
