package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/claudeframes"
	"github.com/niparis/agent-net/internal/protocol"
)

func transcriptPath(r *rig) string {
	return filepath.Join(filepath.Dir(r.sessions), "projects", "-work-x", "sess-1.jsonl")
}

func appendHeldTranscript(t *testing.T, path string, entries ...any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
	}
}

// holdLine is the system entry Claude Code writes for a held frame. It has the
// real shape: the reply socket, the quoted first line, the inner frame's size,
// and its own id and time.
func holdLine(uuid, sock string, d claudeframes.Description, at time.Time) map[string]any {
	return map[string]any{
		"type": "system", "subtype": "informational", "uuid": uuid, "timestamp": at.UTC().Format(time.RFC3339Nano),
		"content": "Held peer message \u2014 from uds:" + sock + " (peer claims name: bob); preview: \u00ab" + d.Preview +
			"\u00bb \u2026[" + strconv.Itoa(d.Lines) + " lines, " + strconv.Itoa(d.Chars) +
			" chars total \u2014 expand to review before approving] \u2014 not delivered to Claude (1 held). " +
			"The sending session's permission mode class doesn't match this session's. Review it below, or set \"crossSessionInbound\" to \"accept\".",
	}
}

// Claude Code writes a system entry when it holds a peer message for review.
// The adapter reports the sender socket, the frame's size and first line, and
// the notice's own id, so the daemon can tie it to one delivery and apply it
// once.
func TestTranscriptHoldIsReported(t *testing.T) {
	r := newBookRig(t)
	m := request("m_hold", "s_bob", "c_1")
	m.Body = "do the thing"
	content, sock := r.deliverContent(m)
	desc, ok := claudeframes.Describe(content)
	if !ok {
		t.Fatal("the delivered frame has no wrapper")
	}
	at := time.Date(2026, 11, 23, 7, 10, 46, 0, time.UTC)
	appendHeldTranscript(t, transcriptPath(r), holdLine("entry-1", sock, desc, at))
	eventually(t, "the hold is reported", func() bool {
		r.host.mu.Lock()
		defer r.host.mu.Unlock()
		return len(r.host.held) == 1
	})
	r.host.mu.Lock()
	got := r.host.held[0]
	r.host.mu.Unlock()
	e := got.entry
	if got.recipient != "s_claude" || e.UUID != "entry-1" || e.ReplySock != sock ||
		e.Preview != desc.Preview || e.FrameLines != desc.Lines || e.FrameChars != desc.Chars || !e.At.Equal(at) {
		t.Fatalf("hold = %+v, want s_claude entry-1 for %q at %s", got, desc.Preview, at)
	}
	if e.Preview != "do the thing" {
		t.Fatalf("preview = %q, want the first body line", e.Preview)
	}
}

// deliverContent sends m and returns the exact frame content the session
// received, with its reply socket.
func (r *rig) deliverContent(m protocol.Message) (content, sock string) {
	r.t.Helper()
	res := r.a.Deliver(context.Background(), protocol.SessionInfo{NativeID: "sess-1", SessionID: "s_claude"}, m)
	if !res.Delivered {
		r.t.Fatalf("deliver %s: %+v", m.ID, res)
	}
	select {
	case line := <-r.lines:
		var f frame
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			r.t.Fatal(err)
		}
		return f.Message.Content, strings.TrimPrefix(f.From, "uds:")
	case <-time.After(5 * time.Second):
		r.t.Fatal("no frame in the inbox")
	}
	return "", ""
}

// The owner's approval writes the frame back. The adapter reports its exact
// content hash, so a message ID quoted in the body or in agent-net's notes
// cannot name another held message.
func TestTranscriptReleaseNamesTheFrameNotCitedIDs(t *testing.T) {
	r := newBookRig(t)
	m := request("m_keep", "s_bob", "c_keep")
	m.Body = "please keep this"
	content, sock := r.deliverContent(m)
	r.host.mu.Lock()
	var want string
	for _, row := range r.book.rows {
		if row.MessageID == m.ID {
			want = row.FrameHash
		}
	}
	r.host.mu.Unlock()
	if want == "" {
		t.Fatal("the frame was not recorded")
	}
	if got := claudeframes.Hash(content); got != want {
		t.Fatalf("frame hash %s, recorded %s", got, want)
	}
	appendHeldTranscript(t, transcriptPath(r), map[string]any{
		"type": "user", "uuid": "release-1",
		"message": map[string]any{"role": "user", "content": "Another Claude session sent a message:\n" + content +
			"\n[agent-net] this body also names m_aaaaaaaaaaaaaaaa and m_bbbbbbbbbbbbbbbb"},
	})
	eventually(t, "the release is reported", func() bool {
		r.host.mu.Lock()
		defer r.host.mu.Unlock()
		return len(r.host.released) == 1
	})
	r.host.mu.Lock()
	got := r.host.released[0]
	r.host.mu.Unlock()
	if got.recipient != "s_claude" || got.replySock != sock || got.frameHash != want {
		t.Fatalf("release = %+v, want the frame %s on %s", got, want, sock)
	}
}

// A release is written as a user entry, a queued command attachment, or a
// queue operation entry. Each form carries the frame unchanged, and each one
// releases it.
func TestTranscriptReleaseIsFoundInAnyEntryForm(t *testing.T) {
	forms := []struct {
		name  string
		entry func(content string) map[string]any
	}{
		{"user turn", func(content string) map[string]any {
			return map[string]any{"type": "user", "uuid": "release-user", "timestamp": time.Now().UTC().Format(time.RFC3339Nano),
				"message": map[string]any{"role": "user", "content": "Another Claude session sent a message:\n" + content}}
		}},
		{"queued command", func(content string) map[string]any {
			return map[string]any{"type": "attachment", "uuid": "release-queued", "timestamp": time.Now().UTC().Format(time.RFC3339Nano),
				"attachment": map[string]any{"type": "queued_command", "commandMode": "prompt", "prompt": content}}
		}},
		{"queue operation", func(content string) map[string]any {
			return map[string]any{"type": "queue-operation", "uuid": "release-queue", "timestamp": time.Now().UTC().Format(time.RFC3339Nano),
				"operation": "dequeue", "content": content}
		}},
	}
	for _, f := range forms {
		t.Run(f.name, func(t *testing.T) {
			r := newBookRig(t)
			m := request("m_release", "s_bob", "c_1")
			m.Body = "please release this"
			content, sock := r.deliverContent(m)
			appendHeldTranscript(t, transcriptPath(r), f.entry(content))
			eventually(t, "the release is reported", func() bool {
				r.host.mu.Lock()
				defer r.host.mu.Unlock()
				return len(r.host.released) == 1
			})
			r.host.mu.Lock()
			got := r.host.released[0]
			r.host.mu.Unlock()
			if got.replySock != sock || got.frameHash != claudeframes.Hash(content) {
				t.Fatalf("release = %+v, want the frame on %s", got, sock)
			}
		})
	}
}

// A line far larger than a frame is skipped without reading the whole
// transcript into memory, and the hold written after it is still found.
func TestTranscriptScansPastAHugeLine(t *testing.T) {
	r := newRig(t)
	eventually(t, "session found", func() bool { return len(r.host.current()) == 1 })
	path := transcriptPath(r)
	huge, err := json.Marshal(map[string]any{"type": "assistant", "uuid": "huge",
		"message": map[string]any{"content": strings.Repeat("x", 2<<20)}})
	if err != nil {
		t.Fatal(err)
	}
	appendHeldTranscript(t, path)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(huge, '\n')); err != nil {
		t.Fatal(err)
	}
	f.Close()
	body := "after the huge line"
	sock := "/tmp/x/r/s_bob.c_y.sock"
	content := "<cross-session-message from=\"uds:" + sock + "\">\n" + body + "\n\n[agent-net] note\n</cross-session-message>"
	desc, ok := claudeframes.Describe(content)
	if !ok {
		t.Fatal("the frame has no wrapper")
	}
	appendHeldTranscript(t, path, holdLine("entry-after", sock, desc, time.Now()))
	eventually(t, "the hold after the huge line is reported", func() bool {
		r.host.mu.Lock()
		defer r.host.mu.Unlock()
		return len(r.host.held) == 1
	})
	r.host.mu.Lock()
	got := r.host.held[0].entry
	r.host.mu.Unlock()
	if got.UUID != "entry-after" || got.Preview != body {
		t.Fatalf("hold after the huge line = %+v", got)
	}
}

// A transcript whose file is not in the folder named after its working folder
// is searched for once, not on every poll.
func TestTranscriptPathIsSearchedOnce(t *testing.T) {
	dir := t.TempDir()
	projects := filepath.Join(dir, "projects")
	if err := os.MkdirAll(filepath.Join(projects, "-other"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projects, "-other", "sess-1.jsonl")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := globTranscripts
	calls := 0
	globTranscripts = func(pattern string) ([]string, error) {
		calls++
		return old(pattern)
	}
	defer func() { globTranscripts = old }()
	a := New(Config{ProjectsDir: projects, SessionsDir: filepath.Join(dir, "sessions"), ReplyDir: filepath.Join(dir, "r")})
	for i := 0; i < 5; i++ {
		if got := a.transcriptPath("sess-1", "/work/x"); got != path {
			t.Fatalf("path %d = %q, want %q", i, got, path)
		}
	}
	if calls != 1 {
		t.Fatalf("searches = %d, want 1", calls)
	}
}

// BenchmarkTranscriptLookup30Sessions is the per-poll transcript lookup for 30
// sessions whose files are not in the folder named after their working folder.
// The old lookup searched every project folder on every poll; the adapter
// remembers the path and searches at most once a minute.
func BenchmarkTranscriptLookup30Sessions(b *testing.B) {
	projects := b.TempDir()
	if err := os.MkdirAll(filepath.Join(projects, "-other"), 0o700); err != nil {
		b.Fatal(err)
	}
	natives := make([]string, 30)
	for i := range natives {
		natives[i] = "sess-" + strconv.Itoa(i)
		if err := os.WriteFile(filepath.Join(projects, "-other", natives[i]+".jsonl"), nil, 0o600); err != nil {
			b.Fatal(err)
		}
	}
	oldLookup := func(native string) string {
		name := native + ".jsonl"
		p := filepath.Join(projects, "-work", name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
		matches, _ := filepath.Glob(filepath.Join(projects, "*", name))
		if len(matches) > 0 {
			return matches[0]
		}
		return ""
	}
	a := New(Config{ProjectsDir: projects, SessionsDir: filepath.Join(projects, "sessions"), ReplyDir: filepath.Join(projects, "r")})
	for _, native := range natives {
		a.transcriptPath(native, "/work/x")
	}
	b.Run("before/glob-every-poll", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			for _, native := range natives {
				oldLookup(native)
			}
		}
	})
	b.Run("after/remembered-path", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			for _, native := range natives {
				a.transcriptPath(native, "/work/x")
			}
		}
	})
}
