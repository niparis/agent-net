package daemon

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// Enter on a session the owner started in a terminal offers the move. It never
// opens a second `claude --resume` client next to the first.
func TestEnterHandStartedClaudeOffersMoveToBackground(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "claude")
	n, _, id := enterNode(t, g, "claude", protocol.SessionInfo{NativeID: "session-123", PID: 1, Name: "garden"}, bins, nil)
	p := enterOf(t, n, id, "")
	if !p.MoveToBackground || len(p.Argv) != 0 || p.OpenedHerdr {
		t.Fatalf("plan %+v", p)
	}
}

// The move ends the terminal client and resumes the conversation as a
// background job. The agent-net session ID stays, also when Claude gives the
// resumed conversation a new native ID.
func TestMoveHandStartedClaudeToBackground(t *testing.T) {
	t.Setenv("FAKE_CLAUDE_NEW_RESUME_ID", "1")
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	c.register(t, repo)
	native := "44444444-0000-4000-8000-000000000000"
	pid, _ := startSleeper(t)
	seedClaudeSessionFileKind(t, c.sessions, pid, native, repo, "idle", "interactive")
	var id string
	eventually(t, "hand-started session registered", func() bool {
		for _, s := range c.d.Sessions() {
			if s.NativeID == native {
				id = s.SessionID
			}
		}
		return id != ""
	})
	t.Cleanup(func() { killFakeClaude(t, repo) })
	var r sessionStartTestResult
	mustJSON(t, c.cli.mustCall("session_resume", protocol.SessionResumeRequest{SessionID: id, MoveToBackground: true}), &r)
	eventually(t, "moved to the background", func() bool {
		mustJSON(t, c.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
		if r.Status == "failed" {
			t.Fatalf("move: %+v", r)
		}
		return r.Status == "started"
	})
	if _, live := procToken(pid); live {
		t.Fatal("the terminal client is still running")
	}
	rec := readClaude(t, repo)
	if argValue(rec.Args, "--resume") != native || rec.Args[0] != "--bg" {
		t.Fatalf("resume record %+v", rec)
	}
	if r.SessionID != id || r.NativeID == native {
		t.Fatalf("the session ID must stay while the native ID moves: %+v (was %s)", r, id)
	}
}

// A session that already runs in the background has nothing to move.
func TestMoveBackgroundClaudeSessionRefused(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	c.register(t, repo)
	native := "55555555-0000-4000-8000-000000000000"
	seedClaudeSessionFileKind(t, c.sessions, fakeClaudeProcess(t), native, repo, "idle", "bg")
	var id string
	eventually(t, "session registered", func() bool {
		for _, s := range c.d.Sessions() {
			if s.NativeID == native {
				id = s.SessionID
			}
		}
		return id != ""
	})
	if _, err := c.cli.call("session_resume", protocol.SessionResumeRequest{SessionID: id, MoveToBackground: true}); err == nil {
		t.Fatal("a background session was moved")
	}
}
