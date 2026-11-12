package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// newWorktreeWakeRig is an offline background Claude session with no Claude
// record, whose conversation lives in a git worktree of the repo, as the
// agent-net lead's did on 2026-11-12.
func newWorktreeWakeRig(t *testing.T) (wakeRig, string) {
	t.Helper()
	r := newClaudeWakeRig(t, protocol.ModeBackground, protocol.AutonomyOff, []map[string]any{})
	worktree := filepath.Join(r.repo, ".claude", "worktrees", "front")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := r.g.gw.Store().UpsertSession(r.c.d.NodeID(), protocol.SessionInfo{Harness: "claude", NativeID: wakeNative, Cwd: worktree, Name: "shop",
		Mode: protocol.ModeBackground, Repo: protocol.RepoInfo{Root: r.repo, Project: "shop", Worktree: worktree}}); err != nil {
		t.Fatal(err)
	}
	if err := r.g.gw.Store().SetSessionOffline(r.c.d.NodeID(), r.id); err != nil {
		t.Fatal(err)
	}
	return r, worktree
}

// liveClaudeSessions is every online Claude session the gateway lists.
func liveClaudeSessions(t *testing.T, r wakeRig) []protocol.AgentView {
	t.Helper()
	var live []protocol.AgentView
	for _, a := range r.c.agents(t, false) {
		if a.Harness == "claude" && a.Online {
			live = append(live, a)
		}
	}
	return live
}

// claudeLaunches counts the Claude background sessions the fake started.
func claudeLaunches(t *testing.T, sessions string) int {
	t.Helper()
	entries, _ := os.ReadDir(sessions)
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			n++
		}
	}
	return n
}

// A wake whose resume first shows a fresh conversation ID in its session file
// must not rebind the session to that ID. The resumed conversation then
// registered as a new session and the woken one never came back (the owner
// saw "agent-net 5", s_22331c24d4914828, which he never started).
func TestWakeInWorktreeKeepsSessionIDWhenResumeSettlesLate(t *testing.T) {
	r, worktree := newWorktreeWakeRig(t)
	gate := filepath.Join(t.TempDir(), "settled")
	t.Setenv(envFakeResumeSettleGate, gate)
	r.send(t, "wake the worktree session")
	// Let the resume settle once the wake has read the early session file:
	// either it moved the record off its conversation, or it waits.
	eventually(t, "the wake reads the early session file", func() bool {
		a, ok, err := r.g.gw.Store().Agent(r.id)
		if err != nil || !ok {
			return false
		}
		return a.NativeID != wakeNative || len(r.c.logs.lines("claude start", "waiting for")) > 0
	})
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the queued message reaches the woken session", func() bool {
		return len(r.c.adapter.deliveredTo(r.id)) == 1
	})
	live := liveClaudeSessions(t, r)
	if len(live) != 1 || live[0].SessionID != r.id || live[0].NativeID != wakeNative {
		t.Fatalf("live sessions %+v, want only %s on %s", live, r.id, wakeNative)
	}
	rec := readClaude(t, worktree)
	if rec.Cwd != worktree || argValue(rec.Args, "--resume") != wakeNative {
		t.Fatalf("launch in %s with %v, want --resume %s in %s", rec.Cwd, rec.Args, wakeNative, worktree)
	}
}

// Two wakes of one session that overlap start one Claude session, and a wake
// of a session that is already live starts nothing.
func TestWakeIsSingleFlightPerSession(t *testing.T) {
	r := newClaudeWakeRig(t, protocol.ModeBackground, protocol.AutonomyOff, []map[string]any{})
	// The gate holds the first wake while its session is still starting.
	gate := filepath.Join(t.TempDir(), "settled")
	t.Setenv(envFakeResumeSettleGate, gate)
	errs := make(chan error, 2)
	go func() { errs <- r.c.d.wakeRetiredClaude(context.Background(), r.id, "") }()
	eventually(t, "the first wake has started Claude", func() bool { return claudeLaunches(t, r.c.sessions) == 1 })
	go func() { errs <- r.c.d.wakeRetiredClaude(context.Background(), r.id, "") }()
	eventually(t, "the second wake joins the first", func() bool { return len(r.c.logs.lines("session wake", "already being woken")) > 0 })
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("wake failed: %v", err)
		}
	}
	if r.c.d.sessionByID(r.id) == nil {
		t.Fatal("the woken session is not live")
	}
	if err := r.c.d.wakeRetiredClaude(context.Background(), r.id, ""); err != nil {
		t.Fatalf("wake of a live session: %v", err)
	}
	if n := claudeLaunches(t, r.c.sessions); n != 1 {
		t.Fatalf("%d Claude sessions started, want 1", n)
	}
}

// The wake resumes the conversation last bound to the woken session ID, not
// another conversation of the same agent.
func TestWakeResumesTheConversationBoundToTheSession(t *testing.T) {
	r := newClaudeWakeRig(t, protocol.ModeBackground, protocol.AutonomyOff, []map[string]any{})
	other := "22222222-0000-4000-8000-000000000000"
	otherID, err := r.g.gw.Store().UpsertSession(r.c.d.NodeID(), protocol.SessionInfo{Harness: "claude", NativeID: other, Cwd: r.repo, Name: "shop",
		Mode: protocol.ModeBackground, Repo: protocol.RepoInfo{Root: r.repo, Project: "shop"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.g.gw.Store().SetSessionOffline(r.c.d.NodeID(), otherID); err != nil {
		t.Fatal(err)
	}
	if err := r.c.d.wakeRetiredClaude(context.Background(), r.id, ""); err != nil {
		t.Fatalf("wake refused: %v", err)
	}
	if got := argValue(readClaude(t, r.repo).Args, "--resume"); got != wakeNative {
		t.Fatalf("resumed %s, want %s (the other conversation is %s)", got, wakeNative, other)
	}
}
