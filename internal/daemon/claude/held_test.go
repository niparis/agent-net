package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
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

// Claude Code writes a system entry when it holds a peer message for review.
// The adapter reports it with the sender and conversation from the reply
// socket the entry names.
func TestTranscriptHoldIsReported(t *testing.T) {
	r := newRig(t)
	eventually(t, "session found", func() bool { return len(r.host.current()) == 1 })
	at := time.Date(2026, 11, 23, 7, 10, 46, 0, time.UTC)
	appendHeldTranscript(t, transcriptPath(r), map[string]any{
		"type": "system", "subtype": "informational", "timestamp": at.Format(time.RFC3339Nano),
		"content": "Held peer message \u2014 from uds:/tmp/agent-net-501/r/s_bob.c_6f1b93f3c3bf060c.sock (peer claims name: bob); preview: \u00abdo the thing\u00bb \u2026[1 line, 12 chars \u2014 expand to review before approving] \u2014 not delivered to Claude (1 held). The sending session's permission mode class doesn't match this session's. Review it below, or set \"crossSessionInbound\" to \"accept\".",
	})
	eventually(t, "the hold is reported", func() bool {
		r.host.mu.Lock()
		defer r.host.mu.Unlock()
		return len(r.host.held) == 1
	})
	r.host.mu.Lock()
	got := r.host.held[0]
	r.host.mu.Unlock()
	if got.recipient != "s_claude" || got.sender != "s_bob" || got.conversation != "c_6f1b93f3c3bf060c" || !got.at.Equal(at) {
		t.Fatalf("hold = %+v, want s_claude <- s_bob in c_6f1b93f3c3bf060c at %s", got, at)
	}
}

// A user entry with the peer frame is the owner releasing the held message.
// The adapter reports the message ID, and the older wording of the hold notice
// is accepted too.
func TestTranscriptReleaseIsReported(t *testing.T) {
	r := newRig(t)
	eventually(t, "session found", func() bool { return len(r.host.current()) == 1 })
	path := transcriptPath(r)
	appendHeldTranscript(t, path, map[string]any{
		"type": "system", "subtype": "informational",
		"content": "Held message from another session \u2014 from uds:/tmp/agent-net-501/r/s_bob.c_aa7778df4ba6d9aa.sock; preview: \u00abdo the thing\u00bb \u2014 not delivered to Claude (1 held). The sending session's permission mode class doesn't match this session's.",
	})
	eventually(t, "the hold is reported", func() bool {
		r.host.mu.Lock()
		defer r.host.mu.Unlock()
		return len(r.host.held) == 1
	})
	appendHeldTranscript(t, path, map[string]any{
		"type": "user",
		"message": map[string]any{"role": "user", "content": "Another Claude session sent a message:\n" +
			"<cross-session-message from=\"uds:/tmp/agent-net-501/r/s_bob.c_aa7778df4ba6d9aa.sock\" from-name=\"bob\" from-mode=\"prompting\">\n" +
			"do the thing\n\n[agent-net] message m_0123456789abcdef from bob (pi, session s_bob).\n</cross-session-message>"},
	})
	eventually(t, "the release is reported", func() bool {
		r.host.mu.Lock()
		defer r.host.mu.Unlock()
		return len(r.host.released) == 1
	})
	r.host.mu.Lock()
	got := r.host.released[0]
	r.host.mu.Unlock()
	if got.recipient != "s_claude" || got.messageID != "m_0123456789abcdef" || got.sender != "s_bob" || got.conversation != "c_aa7778df4ba6d9aa" {
		t.Fatalf("release = %+v", got)
	}
}
