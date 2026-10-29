package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestModeOfKind(t *testing.T) {
	// The kinds Claude Code 2.1.312 accepts in a session file.
	cases := map[string]string{
		"interactive":   protocol.ModeForeground,
		"bg":            protocol.ModeBackground,
		"daemon":        protocol.ModeBackground,
		"daemon-worker": protocol.ModeBackground,
		"":              protocol.ModeUnknown, // an older Claude Code writes no kind
		"something-new": protocol.ModeBackground,
	}
	for kind, want := range cases {
		if got := modeOfKind(kind); got != want {
			t.Errorf("kind %q: %q, want %q", kind, got, want)
		}
	}
}

func assistant(model string, extra string) string {
	return fmt.Sprintf(`{"type":"assistant","uuid":"u","sessionId":"x"%s,"message":{"role":"assistant","model":%q,"content":[{"type":"text","text":"SECRET TEXT"}]}}`, extra, model)
}

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func lastModelOf(t *testing.T, lines ...string) (string, bool) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "t.jsonl")
	writeLines(t, p, lines...)
	fi, _ := os.Stat(p)
	return lastModel(p, fi.Size())
}

func TestLastModelTakesTheNewestAssistantEntry(t *testing.T) {
	user := `{"type":"user","message":{"role":"user","content":"hello"}}`
	m, ok := lastModelOf(t, user, assistant("claude-sonnet-5-5", ""), user, assistant("claude-opus-5-5", ""), user,
		`{"type":"system","subtype":"note"}`)
	if !ok || m != "claude-opus-5-5" {
		t.Fatalf("got %q %v", m, ok)
	}
}

func TestLastModelSkipsSubagentsAndPlaceholders(t *testing.T) {
	m, ok := lastModelOf(t,
		assistant("claude-opus-5-5", ""),
		assistant("claude-haiku-5", `,"isSidechain":true`),
		assistant("<synthetic>", ""),
		assistant("", ""))
	if !ok || m != "claude-opus-5-5" {
		t.Fatalf("got %q %v", m, ok)
	}
	if _, ok := lastModelOf(t, `{"type":"user"}`, assistant("<synthetic>", "")); ok {
		t.Fatal("a transcript with no real model must report none")
	}
}

func TestLastModelIgnoresAHalfWrittenLastLine(t *testing.T) {
	half := assistant("claude-haiku-5", "")
	half = half[:len(half)/2] // Claude Code is writing this line right now
	p := filepath.Join(t.TempDir(), "t.jsonl")
	os.WriteFile(p, []byte(assistant("claude-opus-5-5", "")+"\n"+half), 0o600)
	fi, _ := os.Stat(p)
	if m, ok := lastModel(p, fi.Size()); !ok || m != "claude-opus-5-5" {
		t.Fatalf("got %q %v", m, ok)
	}
}

func TestLastModelReadsFromTheEndOfALargeFile(t *testing.T) {
	// The model line is first; megabytes of other lines follow. A line longer
	// than one block must still be handled.
	big := `{"type":"user","message":{"content":"` + strings.Repeat("x", 3*tailBlock) + `"}}`
	lines := []string{assistant("claude-opus-5-5", "")}
	for i := 0; i < 20; i++ {
		lines = append(lines, big)
	}
	m, ok := lastModelOf(t, lines...)
	if !ok || m != "claude-opus-5-5" {
		t.Fatalf("got %q %v", m, ok)
	}
	// And the newest entry wins when it is near the end of a large file.
	lines = append(lines, assistant("claude-sonnet-5-5", ""), big)
	if m, _ := lastModelOf(t, lines...); m != "claude-sonnet-5-5" {
		t.Fatalf("got %q", m)
	}
}

func TestLastModelGivesUpOnAHugeFile(t *testing.T) {
	// The only model line lies further back than the read limit.
	p := filepath.Join(t.TempDir(), "t.jsonl")
	f, _ := os.Create(p)
	f.WriteString(assistant("claude-opus-5-5", "") + "\n")
	chunk := []byte(`{"type":"user","message":{"content":"` + strings.Repeat("y", 1<<20) + `"}}` + "\n")
	for i := 0; i < tailMaxBytes/len(chunk)+3; i++ {
		f.Write(chunk)
	}
	f.Close()
	fi, _ := os.Stat(p)
	if _, ok := lastModel(p, fi.Size()); ok {
		t.Fatal("read past the limit")
	}
}

func TestTranscriptDirRule(t *testing.T) {
	cases := map[string]string{
		"/home/nicolas/code/niparis/a2a-ocs":                     "-home-nicolas-code-niparis-a2a-ocs",
		"/home/n/code/x/.claude/worktrees/agent-a97bcf0f23c6359": "-home-n-code-x--claude-worktrees-agent-a97bcf0f23c6359",
		"/tmp/my_dir": "-tmp-my-dir",
	}
	for in, want := range cases {
		if got := transcriptDir(in); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}

func TestModelReaderFindsAndRefreshesAndKeepsTheLast(t *testing.T) {
	projects := t.TempDir()
	r := newModelReader(projects)
	path := filepath.Join(projects, transcriptDir("/work/x"), "sess-1.jsonl")
	if m := r.Model("sess-1", "/work/x"); m != "" {
		t.Fatalf("no transcript yet: %q", m)
	}
	writeLines(t, path, assistant("claude-opus-5-5", ""))
	if m := r.Model("sess-1", "/work/x"); m != "claude-opus-5-5" {
		t.Fatalf("got %q", m)
	}
	// /model changes the model: the next assistant entry has the new one.
	time.Sleep(10 * time.Millisecond)
	writeLines(t, path, assistant("claude-opus-5-5", ""), assistant("claude-sonnet-5-5", ""))
	if m := r.Model("sess-1", "/work/x"); m != "claude-sonnet-5-5" {
		t.Fatalf("after /model: %q", m)
	}
	// A new user line without an assistant entry keeps the last model.
	writeLines(t, path, assistant("claude-opus-5-5", ""), assistant("claude-sonnet-5-5", ""), `{"type":"user"}`)
	if m := r.Model("sess-1", "/work/x"); m != "claude-sonnet-5-5" {
		t.Fatalf("after a user line: %q", m)
	}
	// The folder name rule is only a first guess: another folder works too.
	other := filepath.Join(projects, "some-other-folder", "sess-2.jsonl")
	writeLines(t, other, assistant("claude-haiku-5", ""))
	if m := r.Model("sess-2", "/work/y"); m != "claude-haiku-5" {
		t.Fatalf("by search: %q", m)
	}
}

func TestModelReaderOnlyReadsWhenTheFileChanges(t *testing.T) {
	projects := t.TempDir()
	r := newModelReader(projects)
	path := filepath.Join(projects, transcriptDir("/work/x"), "s.jsonl")
	writeLines(t, path, assistant("claude-opus-5-5", ""))
	if m := r.Model("s", "/work/x"); m != "claude-opus-5-5" {
		t.Fatal(m)
	}
	// Same size and time: the answer comes from the cache, not from the file.
	fi, _ := os.Stat(path)
	r.mu.Lock()
	e := r.cache["s"]
	e.model = "from-cache"
	r.cache["s"] = e
	r.mu.Unlock()
	if m := r.Model("s", "/work/x"); m != "from-cache" {
		t.Fatalf("file was read again: %q", m)
	}
	os.Chtimes(path, time.Now(), fi.ModTime().Add(time.Second))
	if m := r.Model("s", "/work/x"); m != "claude-opus-5-5" {
		t.Fatalf("changed file not read: %q", m)
	}
}

func TestAdapterReportsModeAndModel(t *testing.T) {
	r := newRig(t)
	eventually(t, "session found", func() bool { return len(r.host.current()) == 1 })
	if s := r.host.current()[0]; s.Mode != protocol.ModeUnknown || s.Model != "" {
		t.Fatalf("no kind, no transcript: %+v", s)
	}
	projects := filepath.Join(filepath.Dir(r.sessions), "projects")
	writeLines(t, filepath.Join(projects, transcriptDir("/work/x"), "sess-1.jsonl"), assistant("claude-opus-5-5", ""))
	body := fmt.Sprintf(`{"pid":%d,"sessionId":"sess-1","cwd":"/work/x","name":"claude-x","status":"idle","kind":"bg","messagingSocketPath":%q}`,
		os.Getpid(), r.inboxPath)
	os.WriteFile(filepath.Join(r.sessions, fmt.Sprintf("%d.json", os.Getpid())), []byte(body), 0o600)
	eventually(t, "mode and model seen", func() bool {
		c := r.host.current()
		return len(c) == 1 && c[0].Mode == protocol.ModeBackground && c[0].Model == "claude-opus-5-5"
	})
	body = strings.Replace(body, `"kind":"bg"`, `"kind":"interactive"`, 1)
	os.WriteFile(filepath.Join(r.sessions, fmt.Sprintf("%d.json", os.Getpid())), []byte(body), 0o600)
	eventually(t, "foreground", func() bool { c := r.host.current(); return len(c) == 1 && c[0].Mode == protocol.ModeForeground })
}

func TestModelNeverLeavesTheTranscriptText(t *testing.T) {
	// The adapter's session info must not carry any transcript text.
	r := newRig(t)
	projects := filepath.Join(filepath.Dir(r.sessions), "projects")
	writeLines(t, filepath.Join(projects, transcriptDir("/work/x"), "sess-1.jsonl"), assistant("claude-opus-5-5", ""))
	eventually(t, "model seen", func() bool { c := r.host.current(); return len(c) == 1 && c[0].Model != "" })
	if s := fmt.Sprintf("%+v", r.host.current()[0]); strings.Contains(s, "SECRET TEXT") {
		t.Fatalf("transcript text in %s", s)
	}
}
