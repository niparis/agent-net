package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A Claude Code session keeps every hook answer in its transcript, so the
// full charter block is sent once per session. Later prompts get nothing,
// a change sends the full new text, and a compaction sends it again because
// the summary may have dropped it.

const charterBlockNativeID = "claude-block"

// claudeCharterSession makes a Claude Code session in a repo named demo and
// returns the node and a command-line connection that asks the way the hook
// does. The repo has a charter, so every answer would carry it on main.
func claudeCharterSession(t *testing.T) (*testGateway, *node, *localClient, string) {
	t.Helper()
	g := startGateway(t, t.TempDir(), "")
	sessDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sessDir, "1.json"), []byte(`{"pid":1,"sessionId":"`+charterBlockNativeID+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeAdapter{harness: "claude"}
	n := startNode(t, g, nodeOpts{adapter: adapter, claudeDir: sessDir, cmdline: "/usr/bin/pi"})
	repo := newRepo(t, "demo")
	adapter.setSessions(protocol.SessionInfo{Harness: "claude", NativeID: charterBlockNativeID, Cwd: repo, Status: "idle"})
	eventually(t, "Claude session registered", func() bool {
		return adapter.host.SessionID("claude", charterBlockNativeID) != ""
	})
	if err := g.gw.Store().SetCharter("demo", "maturity", "mature", "owner"); err != nil {
		t.Fatal(err)
	}
	return g, n, cliClient(t, n), charterBlockNativeID
}

func claudePrompt(t *testing.T, cli *localClient, nativeID, prompt string) protocol.PolicyResult {
	t.Helper()
	return askPolicy(t, cli, protocol.PolicyRequest{Harness: "claude", NativeID: nativeID, Prompt: prompt})
}

func TestClaudeCharterBlockOncePerSession(t *testing.T) {
	t.Parallel()
	_, _, cli, nativeID := claudeCharterSession(t)
	for turn := 1; turn <= 3; turn++ {
		got := claudePrompt(t, cli, nativeID, "turn "+string(rune('0'+turn)))
		has := strings.Contains(got.Charter, "maturity mature")
		if turn == 1 && !has {
			t.Fatalf("the first prompt has no charter block:\n%s", got.Charter)
		}
		if turn > 1 && has {
			t.Fatalf("prompt %d repeated the charter block:\n%s", turn, got.Charter)
		}
	}
}

func TestClaudeCharterBlockAgainAfterChange(t *testing.T) {
	t.Parallel()
	g, n, cli, nativeID := claudeCharterSession(t)
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	n.d.policy.now = func() time.Time { return now }
	if got := claudePrompt(t, cli, nativeID, "first"); !strings.Contains(got.Charter, "maturity mature") {
		t.Fatalf("the first prompt has no charter block:\n%s", got.Charter)
	}
	if err := g.gw.Store().SetCharter("demo", "maturity", "early", "owner"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	changed := claudePrompt(t, cli, nativeID, "second")
	if !strings.Contains(changed.Charter, protocol.CharterChangedNotice) {
		t.Fatalf("the changed charter has no change line:\n%s", changed.Charter)
	}
	if !strings.Contains(changed.Charter, "maturity early") {
		t.Fatalf("the changed charter block is not the new text:\n%s", changed.Charter)
	}
	if again := claudePrompt(t, cli, nativeID, "third"); strings.Contains(again.Charter, "maturity") {
		t.Fatalf("the unchanged charter was sent again:\n%s", again.Charter)
	}
}

func TestClaudeCharterBlockAgainAfterCompaction(t *testing.T) {
	t.Parallel()
	_, _, cli, nativeID := claudeCharterSession(t)
	if got := claudePrompt(t, cli, nativeID, "first"); !strings.Contains(got.Charter, "maturity mature") {
		t.Fatalf("the first prompt has no charter block:\n%s", got.Charter)
	}
	if got := claudePrompt(t, cli, nativeID, "second"); got.Charter != "" {
		t.Fatalf("the second prompt repeated the charter block:\n%s", got.Charter)
	}
	cli.mustCall(protocol.MethodCompaction, protocol.CompactionRequest{Harness: "claude", NativeID: nativeID})
	if got := claudePrompt(t, cli, nativeID, "third"); !strings.Contains(got.Charter, "maturity mature") {
		t.Fatalf("the prompt after a compaction has no charter block:\n%s", got.Charter)
	}
	if got := claudePrompt(t, cli, nativeID, "fourth"); got.Charter != "" {
		t.Fatalf("the block was repeated after the compaction send:\n%s", got.Charter)
	}
}
