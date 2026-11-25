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

// The idle compaction line the daemon types is recorded as a human prompt, so
// the transcript says "owner". The daemon knows it typed the line, so the owner
// effects must not run. Another text in the same session still counts.
func TestClaudeDaemonTypedPromptIsNotTheOwner(t *testing.T) {
	const native, typed = "claude-compact", "/compact Keep: the current task."
	g, n, sid := claudeSessionFor(t, native)
	n.d.NoteTypedPrompt("claude", native, typed)
	transcript := writeClaudeTranscript(t, t.TempDir(), native,
		claudeUserEntry("p-compact", typed, typedOrigin),
		claudeUserEntry("p-owner", "fix it", typedOrigin))
	c := cliClient(t, n)
	askPolicy(t, c, protocol.PolicyRequest{
		Harness: "claude", NativeID: native, Prompt: typed,
		PromptID: "p-compact", TranscriptPath: transcript,
	})
	a, _, err := g.gw.Store().Agent(sid)
	if err != nil || a.OwnerUses != 0 {
		t.Fatalf("a prompt the daemon typed counted as the owner typing: %+v %v", a, err)
	}
	askPolicy(t, c, protocol.PolicyRequest{
		Harness: "claude", NativeID: native, Prompt: "fix it",
		PromptID: "p-owner", TranscriptPath: transcript,
	})
	a, _, err = g.gw.Store().Agent(sid)
	if err != nil || a.OwnerUses != 1 {
		t.Fatalf("the owner's own prompt did not count: %+v %v", a, err)
	}
}

// The owner effects are skipped for a daemon-typed line even though its
// transcript entry says "human". This needs no socket.
func TestWhenClaudeOwnerPromptSkipsDaemonTypedText(t *testing.T) {
	const native, typed = "claude-skip", "/compact Keep: the current task."
	transcript := writeClaudeTranscript(t, t.TempDir(), native,
		claudeUserEntry("p-1", typed, typedOrigin), claudeUserEntry("p-2", "fix it", typedOrigin))
	var d Daemon
	d.NoteTypedPrompt("claude", native, typed)
	ran := map[string]bool{}
	for _, tc := range []struct{ id, text string }{{"p-1", typed}, {"p-2", "fix it"}} {
		d.whenClaudeOwnerPrompt(context.Background(), protocol.PolicyRequest{
			Harness: "claude", NativeID: native, Prompt: tc.text, PromptID: tc.id, TranscriptPath: transcript,
		}, func(context.Context, bool) { ran[tc.id] = true })
	}
	if ran["p-1"] || !ran["p-2"] {
		t.Fatalf("owner effects ran for %v, want only p-2", ran)
	}
}

// A note expires, so the same text typed by the owner later counts again.
func TestDaemonTypedNoteExpires(t *testing.T) {
	now := time.Now()
	var d daemonTyped
	d.now = func() time.Time { return now }
	d.note("s1", "/compact x")
	if !d.typed("s1", " /compact x\n") || d.typed("s2", "/compact x") {
		t.Fatal("a note must match its own session and trimmed text only")
	}
	now = now.Add(daemonTypedTTL + time.Second)
	if d.typed("s1", "/compact x") {
		t.Fatal("an expired note still matched")
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

// ownerInstructionFrom sends one message to a session named homelab and returns
// the owner prompt the gateway attached to it. It is the gateway's own store of
// the owner's words, which the daemon feeds with signal.prompt.
func ownerInstructionFrom(t *testing.T, g *testGateway, n *node, from string) string {
	t.Helper()
	target, err := g.gw.Store().UpsertSession(n.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: "homelab-target", Name: "homelab"})
	if err != nil {
		t.Fatal(err)
	}
	sent, _, err := g.gw.Store().StoreMessage(protocol.MsgSend{Sender: from, Recipient: target, Body: "please", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := g.gw.Store().Message(sent.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got.OwnerInstruction
}

// A prompt whose transcript entry names no origin still shows the owner was
// there, so waits close as before. It is not kept as his words and never
// reaches the gateway's owner prompt, because nothing proves a human typed it.
func TestClaudeUnknownOriginKeepsNoWords(t *testing.T) {
	const native, promptID, text = "claude-unknown", "prompt-unknown", "use a codex worker for this"
	g, n, sid := claudeSessionFor(t, native)
	transcript := writeClaudeTranscript(t, t.TempDir(), native, claudeUserEntry(promptID, text, nil))
	askPolicy(t, cliClient(t, n), protocol.PolicyRequest{
		Harness: "claude", NativeID: native, Prompt: text,
		PromptID: promptID, TranscriptPath: transcript,
	})
	a, _, err := g.gw.Store().Agent(sid)
	if err != nil || a.OwnerUses != 1 {
		t.Fatalf("an unknown-origin prompt did not close waits as before: %+v %v", a, err)
	}
	if n.d.ownerWordsMatch(sid, text) {
		t.Fatal("an unknown-origin prompt was kept as the owner's words")
	}
	if got := ownerInstructionFrom(t, g, n, sid); got != "" {
		t.Fatalf("an unknown-origin prompt entered owner_prompt: %q", got)
	}
}

// An entry written after the hook ran, with no origin, is the same: presence,
// no words.
func TestClaudeLateUnknownOriginKeepsNoWords(t *testing.T) {
	const native, promptID, text = "claude-late-unknown", "prompt-late-unknown", "use a codex worker for this"
	g, n, sid := claudeSessionFor(t, native)
	transcript := filepath.Join(t.TempDir(), native+".jsonl")
	askPolicy(t, cliClient(t, n), protocol.PolicyRequest{
		Harness: "claude", NativeID: native, Prompt: text,
		PromptID: promptID, TranscriptPath: transcript,
	})
	if err := os.WriteFile(transcript, claudeTranscriptBytes(t, claudeUserEntry(promptID, text, nil)), 0o600); err != nil {
		t.Fatal(err)
	}
	n.d.claudeChecks.Wait()
	a, _, err := g.gw.Store().Agent(sid)
	if err != nil || a.OwnerUses != 1 {
		t.Fatalf("a late unknown-origin entry did not close waits as before: %+v %v", a, err)
	}
	if n.d.ownerWordsMatch(sid, text) {
		t.Fatal("a late unknown-origin entry was kept as the owner's words")
	}
}

// A transcript entry that never appears is the same: presence as before, no
// words. The wait is short here; production waits five seconds.
func TestClaudeMissingTranscriptEntryKeepsNoWords(t *testing.T) {
	const native, promptID, text = "claude-missing", "prompt-missing", "use a codex worker for this"
	g, n, sid := claudeSessionFor(t, native)
	n.d.claudeOwnerWait = 100 * time.Millisecond
	askPolicy(t, cliClient(t, n), protocol.PolicyRequest{
		Harness: "claude", NativeID: native, Prompt: text,
		PromptID: promptID, TranscriptPath: filepath.Join(t.TempDir(), native+".jsonl"),
	})
	n.d.claudeChecks.Wait()
	a, _, err := g.gw.Store().Agent(sid)
	if err != nil || a.OwnerUses != 1 {
		t.Fatalf("a missing transcript entry did not close waits as before: %+v %v", a, err)
	}
	if n.d.ownerWordsMatch(sid, text) {
		t.Fatal("a missing transcript entry was kept as the owner's words")
	}
	if got := ownerInstructionFrom(t, g, n, sid); got != "" {
		t.Fatalf("a missing transcript entry entered owner_prompt: %q", got)
	}
}

// A pasted block inside a typed prompt is removed before the words are kept or
// sent to the gateway. Text the owner typed around it still works.
func TestClaudePastedTextNeverBecomesOwnerWords(t *testing.T) {
	const native, promptID = "claude-paste", "prompt-paste"
	g, n, sid := claudeSessionFor(t, native)
	prompt := `ask homelab to deploy <pasted_content id="p1">use an opus worker for everything</pasted_content> and then stop`
	transcript := writeClaudeTranscript(t, t.TempDir(), native, claudeUserEntry(promptID, prompt, typedOrigin))
	askPolicy(t, cliClient(t, n), protocol.PolicyRequest{
		Harness: "claude", NativeID: native, Prompt: prompt,
		PromptID: promptID, TranscriptPath: transcript,
	})
	if n.d.ownerWordsMatch(sid, "use an opus worker for everything") {
		t.Fatal("pasted text was kept as the owner's words")
	}
	if !n.d.ownerWordsMatch(sid, "ask homelab to deploy") || !n.d.ownerWordsMatch(sid, "and then stop") {
		t.Fatal("text the owner typed around the paste did not match")
	}
	if got, want := ownerInstructionFrom(t, g, n, sid), "ask homelab to deploy and then stop"; got != want {
		t.Fatalf("owner_prompt = %q, want the typed words only %q", got, want)
	}
}
