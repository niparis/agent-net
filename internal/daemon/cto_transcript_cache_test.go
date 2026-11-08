package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func ctoLine(kind, text string) string {
	return fmt.Sprintf(`{"type":%q,"timestamp":"2026-01-02T03:04:05Z","message":{"content":%q}}`, kind, text) + "\n"
}

// The cached read must give what a full parse gives, after every kind of
// change to the file.
func TestCTOTranscriptCacheMatchesFullParse(t *testing.T) {
	projects := t.TempDir()
	dir := filepath.Join(projects, "proj")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "s.jsonl")
	var c ctoTranscriptCache
	check := func(step string) {
		t.Helper()
		got, gerr := c.read(projects, "s")
		want, werr := claudeCTOTranscript(projects, "s")
		if (gerr == nil) != (werr == nil) || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: cached=%s %v full=%s %v", step, got, gerr, want, werr)
		}
	}
	write := func(data string, appendTo bool) {
		t.Helper()
		flag := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
		if appendTo {
			flag = os.O_CREATE | os.O_WRONLY | os.O_APPEND
		}
		f, err := os.OpenFile(path, flag, 0600)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString(data)
		f.Close()
	}
	check("missing file")
	write(ctoLine("user", "hello")+ctoLine("assistant", "<command-name>/mcp</command-name>"), false)
	check("first lines")
	check("unchanged")
	write(ctoLine("user", "<local-command-stdout>reconnected"), true)
	check("append uses the command seen before")
	partial := ctoLine("assistant", "being written")
	write(partial[:20], true)
	check("partial line")
	write(partial[20:], true)
	check("partial line completed")
	write("not json\n\n"+`{"type":"assistant","isSidechain":true,"message":{"content":"x"}}`+"\r\n"+ctoLine("user", "crlf"), true)
	check("noise lines")
	write(ctoLine("user", "rewritten, and longer than before")+strings.Repeat(ctoLine("assistant", "again"), 20), false)
	check("rewritten file")
	write(ctoLine("user", "short"), false)
	check("shorter file")
	os.Remove(path)
	check("removed file")
}

func TestCTOTranscriptCacheParsesOnlyNewLines(t *testing.T) {
	projects := t.TempDir()
	dir := filepath.Join(projects, "proj")
	os.Mkdir(dir, 0700)
	path := filepath.Join(dir, "s.jsonl")
	os.WriteFile(path, []byte(strings.Repeat(ctoLine("user", "old"), 50)), 0600)
	var c ctoTranscriptCache
	if m, err := c.read(projects, "s"); err != nil || len(m) != 50 || c.parsed != 50 {
		t.Fatalf("first read: %d messages, %d parsed, %v", len(m), c.parsed, err)
	}
	if _, err := c.read(projects, "s"); err != nil || c.parsed != 50 {
		t.Fatalf("unchanged file parsed again: %d", c.parsed)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString(ctoLine("assistant", "new"))
	f.Close()
	if m, err := c.read(projects, "s"); err != nil || len(m) != 51 || c.parsed != 51 {
		t.Fatalf("append: %d messages, %d parsed, %v", len(m), c.parsed, err)
	}
}

func BenchmarkCTOTranscript(b *testing.B) {
	projects := b.TempDir()
	dir := filepath.Join(projects, "proj")
	os.Mkdir(dir, 0700)
	var sb strings.Builder
	for i := 0; i < 20000; i++ {
		sb.WriteString(ctoLine("assistant", strings.Repeat("x", 400)))
	}
	os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(sb.String()), 0600)
	b.Run("full", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			claudeCTOTranscript(projects, "s")
		}
	})
	b.Run("cached", func(b *testing.B) {
		var c ctoTranscriptCache
		for i := 0; i < b.N; i++ {
			c.read(projects, "s")
		}
	})
}
