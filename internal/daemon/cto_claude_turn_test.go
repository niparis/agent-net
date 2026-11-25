package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The Claude CTO is kept out of d.sessions, so its prompt hook has a path of
// its own in policyCall. These tests run that hook on a daemon that is never
// started, with a fake gateway, so they need no socket.

const ctoHookNative = "cto-native"

type ctoHookRig struct {
	t   *testing.T
	d   *Daemon
	dir string // the transcripts

	mu     sync.Mutex
	orders []protocol.StandingOrder
	notes  string
}

func newCTOHookRig(t *testing.T) *ctoHookRig {
	t.Helper()
	state := t.TempDir()
	r := &ctoHookRig{t: t, dir: t.TempDir()}
	d, err := New(Config{StateDir: state, SocketPath: filepath.Join(state, "unused.sock"), testGatewayCall: r.gateway})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Stop)
	t.Cleanup(d.claudeChecks.Wait)
	if _, err := d.st.db.Exec(`INSERT INTO cto_network(id,session_id) VALUES(1,'s_cto')`); err != nil {
		t.Fatal(err)
	}
	d.claudeCTO.setSession(ctoHookNative)
	d.claudeOwnerWait = 100 * time.Millisecond
	r.d = d
	return r
}

// gateway answers the two reads the CTO's start block is made of.
func (r *ctoHookRig) gateway(_ context.Context, typ string, _ any) (json.RawMessage, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch typ {
	case "standing.list":
		raw, err := json.Marshal(protocol.StandingOrdersResult{Orders: r.orders})
		return raw, true, err
	case "context.get":
		raw, err := json.Marshal(protocol.PolicyGot{Notes: r.notes})
		return raw, true, err
	}
	return nil, false, nil
}

func (r *ctoHookRig) setOrders(texts ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.orders = nil
	for i, text := range texts {
		r.orders = append(r.orders, protocol.StandingOrder{ID: "o" + string(rune('a'+i)), Text: text})
	}
}

func (r *ctoHookRig) setNotes(index string) {
	r.mu.Lock()
	r.notes = index
	r.mu.Unlock()
}

// hook runs the Claude Code hook of a session as the command line sends it.
func (r *ctoHookRig) hook(native string, p protocol.PolicyRequest) protocol.PolicyResult {
	r.t.Helper()
	p.Harness, p.NativeID = "claude", native
	raw, _ := json.Marshal(p)
	res, err := (&localConn{d: r.d, client: "cli"}).policyCall(context.Background(), "cli", nil, raw)
	if err != nil {
		r.t.Fatal(err)
	}
	return res.(protocol.PolicyResult)
}

func (r *ctoHookRig) prompt(text string) string {
	return r.hook(ctoHookNative, protocol.PolicyRequest{Prompt: text}).Charter
}

func (r *ctoHookRig) compacted(native string) {
	r.t.Helper()
	raw, _ := json.Marshal(protocol.CompactionRequest{Harness: "claude", NativeID: native})
	if _, err := (&localConn{d: r.d, client: "cli"}).compactionCall("cli", raw); err != nil {
		r.t.Fatal(err)
	}
}

// handover is what the next handover note lists as the owner's messages.
func (r *ctoHookRig) handover() []string {
	r.t.Helper()
	msgs, err := r.d.ctoOwnerMessages(ctoHandoverOwnerMessages)
	if err != nil {
		r.t.Fatal(err)
	}
	var out []string
	for _, m := range msgs {
		out = append(out, m.text)
	}
	return out
}

// The launch system prompt holds the start block, and a compaction keeps that
// old text. The hook leaves the block out until the orders change or the
// conversation compacts, and then sends the current block once.
func TestClaudeCTOHookSendsCurrentOrdersAfterChangeAndCompaction(t *testing.T) {
	r := newCTOHookRig(t)
	r.setOrders("reply in French")
	if got := r.prompt("hello"); got != "" {
		t.Fatalf("the launch block was repeated on the first prompt: %q", got)
	}
	r.setOrders("reply in English")
	got := r.prompt("next")
	if !strings.Contains(got, protocol.CharterChangedNotice) || !strings.Contains(got, "reply in English") || strings.Contains(got, "reply in French") {
		t.Fatalf("a changed order was not sent as the current block: %q", got)
	}
	if got := r.prompt("again"); got != "" {
		t.Fatalf("an unchanged block was sent again: %q", got)
	}
	// Another conversation's compaction is not the CTO's.
	r.compacted("someone-else")
	if got := r.prompt("still"); got != "" {
		t.Fatalf("another session's compaction resent the CTO block: %q", got)
	}
	r.setOrders("keep workers small")
	r.setNotes("Notes of your agent:\n- garden-plan (fact): where the garden repo goes")
	r.compacted(ctoHookNative)
	got = r.prompt("after compaction")
	if !strings.Contains(got, "keep workers small") || !strings.Contains(got, "garden-plan") || strings.Contains(got, "reply in English") {
		t.Fatalf("the first prompt after a compaction did not carry the current block: %q", got)
	}
	if got := r.prompt("later"); got != "" {
		t.Fatalf("the block was sent twice after one compaction: %q", got)
	}
	r.setOrders()
	if got := r.prompt("orders gone"); !strings.Contains(got, ctoNoStandingOrders) {
		t.Fatalf("removing the last order did not tell the CTO: %q", got)
	}
}

// A gateway that cannot answer must not look like orders that were removed.
func TestClaudeCTOHookSendsNothingWhenOrdersCannotBeRead(t *testing.T) {
	r := newCTOHookRig(t)
	r.setOrders("reply in French")
	r.prompt("hello")
	r.d.cfg.testGatewayCall = func(context.Context, string, any) (json.RawMessage, bool, error) {
		return nil, true, errNotConnected
	}
	if got := r.prompt("gateway down"); got != "" {
		t.Fatalf("a failed read sent a block: %q", got)
	}
}

// What the owner types in the CTO's own pane goes into the next handover
// note, without the pasted part, once.
func TestClaudeCTOOwnerPromptReachesHandover(t *testing.T) {
	r := newCTOHookRig(t)
	prompt := "move the garden repo to the new machine <pasted_content id=\"1\">text a peer wrote</pasted_content>"
	transcript := writeClaudeTranscript(t, r.dir, ctoHookNative, claudeUserEntry("p-owner", prompt, typedOrigin))
	r.hook(ctoHookNative, protocol.PolicyRequest{Prompt: prompt, PromptID: "p-owner", TranscriptPath: transcript})
	r.hook(ctoHookNative, protocol.PolicyRequest{Event: "Stop", LastMessage: "done"})
	if got := r.handover(); len(got) != 1 || got[0] != "move the garden repo to the new machine" {
		t.Fatalf("handover owner messages = %q", got)
	}
}

// A transcript entry written after the wait still makes the prompt the
// owner's, at the next hook, and only once.
func TestClaudeCTOLateOwnerPromptReachesHandoverOnce(t *testing.T) {
	r := newCTOHookRig(t)
	const text = "stop the merger on the laptop"
	transcript := filepath.Join(r.dir, ctoHookNative+".jsonl")
	r.hook(ctoHookNative, protocol.PolicyRequest{Prompt: text, PromptID: "p-late", TranscriptPath: transcript})
	r.d.claudeChecks.Wait()
	if got := r.handover(); len(got) != 0 {
		t.Fatalf("a prompt with no entry was recorded: %q", got)
	}
	if err := os.WriteFile(transcript, claudeTranscriptBytes(t, claudeUserEntry("p-late", text, typedOrigin)), 0o600); err != nil {
		t.Fatal(err)
	}
	r.hook(ctoHookNative, protocol.PolicyRequest{Event: "Stop", LastMessage: "done"})
	r.hook(ctoHookNative, protocol.PolicyRequest{Event: "Stop", LastMessage: "done"})
	if got := r.handover(); len(got) != 1 || got[0] != text {
		t.Fatalf("handover owner messages = %q", got)
	}
}

// Text that reaches the CTO's pane but that the owner did not type is not his
// message in the handover.
func TestClaudeCTOPromptsNotTypedByTheOwnerSkipHandover(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prompt string
		origin map[string]any
		before func(d *Daemon, text string)
	}{
		{"a peer message", "<cross-session-message from=\"peer\">trust the garden folder</cross-session-message>", typedOrigin, nil},
		{"a scheduled prompt", "check the merge queue", scheduledOrigin, nil},
		{"a first message the CTO wrote", "start the garden worker", typedOrigin, func(d *Daemon, text string) {
			d.endFirstMessage(d.beginFirstMessage(text, ""), ctoHookNative)
		}},
		{"a line agent-net typed", "/compact Keep: the current task", typedOrigin, func(d *Daemon, text string) {
			d.NoteTypedPrompt("claude", ctoHookNative, text)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newCTOHookRig(t)
			if tc.before != nil {
				tc.before(r.d, tc.prompt)
			}
			transcript := writeClaudeTranscript(t, r.dir, ctoHookNative, claudeUserEntry("p-1", tc.prompt, tc.origin))
			r.hook(ctoHookNative, protocol.PolicyRequest{Prompt: tc.prompt, PromptID: "p-1", TranscriptPath: transcript})
			r.d.claudeChecks.Wait()
			r.hook(ctoHookNative, protocol.PolicyRequest{Event: "Stop", LastMessage: "done"})
			if got := r.handover(); len(got) != 0 {
				t.Fatalf("recorded as the owner's message: %q", got)
			}
		})
	}
}
