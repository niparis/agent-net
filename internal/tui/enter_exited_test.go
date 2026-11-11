package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
)

// The owner quits a Claude background session with /exit. The process and the
// session file stay, so discovery still lists it with status exited. The
// gateway records that row offline, and Enter on it must take the offline
// resume path instead of showing the manual "claude --resume" note. The row
// comes from a real gateway store, so this test breaks if that rule regresses.
func TestEnterResumesAnExitedClaudeSession(t *testing.T) {
	st, err := gateway.OpenStore(filepath.Join(t.TempDir(), "gw.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	node, _, err := st.CreateNode("h1", "linux")
	if err != nil {
		t.Fatal(err)
	}
	native := "22222222-0000-4000-8000-000000000000"
	out, err := st.SyncSessions(node, []protocol.SessionInfo{
		{Harness: "claude", NativeID: native, Status: "exited", Cwd: "/work/orchestrator", Name: "orchestrator"},
	})
	if err != nil || len(out) != 1 {
		t.Fatalf("sync: %d sessions, %v", len(out), err)
	}
	row, ok, err := st.Agent(out[0].SessionID)
	if err != nil || !ok {
		t.Fatalf("the exited row is gone: ok %v err %v", ok, err)
	}
	row.LastSeen = t0.Unix() // pin the clock; online and status come from the gateway

	m, src := workerModel(120, 34)
	src.snap.Agents = append(src.snap.Agents, row)
	m = refresh(m, src)
	m.showOffline = true
	m = selectID(m, row.SessionID)
	// The old path reached session_enter, whose answer is this note.
	src.enterPlan = protocol.EnterPlan{Note: "orchestrator exited; on Mac run: claude --resume " + native}
	src.sessionStart = startedSession("orchestrator", "claude")
	src.sessionStart.SessionID = row.SessionID

	m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter gave no command")
	}
	m, cmd = press2(m, cmd())
	if len(src.sessionResumes) != 1 || src.sessionResumes[0].SessionID != row.SessionID {
		t.Fatalf("Enter did not resume the exited row: resumes %+v enters %+v", src.sessionResumes, src.enters)
	}
	if cmd == nil {
		t.Fatal("the resumed session did not open")
	}
	if _, _ = press2(m, cmd()); len(src.enters) != 1 || src.enters[0].SessionID != row.SessionID {
		t.Fatalf("the resumed session was not entered: %+v", src.enters)
	}
	if strings.Contains(m.testView(), "claude --resume") {
		t.Fatalf("Enter showed the manual resume note instead of resuming:\n%s", m.testView())
	}
}
