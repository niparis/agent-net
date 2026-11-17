package daemon

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// openCodeCTOConfirm is a node whose CTO runs on the fake OpenCode, with the
// delivery check on and short (decision 0171, part b).
func openCodeCTOConfirm(t *testing.T, confirm bool) (*node, *localClient, *logBuf) {
	t.Helper()
	t.Setenv("FAKE_OC_AUTH_RECORD", "1")
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	logs := &logBuf{}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, logger: log.New(logs, "", 0), cfg: func(c *Config) {
		c.HarnessPaths = map[string]string{"opencode": os.Args[0]}
		c.testNoDeliveryConfirm = !confirm
		c.deliveryConfirmWait = scaled(500 * time.Millisecond)
	}})
	if err := n.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	return n, cliClient(t, n), logs
}

// The fake answers a prompt that starts with FAKE:answer, so the turn starts.
// The check sees the answer and sends nothing again.
func TestOpenCodeCTODeliveryTakenIsNotSentAgain(t *testing.T) {
	withFakeOpenCodeRouteSets(t, testOpenCodeCTODeliveryTakenIsNotSentAgain)
}

func testOpenCodeCTODeliveryTakenIsNotSentAgain(t *testing.T) {
	n, cli, logs := openCodeCTOConfirm(t, true)
	before := n.d.deliveryReads.Load()
	cli.mustCall("cto_send", map[string]string{"text": "FAKE:answer the build question"})
	eventually(t, "the delivery confirmed", func() bool { return len(logs.lines("cto.delivery_confirmed")) == 1 })
	if lines := logs.lines("cto.delivery_retry", "cto.restart_for_delivery"); len(lines) != 0 {
		t.Fatalf("a taken message was sent again: %v", lines)
	}
	// The cost of a taken delivery: one read of the conversation, in the check.
	if got := n.d.deliveryReads.Load() - before; got != 1 {
		t.Fatalf("reads for one taken delivery: %d, want 1", got)
	}
}

// A prompt the fake does not answer is sent once more, then the server is
// restarted and resumes the conversation, which already holds the prompt.
func TestOpenCodeCTODeliveryRetriedThenRestarted(t *testing.T) {
	withFakeOpenCodeRouteSets(t, testOpenCodeCTODeliveryRetriedThenRestarted)
}

func testOpenCodeCTODeliveryRetriedThenRestarted(t *testing.T) {
	n, cli, logs := openCodeCTOConfirm(t, true)
	cli.mustCall("cto_send", map[string]string{"text": "please answer this"})
	eventually(t, "the restart", func() bool { return len(logs.lines("cto.restart_for_delivery")) == 1 })
	for _, want := range []string{"cto.delivery_retry", "cto.restart_for_delivery"} {
		if got := len(logs.lines(want)); got != 1 {
			t.Errorf("%q logged %d times, want 1:\n%s", want, got, logs.String())
		}
	}
	// The first copy of the prompt is in the conversation, so the restart resumes
	// it and the new server is not sent the prompt again.
	eventually(t, "the restart resumed the stored prompt", func() bool { return len(logs.lines("cto.delivery_resumed")) == 1 })
	rec := readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	for _, turn := range rec.Turns {
		if strings.Contains(turn, "please answer this") {
			t.Fatalf("the restarted server got the stored prompt again: %q", rec.Turns)
		}
	}
}

// While the CTO is busy the message is held, not sent again. When the turn
// ends and still no turn started for it, the check sends it once more.
func TestOpenCodeCTODeliveryHeldWhileBusy(t *testing.T) {
	withFakeOpenCodeRouteSets(t, testOpenCodeCTODeliveryHeldWhileBusy)
}

func testOpenCodeCTODeliveryHeldWhileBusy(t *testing.T) {
	n, cli, logs := openCodeCTOConfirm(t, true)
	cli.mustCall("cto_send", map[string]string{"text": "a message after a long turn"})
	n.d.cto.mu.Lock()
	session := n.d.cto.session
	n.d.cto.mu.Unlock()
	active := filepath.Join(n.cfg.StateDir, "cto", "data", "fake-active")
	if err := os.WriteFile(active, []byte(session), 0o600); err != nil {
		t.Fatal(err)
	}
	// Two checks pass while the turn runs: each reads the conversation and the
	// busy state, and neither sends the message again.
	reads := n.d.deliveryReads.Load()
	eventually(t, "two checks during the busy turn", func() bool { return n.d.deliveryReads.Load() >= reads+4 })
	if lines := logs.lines("cto.delivery_retry"); len(lines) != 0 {
		t.Fatalf("a message was sent again during a busy turn: %v", lines)
	}
	if err := os.Remove(active); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the message sent again once the turn ended", func() bool { return len(logs.lines("cto.delivery_retry")) == 1 })
}

// After a compaction the CTO's next turn carries the start block again, once.
// The first turn of a conversation carries it already.
func TestOpenCodeCTOStartBlockAgainAfterCompaction(t *testing.T) {
	withFakeOpenCodeRouteSets(t, testOpenCodeCTOStartBlockAgainAfterCompaction)
}

func testOpenCodeCTOStartBlockAgainAfterCompaction(t *testing.T) {
	n, cli, _ := openCodeCTOConfirm(t, false)
	// A handover note is in the start block, so the block is never empty.
	if _, err := n.d.st.db.Exec(`UPDATE cto SET handover_note=? WHERE id=1`, "handover-start-block"); err != nil {
		t.Fatal(err)
	}
	const marker = "Latest handover note: handover-start-block"
	turn := func(text string) {
		t.Helper()
		cli.mustCall("cto_send", map[string]string{"text": text})
	}
	turn("first")
	rec := readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	if !strings.Contains(rec.Turns[len(rec.Turns)-1], marker) {
		t.Fatalf("the first turn of a conversation lacks the start block: %q", rec.Turns)
	}
	turn("second")
	n.d.cto.mu.Lock()
	session := n.d.cto.session
	n.d.cto.mu.Unlock()
	n.d.noteCTOCompacted(session, "fill 80%")
	turn("third")
	turn("fourth")
	rec = readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	if got := len(rec.Turns); got != 4 {
		t.Fatalf("turns: %d, want 4: %q", got, rec.Turns)
	}
	// Turns two, three and four: only the one after the compaction carries it.
	for i, want := range []bool{false, true, false} {
		if got := strings.Contains(rec.Turns[i+1], marker); got != want {
			t.Fatalf("turn %d carries the start block: %v, want %v: %q", i+2, got, want, rec.Turns[i+1])
		}
	}
}

// A turn that fails before the model answers still ends with a message the
// owner did not write. It counts as taken, so the prompt is not sent again.
func TestOpenCodeCTODeliveryFailedTurnIsTaken(t *testing.T) {
	withFakeOpenCodeRouteSets(t, testOpenCodeCTODeliveryFailedTurnIsTaken)
}

func testOpenCodeCTODeliveryFailedTurnIsTaken(t *testing.T) {
	_, cli, logs := openCodeCTOConfirm(t, true)
	cli.mustCall("cto_send", map[string]string{"text": "FAKE:ctofail"})
	eventually(t, "the delivery confirmed", func() bool { return len(logs.lines("cto.delivery_confirmed")) == 1 })
	if lines := logs.lines("cto.delivery_retry", "cto.restart_for_delivery"); len(lines) != 0 {
		t.Fatalf("a failed turn was sent again: %v", lines)
	}
}

// The first copy of a prompt is in the conversation. After the restart the
// daemon resumes the conversation and does not send the prompt again.
func TestOpenCodeCTORestartResumesStoredPrompt(t *testing.T) {
	withFakeOpenCodeRouteSets(t, testOpenCodeCTORestartResumesStoredPrompt)
}

func testOpenCodeCTORestartResumesStoredPrompt(t *testing.T) {
	n, cli, logs := openCodeCTOConfirm(t, true)
	cli.mustCall("cto_send", map[string]string{"text": "please answer this"})
	eventually(t, "the restart", func() bool { return len(logs.lines("cto.restart_for_delivery")) == 1 })
	eventually(t, "the restart resumed the stored prompt", func() bool { return len(logs.lines("cto.delivery_resumed")) == 1 })
	rec := readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	for _, turn := range rec.Turns {
		if strings.Contains(turn, "please answer this") {
			t.Fatalf("the restarted server got the stored prompt again: %q", rec.Turns)
		}
	}
}

// A prompt that was never stored is sent again after the restart.
func TestOpenCodeCTORestartPromptsAgainWhenNotStored(t *testing.T) {
	withFakeOpenCodeRouteSets(t, testOpenCodeCTORestartPromptsAgainWhenNotStored)
}

func testOpenCodeCTORestartPromptsAgainWhenNotStored(t *testing.T) {
	n, cli, logs := openCodeCTOConfirm(t, true)
	cli.mustCall("cto_send", map[string]string{"text": "FAKE:drop please answer this"})
	eventually(t, "the restart", func() bool { return len(logs.lines("cto.restart_for_delivery")) == 1 })
	eventually(t, "the restarted server got the prompt", func() bool {
		rec := readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
		return len(rec.Turns) == 1 && strings.Contains(rec.Turns[0], "please answer this")
	})
	if got := len(logs.lines("cto.delivery_resumed")); got != 0 {
		t.Fatalf("a prompt that was never stored was resumed: %d", got)
	}
}
