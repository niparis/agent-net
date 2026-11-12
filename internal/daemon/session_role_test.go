package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The CTO is an ordinary Claude start with a role. The one builder
// (claudeStartSpec) applies the role's name, flags, prompt, MCP config and
// settings; a coding session gets none of them.
func TestClaudeStartSpecAppliesTheCTORole(t *testing.T) {
	d := &Daemon{cfg: Config{Exe: "/bin/agent-net", SocketPath: "/run/agent-net.sock", StateDir: t.TempDir()}}
	coding := d.claudeStartSpec(context.Background(), claudeStartInput{Name: "shop", Model: "haiku", FirstMessage: "hi"})
	if len(coding.Flags) != 0 || coding.Name != "shop" || strings.Join(coding.ModelArgs, " ") != "--model haiku" || coding.FirstMessage != "hi" {
		t.Fatalf("coding spec: %+v", coding)
	}
	cto := d.claudeStartSpec(context.Background(), claudeStartInput{Role: roleCTO, Resume: "n1", Model: "opus"})
	args := claudeBackgroundArgs(cto)
	for flag, want := range map[string]string{"--name": "agent-net-cto", "--resume": "n1", "--model": "opus"} {
		if got := argValue(args, flag); got != want || !contains(args, flag) {
			t.Errorf("%s = %q (present %v), want %q in %q", flag, got, contains(args, flag), want, args)
		}
	}
	if !contains(args, "--strict-mcp-config") || !strings.Contains(argValue(args, "--system-prompt"), "You are the CTO") {
		t.Errorf("role flags missing: %q", args)
	}
	var mcp struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(argValue(args, "--mcp-config")), &mcp); err != nil || mcp.Servers["agent-net-cto"].Command != "/bin/agent-net" || strings.Join(mcp.Servers["agent-net-cto"].Args, " ") != "cto-mcp" {
		t.Errorf("mcp config %q: %v", argValue(args, "--mcp-config"), err)
	}
	if !strings.Contains(cto.Settings, `"AGENT_NET_CTO":"1"`) || !strings.Contains(cto.Settings, "mcp__agent-net-cto__agent_message_send") {
		t.Errorf("role settings: %s", cto.Settings)
	}
	// Full power (decision 0157): built-in tools and the owner's own Claude
	// settings, hooks included.
	for _, flag := range []string{"--tools", "--setting-sources", "--disallowedTools"} {
		if contains(args, flag) {
			t.Errorf("the CTO role must not pass %s: %q", flag, args)
		}
	}
	if strings.Contains(cto.Settings, "disableAllHooks") {
		t.Errorf("the CTO's hooks are off: %s", cto.Settings)
	}
	if len(cto.DropEnv) != 1 || cto.DropEnv[0] != "CLAUDE_CODE_DISABLE_MOUSE" {
		t.Errorf("the CTO must not inherit a mouse switch: %v", cto.DropEnv)
	}
}

// The CTO's launch settings carry its PreToolUse hook on cto_op. It runs
// agent-net cto-hook, which allows every op the owner can easily revert.
func TestCTORoleCarriesItsHook(t *testing.T) {
	d := &Daemon{cfg: Config{Exe: "/opt/agent net/agent-net", SocketPath: "/run/agent-net.sock", StateDir: t.TempDir()}}
	cto := d.claudeStartSpec(context.Background(), claudeStartInput{Role: roleCTO, Model: "opus"})
	var launch struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(cto.Settings), &launch); err != nil {
		t.Fatal(err)
	}
	pre := launch.Hooks["PreToolUse"]
	if len(pre) != 1 || pre[0].Matcher != "mcp__agent-net-cto__cto_op" || len(pre[0].Hooks) != 1 || pre[0].Hooks[0].Type != "command" || pre[0].Hooks[0].Command != "'/opt/agent net/agent-net' cto-hook" {
		t.Fatalf("PreToolUse hooks = %+v in %s", pre, cto.Settings)
	}
	coding := d.claudeStartSpec(context.Background(), claudeStartInput{Name: "shop"})
	if strings.Contains(coding.Settings, "cto-hook") {
		t.Fatalf("a coding session got the CTO hook: %s", coding.Settings)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// No code outside the one start path may launch the CTO: it has no launcher
// of its own for either harness (decision 0156).
func TestNoPrivateCTOLauncher(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	banned := []string{"func (d *Daemon) startClaudeCTO", "func (d *Daemon) startClaudeCTOOnce", "func (d *Daemon) startCTOLocked"}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, b := range banned {
				if strings.Contains(line, b) {
					t.Errorf("%s:%d has a private launcher (%s); the CTO starts through the shared start", f, i+1, b)
				}
			}
			// A private `opencode serve` for a session runs only through
			// serveOpenCodePrivate. The cleanup of a worker's data starts a
			// throwaway server that only deletes one session; it starts none.
			if strings.Contains(line, `"serve", "--hostname"`) && f != "worker_session_cleanup.go" && !strings.Contains(string(raw), "serveOpenCodePrivate(") {
				t.Errorf("%s:%d starts opencode serve outside serveOpenCodePrivate", f, i+1)
			}
			if f != "claude_background.go" && f != "worker_claude.go" && strings.Contains(line, "runClaudeBackground(") {
				t.Errorf("%s:%d launches Claude outside startBackground", f, i+1)
			}
		}
	}
}
