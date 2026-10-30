package daemon

import (
	"context"
	"time"

	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeCTOTranscript(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "project", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data := "{\"type\":\"user\",\"message\":{\"content\":\"hello\"}}\n" +
		"{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"hi\"}]}}\n" +
		"{\"type\":\"assistant\",\"isSidechain\":true,\"message\":{\"content\":\"hidden\"}}\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	messages, err := claudeCTOTranscript(dir, "session")
	if err != nil || len(messages) != 2 {
		t.Fatalf("messages: %s %v", messages, err)
	}
	var out struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(messages[0], &out) != nil || out.Type != "user" || out.Text != "hello" {
		t.Fatalf("user: %s", messages[0])
	}
	if json.Unmarshal(messages[1], &out) != nil || out.Type != "assistant" || out.Text != "hi" {
		t.Fatalf("assistant: %s", messages[1])
	}
}

func TestClaudeCTOBackgroundAndResume(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startClaudeNode(t, g, shortSet)
	model := "opus"
	n.cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Model: &model})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := claudeCTOHarness{}
	result, err := h.send(ctx, n.d, "first")
	if err != nil {
		t.Fatal(err)
	}
	id := result.(map[string]string)["session_id"]
	rec := readClaude(t, filepath.Join(n.cfg.StateDir, "cto", "claude"))
	if id != rec.Session || argValue(rec.Args, "--model") != "opus" || argValue(rec.Args, "--resume") != "" {
		t.Fatalf("first: %+v %q", rec, id)
	}
	n.d.cto.mu.Lock()
	n.d.stopClaudeCTOLocked()
	n.d.cto.mu.Unlock()
	if _, err := h.send(ctx, n.d, "second"); err != nil {
		t.Fatal(err)
	}
	rec = readClaude(t, filepath.Join(n.cfg.StateDir, "cto", "claude"))
	if rec.Session != id || argValue(rec.Args, "--resume") != id {
		t.Fatalf("resume: %+v", rec)
	}
}

func TestClaudeCTOPrivateToolsAndSession(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startClaudeNode(t, g, shortSet)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := (claudeCTOHarness{}).send(ctx, n.d, "hello"); err != nil {
		t.Fatal(err)
	}
	rec := readClaude(t, filepath.Join(n.cfg.StateDir, "cto", "claude"))
	strict := false
	for _, a := range rec.Args {
		if a == "--strict-mcp-config" {
			strict = true
		}
	}
	var conf struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if !strict || json.Unmarshal([]byte(argValue(rec.Args, "--mcp-config")), &conf) != nil || len(conf.Servers) != 1 {
		t.Fatalf("private config: %v", rec.Args)
	}
	server := conf.Servers["agent-net-cto"]
	if server.Command != n.d.cfg.Exe || len(server.Args) != 1 || server.Args[0] != "cto-mcp" {
		t.Fatalf("server: %+v", server)
	}
	if !n.d.isCTOPeer(rec.ChildPid) || n.d.isCTOPeer(os.Getpid()) {
		t.Fatal("wrong process tree accepted")
	}
	if !n.d.insideSession(rec.ChildPid) {
		t.Fatal("CTO child passed owner gate")
	}
	eventually(t, "Claude adapter discovery", func() bool { sf, ok := findClaudeBg(n.sessions, rec.ID); return ok && sf.SessionID == rec.Session })
	n.d.SetSessions("claude", []protocol.SessionInfo{{NativeID: rec.Session, PID: rec.Pid, Cwd: rec.Cwd}})
	n.d.mu.Lock()
	_, registered := n.d.sessions[key("claude", rec.Session)]
	n.d.mu.Unlock()
	if registered {
		t.Fatal("private CTO registered as network session")
	}
}
