package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestBuildMCPServerStdio(t *testing.T) {
	srv, on, err := buildMCPServer([]string{"fs", "--env", "A_TOKEN", "--env", "B", "--on", "nas", "--timeout", "90s", "--", "npx", "-y", "pkg", "--root", "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	if srv.Name != "fs" || srv.Transport != protocol.MCPStdio || srv.Command != "npx" || on != "nas" || srv.TimeoutSec != 90 ||
		strings.Join(srv.Args, " ") != "-y pkg --root /tmp" || strings.Join(srv.Env, ",") != "A_TOKEN,B" {
		t.Fatalf("srv = %+v on=%q", srv, on)
	}
}

func TestBuildMCPServerURL(t *testing.T) {
	srv, _, err := buildMCPServer([]string{"homelab", "--url", "https://nas/mcp", "--header-env", "Authorization=HOMELAB_TOKEN",
		"--header-file", "X-Key=~/.secrets/k", "--transport", "sse"})
	if err != nil {
		t.Fatal(err)
	}
	if srv.Transport != "sse" || len(srv.Headers) != 2 || srv.Headers[0].Env != "HOMELAB_TOKEN" || srv.Headers[1].File != "~/.secrets/k" {
		t.Fatalf("srv = %+v", srv)
	}
	srv, _, _ = buildMCPServer([]string{"h", "--url", "https://nas/mcp"})
	if srv.Transport != protocol.MCPHTTP {
		t.Fatalf("default transport = %q", srv.Transport)
	}
}

func TestBuildMCPServerRefusals(t *testing.T) {
	bad := map[string][]string{
		"no name":              {"--url", "https://x"},
		"no command":           {"x"},
		"env with value":       {"x", "--env", "T=secret", "--", "cmd"},
		"header with value":    {"x", "--url", "https://h", "--header-env", "Authorization=Bearer sekrit"},
		"header without equal": {"x", "--url", "https://h", "--header-env", "Authorization"},
		"url and command":      {"x", "--url", "https://h", "--", "cmd"},
		"on with url":          {"x", "--url", "https://h", "--on", "nas"},
		"header without url":   {"x", "--header-env", "A=B", "--", "cmd"},
		"bad name":             {"My_Server", "--", "cmd"},
	}
	for what, args := range bad {
		_, _, err := buildMCPServer(args)
		if err == nil {
			t.Errorf("%s accepted", what)
		} else if strings.Contains(err.Error(), "sekrit") || strings.Contains(err.Error(), "secret") && what != "env with value" {
			t.Errorf("%s: error echoes a value: %v", what, err)
		}
	}
}

// `agent-net mcp` with no subcommand is still the stdio server Claude starts,
// and a subcommand goes to the registry CLI. This builds and runs the real binary.
func TestMCPRouting(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "agent-net")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Skipf("cannot build: %v\n%s", err, out)
	}
	run := func(args ...string) string {
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "CLAUDE_CODE_SESSION_ID=", "AGENT_NET_SOCKET="+filepath.Join(t.TempDir(), "none.sock"))
		out, _ := cmd.CombinedOutput()
		return string(out)
	}
	// No subcommand: the server wants a Claude session ID.
	if out := run("mcp"); !strings.Contains(out, "CLAUDE_CODE_SESSION_ID is not set") {
		t.Fatalf("`mcp` no longer starts the stdio server: %s", out)
	}
	// A subcommand: the registry CLI, which needs the daemon.
	if out := run("mcp", "list"); !strings.Contains(out, "cannot reach the daemon") {
		t.Fatalf("`mcp list`: %s", out)
	}
	if out := run("mcp", "bogus"); !strings.Contains(out, "unknown mcp subcommand") {
		t.Fatalf("`mcp bogus`: %s", out)
	}
	if out := run("mcp", "add", "x", "--env", "T=secret", "--", "cmd"); !strings.Contains(out, "NAME only") || strings.Contains(out, "secret\n") {
		t.Fatalf("`mcp add` with a value: %s", out)
	}
}
