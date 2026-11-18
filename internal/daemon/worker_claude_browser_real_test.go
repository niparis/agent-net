package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// The real browser-sandbox check, on macOS. A real Claude Code runs a short
// script in the Bash sandbox that claudeWorkerSettings builds, and the test
// reads what the script left on disk. It makes two paid model calls with
// --model haiku, and it uses the owner's Claude login, so it is opt-in:
//
//	AGENT_NET_TEST_REAL_CLAUDE_SANDBOX=1 go test ./internal/daemon -run TestRealClaudeBrowserSandbox -v
//
// It needs claude, node and npm on PATH, network for npm, and the Playwright
// browsers that playwright-core 1.64.0 uses in ~/Library/Caches/ms-playwright
// (npx playwright@1.64.0 install chromium webkit). Everything it writes is in
// temporary folders; Claude Code keeps its usual session record in ~/.claude.
const realBrowserPlaywright = "1.64.0"

func TestRealClaudeBrowserSandbox(t *testing.T) {
	if os.Getenv("AGENT_NET_TEST_REAL_CLAUDE_SANDBOX") != "1" {
		t.Skip("set AGENT_NET_TEST_REAL_CLAUDE_SANDBOX=1 to run the real Claude Code sandbox")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("the browser profile is for macOS: Linux sandboxes give each command its own loopback")
	}
	for _, bin := range []string{"claude", "node", "npm"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("AGENT_NET_TEST_REAL_CLAUDE_SANDBOX=1, but %s is not on PATH", bin)
		}
	}

	t.Run("opted in", func(t *testing.T) {
		log, project, outside := runRealClaudeProof(t, true)
		t.Logf("proof.log:\n%s", log)
		for _, want := range []string{"serve ok http://127.0.0.1:", "chromium exit 0"} {
			if !strings.Contains(log, want) {
				t.Errorf("proof.log lacks %q", want)
			}
		}
		for _, shot := range []string{"chromium-390.png", "chromium-1280.png"} {
			if fi, err := os.Stat(filepath.Join(project, "shots", shot)); err != nil || fi.Size() == 0 {
				t.Errorf("no screenshot %s: %v", shot, err)
			}
		}
		if _, err := os.Stat(filepath.Join(outside, "denied.txt")); err == nil {
			t.Error("a write outside the worktree went through")
		}
		if !strings.Contains(log, "outside exit 1") {
			t.Errorf("the write outside the worktree was not attempted and refused:\n%s", log)
		}
		// WebKit is reported, not required: see docs/security.md for what it needs.
		t.Logf("webkit: %s", lineWith(log, "webkit exit"))
	})

	t.Run("not opted in", func(t *testing.T) {
		log, _, _ := runRealClaudeProof(t, false)
		t.Logf("proof.log:\n%s", log)
		if strings.Contains(log, "serve ok") || strings.Contains(log, "chromium exit 0") {
			t.Errorf("a project that did not opt in could serve or run Chromium:\n%s", log)
		}
	})
}

func lineWith(s, sub string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, sub) {
			return l
		}
	}
	return "(missing)"
}

// runRealClaudeProof runs proof.sh through a real `claude -p` under the
// worker sandbox settings, and returns proof.log, the project folder (the
// worker's worktree) and a sibling folder the sandbox must not let it write.
func runRealClaudeProof(t *testing.T, optedIn bool) (log, project, outside string) {
	t.Helper()
	project, outside = resolved(t.TempDir()), resolved(t.TempDir())
	probe, err := os.ReadFile(filepath.Join("..", "..", "scripts", "claude-browser-sandbox", "probe.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "probe.mjs"), probe, 0o600); err != nil {
		t.Fatal(err)
	}
	// npm runs here, outside the sandbox: the worker's job is the browser.
	install := exec.Command("npm", "init", "-y")
	install.Dir = project
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("npm init: %v\n%s", err, out)
	}
	install = exec.Command("npm", "install", "--silent", "playwright-core@"+realBrowserPlaywright)
	install.Dir = project
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("npm install: %v\n%s", err, out)
	}
	script := fmt.Sprintf(`#!/bin/bash
cd "$(dirname "$0")"
{
echo "== chromium"; node probe.mjs chromium shots 2>&1; echo "chromium exit $?"
echo "== webkit"; node probe.mjs webkit shots 2>&1; echo "webkit exit $?"
echo "== write outside the worktree"; touch %q 2>&1; echo "outside exit $?"
} > proof.log 2>&1
cat proof.log
`, filepath.Join(outside, "denied.txt"))
	if err := os.WriteFile(filepath.Join(project, "proof.sh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	d := &Daemon{}
	snap := settings.Snapshot{}
	if optedIn {
		snap.Values = []settings.Value{{Key: "workers.claude_browser_sandbox", Level: "repo", Scope: "shop", Value: json.RawMessage("true")}}
	}
	d.settingsSnapshot.Store(&snap)
	w := &worker{id: "w_real", project: "shop", path: project, permissions: protocol.WorkersSandboxed}
	settingsJSON, err := json.Marshal(d.claudeWorkerSettings(w, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("sandbox settings: %s", settingsJSON)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "-p", "--model", "haiku", "--settings", string(settingsJSON),
		"--allowedTools", "Bash(bash proof.sh)", "--output-format", "text",
		"Run exactly one Bash command: bash proof.sh . Do not run it again and do not run anything else. Then say done.")
	cmd.Dir = project
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("claude: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(filepath.Join(project, "proof.log"))
	if err != nil {
		t.Fatalf("Claude did not run proof.sh in the sandbox: %v", err)
	}
	return string(raw), project, outside
}
