package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/daemon/ctoskill"
	"github.com/niparis/agent-net/internal/protocol"
)

// The whole first start uses temp homes and fake Codex servers. There is no
// Claude binary on PATH and the fake hello makes no model call.
func TestRoleSetupWithoutClaudeStartsCodexRolesAndWorker(t *testing.T) {
	home := shortDir(t)
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("AGENT_NET_CONFIG_DIR", filepath.Join(home, "config"))
	t.Setenv("AGENT_NET_STATE_DIR", filepath.Join(home, "state"))
	t.Setenv("AGENT_NET_SOCKET", filepath.Join(home, "daemon.sock"))
	t.Setenv("AGENT_NET_GATEWAY_DATA", filepath.Join(home, "gateway"))
	t.Setenv("AGENT_NET_CLAUDE_SESSIONS_DIR", filepath.Join(home, "claude"))
	t.Setenv(envFakeCodex, "1")
	t.Setenv("FAKE_ROLE_CODEX_BINARY", os.Args[0])
	if _, err := exec.LookPath("claude"); err == nil {
		t.Fatal("Claude is on the isolated PATH")
	}
	bin := filepath.Join(binDir, "codex")
	script := `#!/bin/sh
if [ "$1" = exec ]; then
  while [ "$#" -gt 0 ]; do if [ "$1" = --output-last-message ]; then shift; printf OK > "$1"; fi; shift; done
  exit 0
fi
exec "$FAKE_ROLE_CODEX_BINARY" "$@"
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	f.autoTurn = true
	detect := func(context.Context) []protocol.HarnessInfo {
		return []protocol.HarnessInfo{{Name: "codex", Path: bin, Models: []string{"gpt-fake"}, SignedIn: "yes"}}
	}
	n := startWorkerNodeWithDetect(t, g, shortSet, detect, func(c *Config) {
		c.HomeDir = home
		c.StateDir = filepath.Join(home, "state")
		c.CodexHome = f.home
		c.CodexInterval = 20 * time.Millisecond
		c.HarnessPaths = map[string]string{"codex": bin}
		c.ListModels = func(context.Context, string) ([]string, error) { return []string{"gpt-fake"}, nil }
		c.testNoDeliveryConfirm = false
	})
	eventually(t, "codex inventory", func() bool { list, _ := n.d.inventory(); return len(list) > 0 })
	raw, err := n.cli.callWithin(scaled(60*time.Second), protocol.OpRoleSetup, protocol.RoleSetupRequest{Start: true})
	if err != nil {
		t.Fatal(err)
	}
	var result protocol.RoleSetupResult
	if err := json.Unmarshal(raw, &result); err != nil || len(result.Failures) != 0 || len(result.Models) != 1 {
		t.Fatalf("role setup = %s, %v", raw, err)
	}
	cto := readCodex(t, codexCTODir(n.node), func(r fakeCodexRecord) bool { return len(r.Turns) > 0 && len(r.SkillRoots) > 0 })
	if _, err := os.Stat(filepath.Join(cto.SkillRoots[0], ctoskill.Name, "SKILL.md")); err != nil {
		t.Fatalf("CTO skill: %v", err)
	}
	chief, err := g.gw.Store().ChiefStatus()
	if err != nil || chief.SessionID == "" {
		t.Fatalf("chief = %+v, %v", chief, err)
	}
	eventually(t, "chief takes its first turn", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, turn := range f.turns {
			if strings.Contains(turn, chiefFirstMessage) {
				return true
			}
		}
		return false
	})
	repo := newRepo(t, "shop")
	n.register(t, repo)
	worker := n.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Model: "gpt-fake", Isolation: protocol.IsolationNone, Task: "say ok"})
	rec := readCodex(t, worker.Worktree, func(r fakeCodexRecord) bool { return len(r.Turns) > 0 })
	if len(rec.Turns) == 0 {
		t.Fatal("worker got no turn")
	}
}
