package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The transcript entries below copy the shape Claude Code 2.1 writes. The
// text is made up: only the origin fields matter to these tests.
var (
	scheduledOrigin = map[string]any{"isMeta": true, "promptSource": "system", "turnOrigin": "scheduled", "scheduledTaskId": "task-1"}
	typedOrigin     = map[string]any{"promptSource": "typed", "turnOrigin": "human"}
)

// writeClaudeTranscript writes a transcript file named like Claude Code's, one
// user entry per prompt, and returns its path.
func writeClaudeTranscript(t *testing.T, dir, session string, entries ...map[string]any) string {
	t.Helper()
	path := filepath.Join(dir, session+".jsonl")
	if err := os.WriteFile(path, claudeTranscriptBytes(t, entries...), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func claudeTranscriptBytes(t *testing.T, entries ...map[string]any) []byte {
	t.Helper()
	var out []byte
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, line...)
		out = append(out, '\n')
	}
	return out
}

// claudeUserEntry is one user entry of a transcript: the prompt ID, the text
// and the origin fields.
func claudeUserEntry(promptID, text string, origin map[string]any) map[string]any {
	e := map[string]any{
		"type":     "user",
		"promptId": promptID,
		"message":  map[string]any{"role": "user", "content": text},
	}
	for k, v := range origin {
		e[k] = v
	}
	return e
}

// claudeSessionFor starts a gateway and a Claude Code session that the daemon
// has registered, and returns the gateway session ID of that session.
func claudeSessionFor(t *testing.T, native string) (*testGateway, *node, string) {
	t.Helper()
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "claude"}
	n := startNode(t, g, nodeOpts{adapter: fa, cmdline: "/usr/bin/pi"})
	fa.setSessions(protocol.SessionInfo{Harness: "claude", NativeID: native, Cwd: "/code", Status: "idle"})
	var sid string
	eventually(t, "Claude registered", func() bool {
		for _, s := range n.d.Sessions() {
			if s.NativeID == native {
				sid = s.SessionID
			}
		}
		return sid != ""
	})
	return g, n, sid
}

// A prompt that Claude Code runs on a schedule is not the owner typing: the
// owner's asks and the owner-use count must not move.
func TestClaudeScheduledPromptIsNotTheOwner(t *testing.T) {
	const native, promptID = "claude-cron", "prompt-cron"
	g, n, sid := claudeSessionFor(t, native)
	transcript := writeClaudeTranscript(t, t.TempDir(), native,
		claudeUserEntry(promptID, "check the release again", scheduledOrigin))
	askPolicy(t, cliClient(t, n), protocol.PolicyRequest{
		Harness: "claude", NativeID: native, Prompt: "check the release again",
		PromptID: promptID, TranscriptPath: transcript,
	})
	a, _, err := g.gw.Store().Agent(sid)
	if err != nil || a.OwnerUses != 0 {
		t.Fatalf("a scheduled prompt counted as the owner typing: %+v %v", a, err)
	}
}

// The owner's typed prompt still counts, when its transcript entry is there
// when the hook runs.
func TestClaudeTypedPromptIsTheOwner(t *testing.T) {
	const native, promptID = "claude-typed", "prompt-typed"
	g, n, sid := claudeSessionFor(t, native)
	transcript := writeClaudeTranscript(t, t.TempDir(), native,
		claudeUserEntry(promptID, "fix it", typedOrigin))
	askPolicy(t, cliClient(t, n), protocol.PolicyRequest{
		Harness: "claude", NativeID: native, Prompt: "fix it",
		PromptID: promptID, TranscriptPath: transcript,
	})
	a, _, err := g.gw.Store().Agent(sid)
	if err != nil || a.OwnerUses != 1 {
		t.Fatalf("typed prompt count: %+v %v", a, err)
	}
}

// Claude Code writes the prompt to the transcript after the hook has run, so
// the typed prompt is counted once its entry appears.
func TestClaudeTypedPromptCountsAfterTheTranscriptWrite(t *testing.T) {
	const native, promptID = "claude-late", "prompt-late"
	g, n, sid := claudeSessionFor(t, native)
	dir := t.TempDir()
	transcript := filepath.Join(dir, native+".jsonl")
	askPolicy(t, cliClient(t, n), protocol.PolicyRequest{
		Harness: "claude", NativeID: native, Prompt: "fix it",
		PromptID: promptID, TranscriptPath: transcript,
	})
	if err := os.WriteFile(transcript, claudeTranscriptBytes(t, claudeUserEntry(promptID, "fix it", typedOrigin)), 0o600); err != nil {
		t.Fatal(err)
	}
	n.d.claudeChecks.Wait()
	a, _, err := g.gw.Store().Agent(sid)
	if err != nil || a.OwnerUses != 1 {
		t.Fatalf("typed prompt written late: %+v %v", a, err)
	}
}

// The hook also runs before Claude Code writes a scheduled prompt. The check
// waits for that entry and does not count the prompt as the owner's.
func TestClaudeLateScheduledPromptIsNotTheOwner(t *testing.T) {
	const native, promptID, text = "claude-late-cron", "prompt-late-cron", "check the release again"
	g, n, sid := claudeSessionFor(t, native)
	transcript := filepath.Join(t.TempDir(), native+".jsonl")
	askPolicy(t, cliClient(t, n), protocol.PolicyRequest{
		Harness: "claude", NativeID: native, Prompt: text,
		PromptID: promptID, TranscriptPath: transcript,
	})
	if err := os.WriteFile(transcript, claudeTranscriptBytes(t, claudeUserEntry(promptID, text, scheduledOrigin)), 0o600); err != nil {
		t.Fatal(err)
	}
	n.d.claudeChecks.Wait()
	a, _, err := g.gw.Store().Agent(sid)
	if err != nil || a.OwnerUses != 0 {
		t.Fatalf("a scheduled prompt written late counted as the owner typing: %+v %v", a, err)
	}
}

var peerOrigin = map[string]any{"isMeta": true, "promptSource": "system", "turnOrigin": "peer", "origin": map[string]any{"kind": "peer"}}

// Each origin Claude Code writes on a prompt entry maps to who sent it. An
// entry with no origin fields stays unknown, so the owner check keeps its old
// answer for it.
func TestClaudeSenderOfTranscriptEntry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		origin map[string]any
		want   claudeSender
	}{
		{"typed", typedOrigin, senderOwner},
		{"queued", map[string]any{"promptSource": "queued", "turnOrigin": "human"}, senderOwner},
		{"scheduled", scheduledOrigin, senderOther},
		{"peer", peerOrigin, senderOther},
		{"task notification", map[string]any{"promptSource": "system", "turnOrigin": "task_notification"}, senderOther},
		{"sdk", map[string]any{"promptSource": "sdk", "turnOrigin": "sdk"}, senderOther},
		{"meta without origin", map[string]any{"isMeta": true}, senderOther},
		{"no origin fields", nil, senderUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeClaudeTranscript(t, t.TempDir(), "claude-x", claudeUserEntry("p-1", "hello", tc.origin))
			got, found := claudeSenderOf(path, "claude-x", "p-1")
			if !found || got != tc.want {
				t.Fatalf("sender = %v (found %v), want %v", got, found, tc.want)
			}
		})
	}
}

// The entry is the one with the prompt ID, anywhere in the tail of the file.
// A missing entry, another session's file or an empty ID finds nothing.
func TestClaudeSenderOfFindsOnlyThePromptsEntry(t *testing.T) {
	dir := t.TempDir()
	later := writeClaudeTranscript(t, t.TempDir(), "claude-x",
		claudeUserEntry("p-1", "fix it", typedOrigin),
		claudeUserEntry("p-2", "other", scheduledOrigin),
		claudeUserEntry("p-3", "more", peerOrigin))
	if got, found := claudeSenderOf(later, "claude-x", "p-1"); !found || got != senderOwner {
		t.Fatalf("entry in the middle: sender %v found %v", got, found)
	}
	missing := writeClaudeTranscript(t, dir, "claude-x", claudeUserEntry("p-2", "other", typedOrigin))
	for _, tc := range []struct{ name, path, session, prompt string }{
		{"no entry for the prompt", missing, "claude-x", "p-1"},
		{"another session's file", missing, "claude-y", "p-2"},
		{"no transcript yet", filepath.Join(dir, "absent", "claude-x.jsonl"), "claude-x", "p-1"},
		{"empty prompt ID", missing, "claude-x", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, found := claudeSenderOf(tc.path, tc.session, tc.prompt); found {
				t.Fatal("found an entry that is not the prompt's")
			}
		})
	}
}

// Claude Code writes the prompt after the hook has run, so the wait finds the
// entry when it appears. A prompt that never appears gives the old answer.
func TestClaudeSenderWithinWaitsForTheEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude-x.jsonl")
	data := claudeTranscriptBytes(t, claudeUserEntry("p-late", "check again", scheduledOrigin))
	time.AfterFunc(200*time.Millisecond, func() { _ = os.WriteFile(path, data, 0o600) })
	if got := claudeSenderWithin(path, "claude-x", "p-late", 5*time.Second); got != senderOther {
		t.Fatalf("sender = %v, want the scheduled entry", got)
	}
	if got := claudeSenderWithin(filepath.Join(dir, "never.jsonl"), "never", "p-1", 300*time.Millisecond); got != senderUnknown {
		t.Fatalf("missing entry: sender = %v, want unknown", got)
	}
}

// A worker start may take its quote only from the owner's words, so a
// scheduled prompt must not be kept as them.
func TestClaudeScheduledPromptIsNotOwnerWords(t *testing.T) {
	const native, promptID, text = "claude-cron", "prompt-cron", "use a codex worker for this"
	_, n, sid := claudeSessionFor(t, native)
	transcript := writeClaudeTranscript(t, t.TempDir(), native, claudeUserEntry(promptID, text, scheduledOrigin))
	askPolicy(t, cliClient(t, n), protocol.PolicyRequest{
		Harness: "claude", NativeID: native, Prompt: text,
		PromptID: promptID, TranscriptPath: transcript,
	})
	if n.d.ownerWordsMatch(sid, text) {
		t.Fatal("a scheduled prompt was kept as the owner's words")
	}
}

// The same holds in the CTO's own Claude pane, where the owner's words let the
// CTO act without a proposal.
func TestClaudeScheduledPromptInTheCTOPaneIsNotOwnerWords(t *testing.T) {
	const native, promptID, text = "cto-cron", "prompt-cto-cron", "remove the old machine"
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	if err := n.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	n.d.cto.mu.Lock()
	n.d.claudeCTO.session = native
	n.d.cto.mu.Unlock()
	transcript := writeClaudeTranscript(t, t.TempDir(), native, claudeUserEntry(promptID, text, scheduledOrigin))
	askPolicy(t, cliClient(t, n), protocol.PolicyRequest{
		Harness: "claude", NativeID: native, Prompt: text,
		PromptID: promptID, TranscriptPath: transcript,
	})
	if n.d.ownerWordsMatch(n.d.ctoSessionID(), text) {
		t.Fatal("a scheduled prompt in the CTO pane was kept as the owner's words")
	}
}
