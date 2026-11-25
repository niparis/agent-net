package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// reportDaemon is a daemon whose gateway calls go to call and whose state
// lives in dir. It is never started, so no socket is needed.
func reportDaemon(t *testing.T, dir string, call func(method string, p protocol.DeliveryUpdate) error) *Daemon {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	t.Setenv("AGENT_NET_STATE_DIR", t.TempDir())
	t.Setenv("AGENT_NET_SOCKET", filepath.Join(t.TempDir(), "d.sock"))
	d, err := New(Config{
		StateDir:          dir,
		SocketPath:        filepath.Join(t.TempDir(), "d.sock"),
		ClaudeSessionsDir: filepath.Join(t.TempDir(), "sessions"),
		testGatewayCall: func(_ context.Context, method string, params any) (json.RawMessage, bool, error) {
			p, ok := params.(protocol.DeliveryUpdate)
			if !ok {
				return nil, false, nil
			}
			if err := call(method, p); err != nil {
				return nil, true, err
			}
			return json.RawMessage(`{}`), true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// A hold and its release that cannot reach the gateway stay on disk. A later
// flush on the same daemon sends them in order.
func TestClaudeDeliveryReportsRetryInOrder(t *testing.T) {
	var attempts []string
	down := true
	d := reportDaemon(t, t.TempDir(), func(method string, p protocol.DeliveryUpdate) error {
		attempts = append(attempts, method+":"+p.MessageID)
		if down {
			return errGatewayTimeout
		}
		return nil
	})
	t.Cleanup(d.Stop)
	for _, r := range []struct{ kind, detail string }{{reportHeld, "held"}, {reportRelease, "released"}} {
		if err := d.st.enqueueClaudeReport(r.kind, "m_1", r.detail); err != nil {
			t.Fatal(err)
		}
	}
	if !d.flushClaudeReports(context.Background()) {
		t.Fatal("a failed report did not stall the flush")
	}
	if len(attempts) != 1 || attempts[0] != reportHeld+":m_1" {
		t.Fatalf("attempts after the failed hold = %v, want only the hold", attempts)
	}
	if rows, err := d.st.claudeReports(); err != nil || len(rows) != 2 {
		t.Fatalf("kept reports = %+v, %v, want both", rows, err)
	}

	down = false
	if d.flushClaudeReports(context.Background()) {
		t.Fatal("the flush stalled with the gateway reachable")
	}
	want := []string{reportHeld + ":m_1", reportHeld + ":m_1", reportRelease + ":m_1"}
	if len(attempts) != len(want) || attempts[1] != want[1] || attempts[2] != want[2] {
		t.Fatalf("attempts = %v, want the failed hold, then the hold and the release in order", attempts)
	}
	if rows, _ := d.st.claudeReports(); len(rows) != 0 {
		t.Fatalf("reports left after the flush: %+v", rows)
	}
}

// A report kept by one daemon is sent by the next one, in order.
func TestClaudeDeliveryReportsSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	first := reportDaemon(t, dir, func(string, protocol.DeliveryUpdate) error { return errGatewayTimeout })
	if err := first.st.enqueueClaudeReport(reportHeld, "m_1", "held"); err != nil {
		t.Fatal(err)
	}
	if err := first.st.enqueueClaudeReport(reportRelease, "m_1", "released"); err != nil {
		t.Fatal(err)
	}
	if !first.flushClaudeReports(context.Background()) {
		t.Fatal("a failed report did not stall the flush")
	}
	first.Stop()

	var attempts []string
	second := reportDaemon(t, dir, func(method string, p protocol.DeliveryUpdate) error {
		attempts = append(attempts, method+":"+p.MessageID)
		return nil
	})
	t.Cleanup(second.Stop)
	if second.flushClaudeReports(context.Background()) {
		t.Fatal("the restarted daemon stalled with the gateway reachable")
	}
	want := []string{reportHeld + ":m_1", reportRelease + ":m_1"}
	if len(attempts) != len(want) || attempts[0] != want[0] || attempts[1] != want[1] {
		t.Fatalf("attempts after the restart = %v, want the hold then the release", attempts)
	}
}

// A row saved before frames were described is not rewritten when the database
// opens again: that write ran at every start. Such a row simply matches no
// hold notice.
func TestOpenStateLeavesOldFrameRowsAlone(t *testing.T) {
	dir := t.TempDir()
	st, err := openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveDelivery(ClaudeDelivery{MessageID: "m_old", Recipient: "s_claude", Sender: "s_bob",
		ConversationID: "c_1", Type: protocol.TypeRequest}); err != nil {
		t.Fatal(err)
	}
	st.close()
	st, err = openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	var frame string
	if err := st.db.QueryRow(`SELECT frame FROM claude_deliveries WHERE message_id='m_old'`).Scan(&frame); err != nil {
		t.Fatal(err)
	}
	if frame != "" {
		t.Fatalf("opening the database filled the frame with %q", frame)
	}
}
