package daemon

import (
	"context"
	"time"

	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	transcriptDir := filepath.Join(filepath.Dir(n.sessions), "projects", "fake")
	if err := os.MkdirAll(transcriptDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(transcriptDir, id+".jsonl"), []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"reply"}]}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	events, err := h.events(ctx, n.d, 0)
	if err != nil || len(events.Messages) != 1 || !strings.Contains(string(events.Messages[0]), "reply") {
		t.Fatalf("events: %+v %v", events, err)
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

func TestCTOHarnessConversations(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	sessions, _ := useFakeClaude(t)
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) {
		c.ClaudeSessionsDir = sessions
		c.Adapters = append(c.Adapters, &fsClaudeAdapter{dir: sessions})
		c.HarnessPaths = map[string]string{"claude": os.Args[0], "opencode": os.Args[0]}
	}})
	cli := cliClient(t, n)
	model, harness := "fake/m1", "opencode"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Model: &model, Harness: &harness})
	var first map[string]string
	if err := json.Unmarshal(cli.mustCall("cto_send", map[string]string{"text": "open one"}), &first); err != nil {
		t.Fatal(err)
	}
	model, harness = "opus", "claude"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Model: &model, Harness: &harness})
	var second map[string]string
	if err := json.Unmarshal(cli.mustCall("cto_send", map[string]string{"text": "claude one"}), &second); err != nil {
		t.Fatal(err)
	}
	if first["session_id"] == "" || second["session_id"] == "" || first["session_id"] == second["session_id"] {
		t.Fatalf("sessions: %v %v", first, second)
	}
	oldRoot := n.d.claudeCTO.root.Load()
	model, harness = "fake/m1", "opencode"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Model: &model, Harness: &harness})
	if oldRoot == nil || n.d.isCTOPeer(oldRoot.pid) {
		t.Fatal("previous harness retains CTO access after switch")
	}
	var again map[string]string
	json.Unmarshal(cli.mustCall("cto_send", map[string]string{"text": "open two"}), &again)
	if again["session_id"] != first["session_id"] {
		t.Fatalf("OpenCode conversation lost: %v", again)
	}
	cli.mustCall("cto_new", nil)
	json.Unmarshal(cli.mustCall("cto_send", map[string]string{"text": "open fresh"}), &again)
	if again["session_id"] == first["session_id"] {
		t.Fatal("new OpenCode conversation reused old id")
	}
	model, harness = "opus", "claude"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Model: &model, Harness: &harness})
	json.Unmarshal(cli.mustCall("cto_send", map[string]string{"text": "claude two"}), &again)
	if again["session_id"] != second["session_id"] {
		t.Fatalf("Claude conversation lost: %v", again)
	}
	n.restart()
	cli = cliClient(t, n)
	json.Unmarshal(cli.mustCall("cto_send", map[string]string{"text": "after restart"}), &again)
	if again["session_id"] != second["session_id"] {
		t.Fatalf("Claude conversation lost on restart: %v", again)
	}
	cli.mustCall("cto_new", nil)
	json.Unmarshal(cli.mustCall("cto_send", map[string]string{"text": "claude fresh"}), &again)
	if again["session_id"] == second["session_id"] {
		t.Fatal("new Claude conversation reused old id")
	}
}

func TestClaudeCTOTranscriptOwnerMessageAndTime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "project", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","timestamp":"2026-01-02T03:04:05Z","message":{"content":"<cross-session-message from-name=\"owner\" from-mode=\"prompting\">\nhello\n\n[agent-net] Answer in the CTO chat.\n</cross-session-message>"}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	msgs, err := claudeCTOTranscript(dir, "session")
	if err != nil || len(msgs) != 1 {
		t.Fatalf("messages: %s %v", msgs, err)
	}
	var v struct {
		Text string `json:"text"`
		Time struct {
			Created int64 `json:"created"`
		} `json:"time"`
	}
	if json.Unmarshal(msgs[0], &v) != nil || v.Text != "hello" || v.Time.Created != 1767323045000 {
		t.Fatalf("message: %s", msgs[0])
	}
}

func TestClaudeCTOStopIgnored(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	t.Setenv(envFakeStopIgnored, "1")
	g := startGateway(t, t.TempDir(), "")
	n := startClaudeNode(t, g, shortSet)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := (claudeCTOHarness{}).send(ctx, n.d, "hello"); err != nil {
		t.Fatal(err)
	}
	rec := readClaude(t, filepath.Join(n.cfg.StateDir, "cto", "claude"))
	n.d.cto.mu.Lock()
	n.d.stopClaudeCTOLocked()
	n.d.cto.mu.Unlock()
	eventually(t, "stopped Claude process", func() bool { _, alive := procToken(rec.Pid); return !alive })
}

func TestClaudeCTORequiresItsOwnSessionFile(t *testing.T) {
	dir := t.TempDir()
	private := filepath.Join(t.TempDir(), "cto", "claude")
	pid := os.Getpid()
	file := filepath.Join(dir, strconv.Itoa(pid)+".json")
	write := func(cwd, name string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"pid": pid, "sessionId": "native", "jobId": "job", "kind": "bg", "cwd": cwd, "name": name})
		if err := os.WriteFile(file, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(t.TempDir(), "agent-net-cto")
	if _, ok := findClaudeCTOBg(dir, private, "job"); ok {
		t.Fatal("accepted another working directory")
	}
	write(private, "worker")
	if _, ok := findClaudeCTOBg(dir, private, "job"); ok {
		t.Fatal("accepted another session name")
	}
	write(private, "agent-net-cto")
	if _, ok := findClaudeCTOBg(dir, private, "job"); !ok {
		t.Fatal("rejected CTO session")
	}
	if err := os.MkdirAll(private, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(private, link); err != nil {
		t.Fatal(err)
	}
	if _, ok := findClaudeCTOBg(dir, link, "job"); !ok {
		t.Fatal("rejected symlinked state directory")
	}
}

func TestCTOEnterClaude(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	sessions, _ := useFakeClaude(t)
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) {
		c.ClaudeSessionsDir = sessions
		c.HarnessPaths = map[string]string{"claude": os.Args[0]}
	}})
	cli := cliClient(t, n)
	harness := "claude"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Harness: &harness})
	var plan protocol.EnterPlan
	if err := json.Unmarshal(cli.mustCall("cto_enter", nil), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Argv) != 3 || plan.Argv[1] != "attach" || plan.Argv[2] != n.d.claudeCTO.ref {
		t.Fatalf("plan: %+v", plan)
	}
	first := plan.Argv[2]
	if err := json.Unmarshal(cli.mustCall("cto_enter", nil), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Argv) != 3 || plan.Argv[2] != first {
		t.Fatalf("existing session plan: %+v", plan)
	}
}
