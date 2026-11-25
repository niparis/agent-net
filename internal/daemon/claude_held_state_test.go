package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/claudeframes"
	"github.com/niparis/agent-net/internal/protocol"
)

// heldTestDetail is the detail the daemon writes when it reports a hold.
const heldTestDetail = "held by Claude Code"

// markHeld records one notice the way the daemon does, with the held detail.
func markHeld(st *state, recipient string, e ClaudeHeldEntry) (ClaudeDelivery, bool, bool, error) {
	return st.markClaudeHeld(recipient, e, heldTestDetail)
}

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

// heldFrame builds the one frame agent-net writes for a message: the reply
// socket, the body, and agent-net's own note after it.
func heldFrame(body, sock, id string) string {
	return `<cross-session-message from="uds:` + sock + `" from-name="bob">` + "\n" + body +
		"\n\n[agent-net] message " + id + " from bob.\n</cross-session-message>"
}

// heldTestFrame records one delivered frame the way prepareFrame does and
// returns its content and description, which a release and a notice name.
func heldTestFrame(t *testing.T, st *state, id, body, sock string, at time.Time, typ string) (string, claudeframes.Description) {
	t.Helper()
	content := heldFrame(body, sock, id)
	desc, ok := claudeframes.Describe(content)
	if !ok {
		t.Fatalf("frame %s has no wrapper", id)
	}
	d := ClaudeDelivery{MessageID: id, Recipient: "s_claude", Sender: "s_bob", ConversationID: "c_1", Type: typ,
		DeliveredAt: at, ExpiresAt: at.Add(time.Hour), Frame: id, ReplySock: sock,
		Preview: desc.Preview, FrameLines: desc.Lines, FrameChars: desc.Chars, FrameChars16: desc.Chars16,
		FrameHash: desc.Hash}
	if err := st.SaveDelivery(d); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveFrame(d); err != nil {
		t.Fatal(err)
	}
	return content, desc
}

// heldNotice is the entry Claude Code writes for a held frame. It is the real
// shape: the reply socket, the quoted first line, the inner frame's size, and
// the notice's own id and time.
func heldNotice(uuid, sock string, d claudeframes.Description, at time.Time) ClaudeHeldEntry {
	return ClaudeHeldEntry{UUID: uuid, ReplySock: sock, Preview: d.Preview,
		FrameLines: d.Lines, FrameChars: d.Chars, At: at}
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
	_, first := heldTestFrame(t, st, "m_first", "first body", sock, t0, protocol.TypeRequest)
	entry := heldNotice("entry-1", sock, first, t0.Add(time.Second))

	r, ok, handled, err := markHeld(st, "s_claude", entry)
	if err != nil || handled || !ok || r.MessageID != "m_first" {
		t.Fatalf("first notice = %+v ok=%v handled=%v err=%v", r, ok, handled, err)
	}
	heldTestFrame(t, st, "m_later", "later body", sock, t0.Add(time.Minute), protocol.TypeRequest)
	if _, ok, handled, err := markHeld(st, "s_claude", entry); err != nil || !handled || ok {
		t.Fatalf("repeated notice = ok=%v handled=%v err=%v", ok, handled, err)
	}
	if heldAt(t, st, "m_later") != 0 {
		t.Fatal("the repeated notice marked the later delivery")
	}
}

// A notice older than a delivery does not describe it, even when the size and
// first line are the same.
func TestHeldNoticeNeverMatchesALaterDelivery(t *testing.T) {
	st := heldState(t)
	t0 := time.Now()
	sock := "/tmp/an/r/s_bob.c_1.sock"
	_, later := heldTestFrame(t, st, "m_first", "same first", sock, t0, protocol.TypeRequest)
	entry := heldNotice("entry-1", sock, later, t0.Add(-time.Minute))
	if _, ok, _, err := markHeld(st, "s_claude", entry); err != nil || ok {
		t.Fatalf("an older notice matched: ok=%v err=%v", ok, err)
	}
	if _, ok, handled, err := markHeld(st, "s_claude", entry); err != nil || ok || !handled {
		t.Fatalf("the refused notice was not kept: ok=%v handled=%v err=%v", ok, handled, err)
	}
	if heldAt(t, st, "m_first") != 0 {
		t.Fatal("the older notice marked the delivery")
	}
}

// The notice's own line and character counts pick the frame. Without them a
// notice is not guessed, and with them only the one frame they describe is
// marked.
func TestHeldNoticeMarksOnlyOneCertainFrame(t *testing.T) {
	st := heldState(t)
	t0 := time.Now()
	sock := "/tmp/an/r/s_bob.c_1.sock"
	_, short := heldTestFrame(t, st, "m_short", "same first line\na", sock, t0, protocol.TypeRequest)
	heldTestFrame(t, st, "m_long", "same first line\nlonger body", sock, t0.Add(10*time.Second), protocol.TypeRequest)

	ambiguous := ClaudeHeldEntry{UUID: "entry-1", ReplySock: sock, Preview: "same first line", At: t0.Add(20 * time.Second)}
	if _, ok, _, err := markHeld(st, "s_claude", ambiguous); err != nil || ok {
		t.Fatalf("a notice without counts marked a message: ok=%v err=%v", ok, err)
	}
	r, ok, _, err := markHeld(st, "s_claude", heldNotice("entry-2", sock, short, t0.Add(20*time.Second)))
	if err != nil || !ok || r.MessageID != "m_short" {
		t.Fatalf("counted notice = %+v ok=%v err=%v", r, ok, err)
	}
	if heldAt(t, st, "m_long") != 0 {
		t.Fatal("the counted notice marked the other frame")
	}
}

// The preview breaks a tie between two frames of the same size. Its quote
// drops the characters Claude Code drops.
func TestHeldNoticeBreaksASizeTieWithThePreview(t *testing.T) {
	st := heldState(t)
	t0 := time.Now()
	sock := "/tmp/an/r/s_bob.c_1.sock"
	_, one := heldTestFrame(t, st, "m_one", "first (a)\nxx", sock, t0, protocol.TypeRequest)
	_, two := heldTestFrame(t, st, "m_two", "other [b]\nxx", sock, t0.Add(10*time.Second), protocol.TypeRequest)
	if one.Lines != two.Lines || one.Chars != two.Chars {
		t.Fatalf("the frames do not have the same size: %+v %+v", one, two)
	}
	entry := ClaudeHeldEntry{UUID: "entry-1", ReplySock: sock, Preview: "other b", FrameLines: two.Lines, FrameChars: two.Chars, At: t0.Add(20 * time.Second)}
	r, ok, _, err := markHeld(st, "s_claude", entry)
	if err != nil || !ok || r.MessageID != "m_two" {
		t.Fatalf("preview tie = %+v ok=%v err=%v", r, ok, err)
	}
	if heldAt(t, st, "m_one") != 0 {
		t.Fatal("the tie marked the other frame")
	}
}

// A release is tied to the frame the session wrote back, never to a message ID
// quoted in a body or in agent-net's notes.
func TestReleaseIsTiedToTheFrameOnly(t *testing.T) {
	st := heldState(t)
	t0 := time.Now()
	sockA, sockB := "/tmp/an/r/s_bob.c_a.sock", "/tmp/an/r/s_bob.c_b.sock"
	_, heldA := heldTestFrame(t, st, "m_a", "held body", sockA, t0, protocol.TypeRequest)
	contentB, heldB := heldTestFrame(t, st, "m_b", "body that names m_a", sockB, t0.Add(time.Second), protocol.TypeRequest)
	if _, ok, _, err := markHeld(st, "s_claude", heldNotice("entry-a", sockA, heldA, t0.Add(2*time.Second))); err != nil || !ok {
		t.Fatalf("hold a: ok=%v err=%v", ok, err)
	}
	ids, err := st.releaseClaudeHeld("s_claude", sockB, heldB.Hash, t0.Add(3*time.Second))
	if err != nil || len(ids) != 0 {
		t.Fatalf("release of another frame = %v, %v", ids, err)
	}
	if heldAt(t, st, "m_a") == 0 {
		t.Fatal("the other frame's release released m_a")
	}
	if contentB == "" {
		t.Fatal("frame b is empty")
	}
	ids, err = st.releaseClaudeHeld("s_claude", sockA, heldA.Hash, t0.Add(4*time.Second))
	if err != nil || len(ids) != 1 || ids[0] != "m_a" {
		t.Fatalf("release of the frame = %v, %v", ids, err)
	}
	// Claude Code writes the frame back more than once in some shapes. The
	// release is applied once.
	if ids, err = st.releaseClaudeHeld("s_claude", sockA, heldA.Hash, t0.Add(5*time.Second)); err != nil || len(ids) != 0 {
		t.Fatalf("a repeated release = %v, %v", ids, err)
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
	desc, ok := claudeframes.Describe(content)
	if !ok {
		t.Fatal("the batch frame has no wrapper")
	}
	carrier := ClaudeDelivery{MessageID: "m_batch", Recipient: "s_claude", Sender: "s_bob", ConversationID: "c_1",
		Type: "batch", DeliveredAt: t0, ExpiresAt: t0.Add(time.Hour), Frame: "m_batch", ReplySock: sock,
		Preview: desc.Preview, FrameLines: desc.Lines, FrameChars: desc.Chars, FrameChars16: desc.Chars16, FrameHash: desc.Hash}
	if err := st.SaveFrame(carrier); err != nil {
		t.Fatal(err)
	}
	r, ok, _, err := markHeld(st, "s_claude", heldNotice("entry-batch", sock, desc, t0.Add(time.Second)))
	if err != nil || !ok || r.MessageID != "m_batch" {
		t.Fatalf("batch hold = %+v ok=%v err=%v", r, ok, err)
	}
	for _, id := range []string{"m_one", "m_two"} {
		if heldAt(t, st, id) != 0 {
			t.Fatalf("the batch hold marked original %s", id)
		}
	}
	ids, err := st.releaseClaudeHeld("s_claude", sock, desc.Hash, t0.Add(2*time.Second))
	if err != nil || len(ids) != 1 || ids[0] != "m_batch" {
		t.Fatalf("batch release = %v, %v", ids, err)
	}
}

// A notice agent-net sent itself has an empty body and its text after it. Its
// first line is the preview, so the notice is still tied to its delivery.
func TestHeldNoticeFromAgentNetMatches(t *testing.T) {
	st := heldState(t)
	t0 := time.Now()
	sock := "/tmp/an/r/agent-net.c_1.sock"
	text := "[agent-net] You still owe bob an answer to m_0123456789abcdef."
	content := "<cross-session-message from=\"uds:" + sock + "\" from-name=\"agent-net\">\n\n" + text + "\n</cross-session-message>"
	desc, ok := claudeframes.Describe(content)
	if !ok {
		t.Fatal("the notice frame has no wrapper")
	}
	d := ClaudeDelivery{MessageID: "m_notice", Recipient: "s_claude", Sender: "agent-net", ConversationID: "c_1",
		Type: protocol.TypeNotification, DeliveredAt: t0, ExpiresAt: t0.Add(time.Hour), Frame: "m_notice", ReplySock: sock,
		Preview: desc.Preview, FrameLines: desc.Lines, FrameChars: desc.Chars, FrameChars16: desc.Chars16, FrameHash: desc.Hash}
	if err := st.SaveDelivery(d); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveFrame(d); err != nil {
		t.Fatal(err)
	}
	r, ok, _, err := markHeld(st, "s_claude", heldNotice("entry-notice", sock, desc, t0.Add(time.Second)))
	if err != nil || !ok || r.MessageID != "m_notice" {
		t.Fatalf("notice hold = %+v ok=%v err=%v", r, ok, err)
	}
}

// A hold or release that cannot reach the gateway is kept and sent again, in
// order, after the daemon restarts.
func TestClaudeDeliveryReportsAreKeptAcrossARestart(t *testing.T) {
	dir := t.TempDir()
	st, err := openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.enqueueClaudeReport(reportHeld, "m_1", "held"); err != nil {
		t.Fatal(err)
	}
	if err := st.enqueueClaudeReport(reportRelease, "m_1", "released"); err != nil {
		t.Fatal(err)
	}
	st.close()

	st, err = openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	rows, err := st.claudeReports()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Kind != reportHeld || rows[1].Kind != reportRelease {
		t.Fatalf("reports after a restart = %+v, want the hold then the release", rows)
	}
}
