package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// heldState is a daemon.db for the hold-matching tests.
func heldState(t *testing.T) *state {
	t.Helper()
	st, err := openState(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.close() })
	return st
}

// heldTestFrame records one delivered frame the way prepareFrame does and
// returns the content hash a release writes back.
func heldTestFrame(t *testing.T, st *state, id, body, sock string, at time.Time, typ string) string {
	t.Helper()
	content := "<cross-session-message from=\"uds:" + sock + "\" from-name=\"bob\">\n" + body +
		"\n\n[agent-net] message " + id + " from bob.\n</cross-session-message>"
	sum := sha256.Sum256([]byte(content))
	hash := hex.EncodeToString(sum[:])
	d := ClaudeDelivery{MessageID: id, Recipient: "s_claude", Sender: "s_bob", ConversationID: "c_1", Type: typ,
		DeliveredAt: at, ExpiresAt: at.Add(time.Hour), Frame: id, ReplySock: sock,
		Preview: previewOf(body), BodyLines: linesOf(body), BodyChars: charsOf(body), FrameHash: hash}
	if err := st.SaveDelivery(d); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveFrame(d); err != nil {
		t.Fatal(err)
	}
	return hash
}

func previewOf(body string) string {
	for i := 0; i < len(body); i++ {
		if body[i] == '\n' {
			return body[:i]
		}
	}
	return body
}

func linesOf(body string) int {
	n := 1
	for i := 0; i < len(body); i++ {
		if body[i] == '\n' {
			n++
		}
	}
	return n
}

func charsOf(body string) int {
	n := 0
	for range body {
		n++
	}
	return n
}

func heldAt(t *testing.T, st *state, id string) int64 {
	t.Helper()
	var held int64
	if err := st.db.QueryRow(`SELECT held_at FROM claude_deliveries WHERE message_id=?`, id).Scan(&held); err != nil {
		t.Fatal(err)
	}
	return held
}

// A notice is applied once, keyed by its own id. After a restart it must not
// mark another delivery that arrived later.
func TestHeldNoticeIsAppliedOnce(t *testing.T) {
	st := heldState(t)
	t0 := time.Now()
	sock := "/tmp/an/r/s_bob.c_1.sock"
	heldTestFrame(t, st, "m_first", "first body", sock, t0, protocol.TypeRequest)
	entry := ClaudeHeldEntry{UUID: "entry-1", ReplySock: sock, Preview: "first body", BodyLines: 1, BodyChars: len("first body"), At: t0.Add(time.Second)}

	r, ok, handled, err := st.markClaudeHeld("s_claude", entry)
	if err != nil || handled || !ok || r.MessageID != "m_first" {
		t.Fatalf("first notice = %+v ok=%v handled=%v err=%v", r, ok, handled, err)
	}
	heldTestFrame(t, st, "m_later", "later body", sock, t0.Add(time.Minute), protocol.TypeRequest)
	if _, ok, handled, err := st.markClaudeHeld("s_claude", entry); err != nil || !handled || ok {
		t.Fatalf("repeated notice = ok=%v handled=%v err=%v", ok, handled, err)
	}
	if heldAt(t, st, "m_later") != 0 {
		t.Fatal("the repeated notice marked the later delivery")
	}
}

// A notice older than a delivery does not describe it, even when the first
// body line is the same.
func TestHeldNoticeNeverMatchesALaterDelivery(t *testing.T) {
	st := heldState(t)
	t0 := time.Now()
	sock := "/tmp/an/r/s_bob.c_1.sock"
	heldTestFrame(t, st, "m_first", "same first", sock, t0, protocol.TypeRequest)
	entry := ClaudeHeldEntry{UUID: "entry-1", ReplySock: sock, Preview: "same first", BodyLines: 1, BodyChars: 10, At: t0.Add(-time.Minute)}
	if _, ok, _, err := st.markClaudeHeld("s_claude", entry); err != nil || ok {
		t.Fatalf("an older notice matched: ok=%v err=%v", ok, err)
	}
	if _, ok, handled, err := st.markClaudeHeld("s_claude", entry); err != nil || ok || !handled {
		t.Fatalf("the refused notice was not kept: ok=%v handled=%v err=%v", ok, handled, err)
	}
	if heldAt(t, st, "m_first") != 0 {
		t.Fatal("the older notice marked the delivery")
	}
}

// Two frames with the same first line are told apart by the notice's line and
// character counts. Without a difference, nothing is marked.
func TestHeldNoticeMarksOnlyOneCertainFrame(t *testing.T) {
	st := heldState(t)
	t0 := time.Now()
	sock := "/tmp/an/r/s_bob.c_1.sock"
	heldTestFrame(t, st, "m_short", "same first line\na", sock, t0, protocol.TypeRequest)
	heldTestFrame(t, st, "m_long", "same first line\nlonger body", sock, t0.Add(10*time.Second), protocol.TypeRequest)

	ambiguous := ClaudeHeldEntry{UUID: "entry-1", ReplySock: sock, Preview: "same first line", At: t0.Add(20 * time.Second)}
	if _, ok, _, err := st.markClaudeHeld("s_claude", ambiguous); err != nil || ok {
		t.Fatalf("an ambiguous notice marked a message: ok=%v err=%v", ok, err)
	}
	short := ClaudeHeldEntry{UUID: "entry-2", ReplySock: sock, Preview: "same first line",
		BodyLines: 2, BodyChars: len("same first line\na"), At: t0.Add(20 * time.Second)}
	r, ok, _, err := st.markClaudeHeld("s_claude", short)
	if err != nil || !ok || r.MessageID != "m_short" {
		t.Fatalf("counted notice = %+v ok=%v err=%v", r, ok, err)
	}
	if heldAt(t, st, "m_long") != 0 {
		t.Fatal("the counted notice marked the other frame")
	}
}

// A release is tied to the frame the session wrote back, never to a message ID
// quoted in a body or in agent-net's notes.
func TestReleaseIsTiedToTheFrameOnly(t *testing.T) {
	st := heldState(t)
	t0 := time.Now()
	sockA, sockB := "/tmp/an/r/s_bob.c_a.sock", "/tmp/an/r/s_bob.c_b.sock"
	hashA := heldTestFrame(t, st, "m_a", "held body", sockA, t0, protocol.TypeRequest)
	hashB := heldTestFrame(t, st, "m_b", "body that names m_a", sockB, t0.Add(time.Second), protocol.TypeRequest)
	if _, ok, _, err := st.markClaudeHeld("s_claude", ClaudeHeldEntry{UUID: "entry-a", ReplySock: sockA, Preview: "held body", At: t0.Add(2 * time.Second)}); err != nil || !ok {
		t.Fatalf("hold a: ok=%v err=%v", ok, err)
	}
	ids, err := st.releaseClaudeHeld("s_claude", sockB, hashB, t0.Add(3*time.Second))
	if err != nil || len(ids) != 0 {
		t.Fatalf("release of another frame = %v, %v", ids, err)
	}
	if heldAt(t, st, "m_a") == 0 {
		t.Fatal("the other frame's release released m_a")
	}
	ids, err = st.releaseClaudeHeld("s_claude", sockA, hashA, t0.Add(4*time.Second))
	if err != nil || len(ids) != 1 || ids[0] != "m_a" {
		t.Fatalf("release of the frame = %v, %v", ids, err)
	}
}

// A batch is one frame for several messages. The hold and the release name the
// carrier, never one original.
func TestHeldBatchNamesTheCarrier(t *testing.T) {
	st := heldState(t)
	t0 := time.Now()
	sock := "/tmp/an/r/s_bob.c_1.sock"
	for _, id := range []string{"m_one", "m_two"} {
		if err := st.SaveDelivery(ClaudeDelivery{MessageID: id, Recipient: "s_claude", Sender: "s_bob",
			ConversationID: "c_1", Type: protocol.TypeRequest, DeliveredAt: t0, ExpiresAt: t0.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	body := "2 messages arrived while you were busy. Act on the newest; read the older ones as context.\n\n--- 1 of 2: message m_one from bob ---\nx"
	content := "<cross-session-message from=\"uds:" + sock + "\">\n" + body + "\n\n[agent-net] batch of 2 held messages, newest from bob.\n</cross-session-message>"
	sum := sha256.Sum256([]byte(content))
	hash := hex.EncodeToString(sum[:])
	carrier := ClaudeDelivery{MessageID: "m_batch", Recipient: "s_claude", Sender: "s_bob", ConversationID: "c_1",
		Type: "batch", DeliveredAt: t0, ExpiresAt: t0.Add(time.Hour), Frame: "m_batch", ReplySock: sock,
		Preview: previewOf(body), BodyLines: linesOf(body), BodyChars: charsOf(body), FrameHash: hash}
	if err := st.SaveFrame(carrier); err != nil {
		t.Fatal(err)
	}
	entry := ClaudeHeldEntry{UUID: "entry-batch", ReplySock: sock, Preview: previewOf(body), At: t0.Add(time.Second)}
	r, ok, _, err := st.markClaudeHeld("s_claude", entry)
	if err != nil || !ok || r.MessageID != "m_batch" {
		t.Fatalf("batch hold = %+v ok=%v err=%v", r, ok, err)
	}
	for _, id := range []string{"m_one", "m_two"} {
		if heldAt(t, st, id) != 0 {
			t.Fatalf("the batch hold marked original %s", id)
		}
	}
	ids, err := st.releaseClaudeHeld("s_claude", sock, hash, t0.Add(2*time.Second))
	if err != nil || len(ids) != 1 || ids[0] != "m_batch" {
		t.Fatalf("batch release = %v, %v", ids, err)
	}
}

// A notice agent-net sent itself has an empty body and its text in the
// details. Its first line is the preview, so the notice is still tied to its
// delivery.
func TestHeldNoticeFromAgentNetMatches(t *testing.T) {
	st := heldState(t)
	t0 := time.Now()
	sock := "/tmp/an/r/agent-net.c_1.sock"
	text := "[agent-net] You still owe bob an answer to m_0123456789abcdef."
	heldTestFrame(t, st, "m_notice", text, sock, t0, protocol.TypeNotification)
	entry := ClaudeHeldEntry{UUID: "entry-notice", ReplySock: sock, Preview: text, BodyLines: 1, BodyChars: len(text), At: t0.Add(time.Second)}
	r, ok, _, err := st.markClaudeHeld("s_claude", entry)
	if err != nil || !ok || r.MessageID != "m_notice" {
		t.Fatalf("notice hold = %+v ok=%v err=%v", r, ok, err)
	}
}
