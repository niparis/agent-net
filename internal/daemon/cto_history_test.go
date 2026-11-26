package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// ctoHistoryDaemon is a daemon with only its state, for the save and the merge
// of the CTO's timeline.
func ctoHistoryDaemon(t *testing.T) *Daemon {
	t.Helper()
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.close() })
	return &Daemon{st: st}
}

func ctoTextMessage(text string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"type":"user","text":%q}`, text))
}

// saveCTOConversationText stores conversation as the harness's current one and
// saves one message of it.
func saveCTOConversationText(t *testing.T, d *Daemon, harness, conversation, text string) {
	t.Helper()
	if err := d.setCTOConversation(harness, conversation); err != nil {
		t.Fatal(err)
	}
	if err := d.saveCTOHistory(harness, protocol.CTOEvents{Messages: []json.RawMessage{ctoTextMessage(text)}}); err != nil {
		t.Fatal(err)
	}
}

// A save replaces the saved rows of the conversation it saves, and keeps the
// rows of the harness's earlier conversations.
func TestCTOHistorySaveReplacesOnlyItsConversation(t *testing.T) {
	d := ctoHistoryDaemon(t)
	saveCTOConversationText(t, d, "codex", "thread-old", "words of the old thread")
	saveCTOConversationText(t, d, "codex", "thread-new", "first save of the new thread")
	saveCTOConversationText(t, d, "codex", "thread-new", "second save of the new thread")
	out, err := d.mergeCTOHistory(protocol.CTOEvents{}, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out.Messages)
	for text, want := range map[string]int{"words of the old thread": 1, "first save of the new thread": 0, "second save of the new thread": 1} {
		if got := strings.Count(string(raw), text); got != want {
			t.Errorf("%q is in the timeline %d times, want %d: %s", text, got, want, raw)
		}
	}
}

// The timeline of the active harness shows the saved rows of its earlier
// conversations, and leaves out the saved copy of the conversation it reads
// live, so no message shows twice.
func TestCTOHistoryMergeShowsEarlierConversationsOfTheActiveHarness(t *testing.T) {
	d := ctoHistoryDaemon(t)
	saveCTOConversationText(t, d, "codex", "thread-old", "words of the old thread")
	saveCTOConversationText(t, d, "codex", "thread-new", "words of the live thread")
	live := protocol.CTOEvents{Messages: []json.RawMessage{ctoTextMessage("words of the live thread")}, Actions: []protocol.CTOAction{}}
	out, err := d.mergeCTOHistory(live, "codex", 0)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out.Messages)
	for _, text := range []string{"words of the old thread", "words of the live thread"} {
		if got := strings.Count(string(raw), text); got != 1 {
			t.Errorf("%q is in the timeline %d times, want once: %s", text, got, raw)
		}
	}
}

// ctoTimelineShows waits until the CTO timeline holds every text.
func ctoTimelineShows(t *testing.T, cli *localClient, texts ...string) {
	t.Helper()
	eventually(t, fmt.Sprintf("the CTO timeline shows %q", texts), func() bool {
		raw := string(cli.mustCall(protocol.OpCTOEvents, map[string]int{"index": 0}))
		for _, text := range texts {
			if !strings.Contains(raw, text) {
				return false
			}
		}
		return true
	})
}

// stopCodexCTOQuietly ends the Codex app server the way an idle stop or a
// crash does, not the owner's stop.
func stopCodexCTOQuietly(n *node) {
	n.d.stopCodexCTO("idle")
}

// cto_new starts a new conversation, and the messages of the one before stay in
// the timeline.
func TestCTONewKeepsTheEarlierConversationInTheTimeline(t *testing.T) {
	n, _ := startCodexCTONode(t)
	cli := cliClient(t, n)
	setCTOHarness(t, cli, "codex", "gpt-fake")
	first := ctoSendID(t, cli, "before the new conversation")
	ctoTimelineShows(t, cli, "before the new conversation")
	cli.mustCall(protocol.OpCTONew, nil)
	if second := ctoSendID(t, cli, "after the new conversation"); second == first {
		t.Fatalf("cto_new kept thread %s", first)
	}
	ctoTimelineShows(t, cli, "before the new conversation", "after the new conversation")
}

// A switch back to a harness after a day drops its old conversation and starts
// a new one. The old one stays in the timeline, also after the next switch
// saves the new one.
func TestCTOConversationDroppedAfterADayStaysInTheTimeline(t *testing.T) {
	n, _ := startCodexCTONode(t)
	cli := cliClient(t, n)
	setCTOHarness(t, cli, "codex", "gpt-fake")
	first := ctoSendID(t, cli, "said a day ago")
	ctoTimelineShows(t, cli, "said a day ago")
	setCTOHarness(t, cli, "opencode", "fake/m1")
	waitHandoverSettled(t, n.d)
	old := n.d.ctoNowTime().Add(-ctoConversationFresh - 1e9).UnixNano()
	if _, err := n.d.st.db.Exec(`UPDATE session_conversations SET updated_at=? WHERE harness='codex'`, old); err != nil {
		t.Fatal(err)
	}
	setCTOHarness(t, cli, "codex", "gpt-fake")
	if second := ctoSendID(t, cli, "said today"); second == first {
		t.Fatalf("the switch after a day resumed thread %s", first)
	}
	ctoTimelineShows(t, cli, "said a day ago", "said today")
	setCTOHarness(t, cli, "opencode", "fake/m1")
	waitHandoverSettled(t, n.d)
	ctoTimelineShows(t, cli, "said a day ago", "said today")
}

// codexFallbackNode is a Codex CTO node whose role gate answers pick once
// fallback is set.
func codexFallbackNode(t *testing.T, pick protocol.RolePickResult) (*node, *localClient, *atomic.Bool) {
	t.Helper()
	fallback := &atomic.Bool{}
	n, _ := startCodexCTONode(t, func(c *Config) {
		c.testGatewayCall = func(_ context.Context, typ string, _ any) (json.RawMessage, bool, error) {
			if typ != "worker.role.pick" || !fallback.Load() {
				return nil, false, nil
			}
			raw, err := json.Marshal(pick)
			return raw, true, err
		}
	})
	cli := cliClient(t, n)
	setCTOHarness(t, cli, "codex", "gpt-fake")
	return n, cli, fallback
}

// A fallback that starts a new conversation on the same harness keeps the old
// conversation in the timeline.
func TestCTOFallbackToANewConversationKeepsTheOldOne(t *testing.T) {
	n, cli, fallback := codexFallbackNode(t, protocol.RolePickResult{Harness: "codex", Provider: protocol.ProfileProvider("codex", "gpt-fake"), Model: "gpt-fake", NewConversation: true})
	first := ctoSendID(t, cli, "before the fallback")
	ctoTimelineShows(t, cli, "before the fallback")
	stopCodexCTOQuietly(n)
	fallback.Store(true)
	if second := ctoSendID(t, cli, "after the fallback"); second == first {
		t.Fatalf("the fallback kept thread %s", first)
	}
	ctoTimelineShows(t, cli, "before the fallback", "after the fallback")
}

// A fallback to another harness saves the conversation it leaves, as an owner
// switch does.
func TestCTOFallbackToAnotherHarnessKeepsTheOldConversation(t *testing.T) {
	n, cli, fallback := codexFallbackNode(t, protocol.RolePickResult{Harness: "opencode", Provider: protocol.ProfileProvider("opencode", "fake/m1"), Model: "fake/m1"})
	ctoSendID(t, cli, "codex before the fallback")
	ctoTimelineShows(t, cli, "codex before the fallback")
	stopCodexCTOQuietly(n)
	fallback.Store(true)
	ctoSendID(t, cli, "opencode after the fallback")
	ctoTimelineShows(t, cli, "codex before the fallback", "opencode after the fallback")
}
