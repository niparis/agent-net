package daemon

import (
	"bytes"
	"context"
	"log"
	"time"

	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
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

func TestClaudeCTOTranscriptCommandMarkup(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "proj")
	if err := os.Mkdir(p, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(p, "session.jsonl")
	data := "{\"type\":\"user\",\"message\":{\"content\":\"<command-name>/mcp</command-name>\\n  <command-message>mcp</command-message>\\n  <command-args></command-args>\"}}\n" +
		"{\"type\":\"user\",\"message\":{\"content\":\"<local-command-stdout>Failed to reconnect to agent-net-cto: CONNECTION_CLOSED\"}}\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	messages, err := claudeCTOTranscript(dir, "session")
	if err != nil || len(messages) != 2 {
		t.Fatalf("messages: %s %v", messages, err)
	}
	var out0, out1 struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(messages[0], &out0) != nil || out0.Type != "user" || out0.Text != "/mcp" {
		t.Fatalf("user command: %s", messages[0])
	}
	wantOut := "/mcp → Failed to reconnect to agent-net-cto: CONNECTION_CLOSED"
	if json.Unmarshal(messages[1], &out1) != nil || out1.Text != wantOut {
		t.Fatalf("command stdout: %s", messages[1])
	}
}

func TestClaudeCTOBackgroundAndResume(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startClaudeNode(t, g, shortSet)
	model := "opus"
	n.cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Model: &model})
	ctx, cancel := context.WithTimeout(context.Background(), scaled(10*time.Second))
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
	settingsEnv, _ := rec.Settings["env"].(map[string]any)
	if settingsEnv["CLAUDE_CODE_DISABLE_MOUSE"] != "0" || rec.Env["CLAUDE_CODE_DISABLE_MOUSE"] != "0" || rec.CommandEnv["CLAUDE_CODE_DISABLE_MOUSE"] != "" {
		t.Fatalf("mouse tracking must stay on: settings %v session %v command %v", settingsEnv, rec.Env, rec.CommandEnv)
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

// The wheel scrolls the CTO's long replies only while the session itself has
// mouse tracking on. Claude Code takes CLAUDE_CODE_DISABLE_MOUSE from the
// settings env and from the environment of its background service, never from
// the command that starts the job or from the attach client. A service that
// something else started with the variable set (the daemon's own environment
// stands in for it here) must not turn the wheel off for the CTO.
func TestClaudeCTOKeepsMouseTrackingOnWhenTheServiceHasItOff(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_DISABLE_MOUSE", "1")
	g := startGateway(t, t.TempDir(), "")
	n := startClaudeNode(t, g, shortSet)
	ctx, cancel := context.WithTimeout(context.Background(), scaled(10*time.Second))
	defer cancel()
	if _, err := (claudeCTOHarness{}).send(ctx, n.d, "hello"); err != nil {
		t.Fatal(err)
	}
	rec := readClaude(t, filepath.Join(n.cfg.StateDir, "cto", "claude"))
	settingsEnv, _ := rec.Settings["env"].(map[string]any)
	if settingsEnv["CLAUDE_CODE_DISABLE_MOUSE"] != "0" {
		t.Fatalf("the CTO's settings must turn mouse tracking on: %v", settingsEnv)
	}
	if rec.Env["CLAUDE_CODE_DISABLE_MOUSE"] != "0" {
		t.Fatalf("the CTO session's own mouse setting = %q, want 0", rec.Env["CLAUDE_CODE_DISABLE_MOUSE"])
	}
	if rec.CommandEnv["CLAUDE_CODE_DISABLE_MOUSE"] != "" {
		t.Fatalf("the launch command must not pass the variable on to Claude's service: %q", rec.CommandEnv["CLAUDE_CODE_DISABLE_MOUSE"])
	}
}

func TestClaudeCTOPrivateToolsAndSession(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startClaudeNode(t, g, shortSet)
	ctx, cancel := context.WithTimeout(context.Background(), scaled(10*time.Second))
	defer cancel()
	if _, err := (claudeCTOHarness{}).send(ctx, n.d, "hello"); err != nil {
		t.Fatal(err)
	}
	rec := readClaude(t, filepath.Join(n.cfg.StateDir, "cto", "claude"))
	if rec.MCPClient != "cto-mcp" {
		t.Fatalf("CTO MCP client = %q, want private cto-mcp", rec.MCPClient)
	}
	toolsOff, privateSources := false, false
	for i, arg := range rec.Args {
		if arg == "--setting-sources" && i+1 < len(rec.Args) && rec.Args[i+1] == "" {
			privateSources = true
		}
		if arg == "--tools" && i+1 < len(rec.Args) && rec.Args[i+1] == "" {
			toolsOff = true
		}
	}
	if !toolsOff {
		t.Error("Claude built-in tools still enabled")
	}
	if !privateSources {
		t.Error("owner settings loaded")
	}
	if plugins, ok := rec.Settings["enabledPlugins"].(map[string]any); !ok || len(plugins) != 0 {
		t.Error("plugins enabled")
	}
	if rec.Settings["disableAllHooks"] != true {
		t.Error("hooks enabled")
	}

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
	withFakeOpenCodeRouteSets(t, testCTOHarnessConversations)
}

func testCTOHarnessConversations(t *testing.T) {
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
	networkID := n.d.ctoSessionID()
	if networkID == "" {
		t.Fatal("CTO has no network identity")
	}
	model, harness = "opus", "claude"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Model: &model, Harness: &harness})
	var second map[string]string
	if err := json.Unmarshal(cli.mustCall("cto_send", map[string]string{"text": "claude one"}), &second); err != nil {
		t.Fatal(err)
	}
	if n.d.ctoSessionID() != networkID {
		t.Fatal("harness switch changed CTO network identity")
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
	if n.d.ctoSessionID() != networkID {
		t.Fatal("restart changed CTO network identity")
	}

	cli.mustCall("cto_new", nil)
	json.Unmarshal(cli.mustCall("cto_send", map[string]string{"text": "claude fresh"}), &again)
	if again["session_id"] == second["session_id"] {
		t.Fatal("new Claude conversation reused old id")
	}
}

func TestCTOEventsKeepTimelineAcrossHarnessSwitchesAndRestart(t *testing.T) {
	withFakeOpenCodeRouteSets(t, testCTOEventsKeepTimelineAcrossHarnessSwitchesAndRestart)
}

func testCTOEventsKeepTimelineAcrossHarnessSwitchesAndRestart(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	sessions, _ := useFakeClaude(t)
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) {
		c.ClaudeSessionsDir = sessions
		c.Adapters = append(c.Adapters, &fsClaudeAdapter{dir: sessions})
		c.HarnessPaths = map[string]string{"claude": os.Args[0], "opencode": os.Args[0]}
	}})
	cli := cliClient(t, n)
	set := func(harness, model string) {
		t.Helper()
		cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Harness: &harness, Model: &model})
	}
	read := func() protocol.CTOEvents {
		t.Helper()
		var out protocol.CTOEvents
		if err := json.Unmarshal(cli.mustCall("cto_events", map[string]int{"index": 0}), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	set("opencode", "fake/m1")
	cli.mustCall("cto_send", map[string]string{"text": "OpenCode one"})
	set("claude", "opus")
	var claude map[string]string
	if err := json.Unmarshal(cli.mustCall("cto_send", map[string]string{"text": "Claude one"}), &claude); err != nil {
		t.Fatal(err)
	}
	transcriptDir := filepath.Join(filepath.Dir(sessions), "projects", "fake")
	if err := os.MkdirAll(transcriptDir, 0700); err != nil {
		t.Fatal(err)
	}
	claudeAt := time.Now().UTC().Format(time.RFC3339Nano)
	transcript := `{"type":"user","timestamp":"` + claudeAt + `","message":{"content":"Claude one"}}` + "\n" +
		`{"type":"assistant","timestamp":"` + claudeAt + `","message":{"content":[{"type":"text","text":"Claude reply"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(transcriptDir, claude["session_id"]+".jsonl"), []byte(transcript), 0600); err != nil {
		t.Fatal(err)
	}
	set("opencode", "fake/m1")
	cli.mustCall("cto_send", map[string]string{"text": "OpenCode two"})

	assertTimeline := func(events protocol.CTOEvents) {
		t.Helper()
		type entry struct {
			at   int64
			text string
		}
		var timeline []entry
		for _, raw := range events.Messages {
			var msg struct {
				Text string `json:"text"`
				Time struct {
					Created int64 `json:"created"`
				} `json:"time"`
			}
			if err := json.Unmarshal(raw, &msg); err != nil {
				t.Fatal(err)
			}
			timeline = append(timeline, entry{at: msg.Time.Created * int64(time.Millisecond), text: msg.Text})
		}
		for _, action := range events.Actions {
			timeline = append(timeline, entry{at: action.At, text: action.Text})
		}
		sort.SliceStable(timeline, func(i, j int) bool { return timeline[i].at < timeline[j].at })
		var got []string
		for _, item := range timeline {
			got = append(got, item.text)
		}
		joined := strings.Join(got, "\n")
		want := []string{"OpenCode one", "switched to claude · opus ·", "Claude one", "Claude reply", "switched to opencode · fake/m1 ·", "OpenCode two"}
		for i := range want {
			if i >= len(got) || (strings.HasPrefix(want[i], "switched") && !strings.HasPrefix(got[i], want[i])) || (!strings.HasPrefix(want[i], "switched") && got[i] != want[i]) {
				t.Fatalf("timeline order mismatch: got %q, want %q\nfull=%s", got, want, joined)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("timeline length = %d, want %d: %s", len(got), len(want), joined)
		}
		if len(events.Actions) < 2 {
			t.Fatalf("switch dividers missing: %+v", events.Actions)
		}
		if !strings.HasPrefix(events.Actions[0].Text, "switched to claude · opus ·") || !strings.HasPrefix(events.Actions[1].Text, "switched to opencode · fake/m1 ·") {
			t.Errorf("switch order: %+v", events.Actions)
		}
	}

	beforeRestart := read()
	assertTimeline(beforeRestart)
	n.restart()
	cli = cliClient(t, n)
	var afterRestart protocol.CTOEvents
	if err := json.Unmarshal(cli.mustCall("cto_events", map[string]int{"index": 0}), &afterRestart); err != nil {
		t.Fatal(err)
	}
	assertTimeline(afterRestart)
	if !reflect.DeepEqual(beforeRestart.Messages, afterRestart.Messages) || !reflect.DeepEqual(beforeRestart.Actions, afterRestart.Actions) {
		t.Fatalf("timeline changed after daemon restart:\nbefore=%+v\nafter=%+v", beforeRestart, afterRestart)
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
	ctx, cancel := context.WithTimeout(context.Background(), scaled(10*time.Second))
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
	if _, set := plan.Env["CLAUDE_CODE_DISABLE_MOUSE"]; set {
		t.Fatalf("the CTO's attach must not touch mouse tracking: %v", plan.Env)
	}
	cfgPath := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), ".claude.json")
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Projects map[string]struct {
			Accepted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	dir := filepath.Join(n.cfg.StateDir, "cto", "claude")
	if json.Unmarshal(raw, &cfg) != nil || !cfg.Projects[dir].Accepted {
		t.Fatalf("CTO trust: %s", raw)
	}
	first := plan.Argv[2]
	if err := json.Unmarshal(cli.mustCall("cto_enter", nil), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Argv) != 3 || plan.Argv[2] != first {
		t.Fatalf("existing session plan: %+v", plan)
	}
}

func TestClaudeCTOCompatibilityAndStartupError(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	write := func(script string) {
		t.Helper()
		if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	write(`if [ "$1" = --help ]; then echo '--strict-mcp-config'; elif [ "$1" = --version ]; then echo '2.1.289 (Claude Code)'; fi`)
	d := &Daemon{cfg: Config{StateDir: dir, HarnessPaths: map[string]string{"claude": bin}}}
	err := d.startClaudeCTO(context.Background(), "", "opus")
	if err == nil || !strings.Contains(err.Error(), "required capability --tools is missing") {
		t.Fatalf("old Claude: %v", err)
	}
	write(`if [ "$1" = --help ]; then echo '--bg --strict-mcp-config --tools'; elif [ "$1" = --version ]; then echo '2.1.316'; else echo 'unknown option --bg' >&2; echo 'token=topsecretvalue' >&2; echo 'third line' >&2; exit 1; fi`)
	err = d.startClaudeCTO(context.Background(), "", "opus")
	if err == nil || !strings.Contains(err.Error(), "unknown option --bg") || !strings.Contains(err.Error(), "[REDACTED]") || strings.Contains(err.Error(), "topsecretvalue") || strings.Contains(err.Error(), "third line") {
		t.Fatalf("startup error: %v", err)
	}
}

func TestClaudeCTOCapabilityCache(t *testing.T) {
	dir := t.TempDir()
	bin, log := filepath.Join(dir, "claude"), filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho called >> " + log + "\nif [ \"$1\" = --version ]; then echo 2.1.316; else echo --bg --strict-mcp-config --tools; fi\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{}
	for i := 0; i < 2; i++ {
		if err := d.checkClaudeCTOCapabilities(context.Background(), bin); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(log)
	if err != nil || strings.Count(string(raw), "called") != 2 {
		t.Fatalf("cache: %q %v", raw, err)
	}
	other := filepath.Join(dir, "other-claude")
	if err := os.WriteFile(other, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := d.checkClaudeCTOCapabilities(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if err := d.checkClaudeCTOCapabilities(context.Background(), bin); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(log)
	if err != nil || strings.Count(string(raw), "called") != 4 {
		t.Fatalf("path cache: %q %v", raw, err)
	}
	stamp := time.Now().Add(3 * time.Second)
	if err := os.Chtimes(bin, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := d.checkClaudeCTOCapabilities(context.Background(), bin); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(log)
	if err != nil || strings.Count(string(raw), "called") != 6 {
		t.Fatalf("mtime: %q %v", raw, err)
	}
}

// The owner's start and the delivery of a proposal note can both start the
// Claude CTO. They must not check Claude's capabilities at the same time: the
// cache is one map, and every check runs two subprocesses.
func TestClaudeCTOCapabilityCheckIsSerialized(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	bin, logFile := filepath.Join(dir, "claude"), filepath.Join(dir, "calls")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = --help ]; then echo check >> " + logFile + "; echo '--bg --strict-mcp-config --tools'; " +
		"elif [ \"$1\" = --version ]; then echo check >> " + logFile + "; echo 2.1.316; " +
		"else echo start >> " + logFile + "; echo 'start failed' >&2; exit 1; fi\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	d := &Daemon{log: log.New(&logs, "", 0), cfg: Config{StateDir: dir, HarnessPaths: map[string]string{"claude": bin}}}

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, 16)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = d.startClaudeCTO(context.Background(), "", "opus")
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err == nil || !strings.Contains(err.Error(), "start failed") {
			t.Fatalf("start %d: %v", i, err)
		}
	}
	raw, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	// One help call and one version call fill the cache for all of the starts.
	if calls := strings.Count(string(raw), "check"); calls != 2 {
		t.Fatalf("%d capability checks for one binary, want 2: %q", calls, raw)
	}
}

func TestClaudeCTOTrustPromptError(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := `#!/bin/sh
if [ "$1" = --help ]; then echo '--bg --strict-mcp-config --tools'; elif [ "$1" = --version ]; then echo 2.1.316; else echo 'Accessing workspace'; echo 'Is this a project you created or one you trust?'; exit 1; fi
`
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{StateDir: dir, HarnessPaths: map[string]string{"claude": bin}}}
	err := d.startClaudeCTO(context.Background(), "", "opus")
	want := "Claude Code does not trust " + filepath.Join(dir, "cto", "claude")
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want %s", err, want)
	}
}

func TestCTOExtraBuiltinFailsStartup(t *testing.T) {
	withFakeOpenCodeRouteSets(t, testCTOExtraBuiltinFailsStartup)
}

func testCTOExtraBuiltinFailsStartup(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	t.Setenv("FAKE_CTO_EXTRA_TOOL", "Bash")
	g := startGateway(t, t.TempDir(), "")
	n := startClaudeNode(t, g, shortSet)
	n.d.cfg.HarnessPaths["opencode"] = os.Args[0]
	n.d.cto.mu.Lock()
	err := n.d.startCTOLocked(context.Background())
	retainsProcess := n.d.cto.cmd != nil
	n.d.cto.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "Bash") {
		t.Fatalf("extra built-in must fail clearly: %v", err)
	}
	if retainsProcess {
		t.Error("rejected runtime retains CTO access")
	}
}

func TestClaudeCTORequiresToolsCapability(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	// A small script reports the flags, as a real `claude --help` does. The
	// fake test binary is large, and a machine under load can fail to fork or
	// kill such a child; that error says nothing about --tools.
	bin := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\nif [ \"$1\" = --help ]; then echo '--bg --strict-mcp-config'; elif [ \"$1\" = --version ]; then echo 2.1.316; fi\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	n := startClaudeNode(t, g, shortSet)
	n.d.cfg.HarnessPaths["claude"] = bin
	err := n.d.startClaudeCTO(context.Background(), "", "opus")
	if err == nil || !strings.Contains(err.Error(), "--tools") {
		t.Fatalf("missing --tools must fail clearly: %v", err)
	}
}

func TestCTOOpenCodeMissingDenyFailsStartup(t *testing.T) {
	withFakeOpenCodeRouteSets(t, testCTOOpenCodeMissingDenyFailsStartup)
}

func testCTOOpenCodeMissingDenyFailsStartup(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	t.Setenv("FAKE_CTO_MISSING_DENY", "read")
	g := startGateway(t, t.TempDir(), "")
	n := startClaudeNode(t, g, shortSet)
	n.d.cfg.HarnessPaths["opencode"] = os.Args[0]
	n.d.cto.mu.Lock()
	err := n.d.startCTOLocked(context.Background())
	retainsProcess := n.d.cto.cmd != nil
	n.d.cto.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("missing deny must fail clearly: %v", err)
	}
	if retainsProcess {
		t.Error("rejected runtime retains CTO access")
	}
}

func TestClaudeCTOCapabilitiesWithoutVersionGate(t *testing.T) {
	for _, tc := range []struct {
		name, help, version, want string
	}{
		{"mac version", "--tools --strict-mcp-config", "2.1.289", ""},
		{"future version", "--tools --strict-mcp-config", "9.0.0", ""},
		{"unrecognized version", "--tools --strict-mcp-config", "development", ""},
		{"missing tools", "--strict-mcp-config", "2.1.316", "--tools"},
		{"missing strict MCP", "--tools", "2.1.316", "--strict-mcp-config"},
		{"similar flag", "--tools-other --strict-mcp-config", "2.1.316", "--tools"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "claude")
			script := "#!/bin/sh\nif [ \"$1\" = --help ]; then echo '" + tc.help + "'; else echo '" + tc.version + "'; fi\n"
			if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			d := &Daemon{log: log.New(&logs, "", 0)}
			err := d.checkClaudeCTOCapabilities(context.Background(), bin)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
			if !strings.Contains(logs.String(), "CTO Claude Code version:") {
				t.Fatalf("version was not logged: %q", logs.String())
			}
		})
	}
}

func TestClaudeCTOAdoptReleasedJob(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startClaudeNode(t, g, shortSet)
	ctx, cancel := context.WithTimeout(context.Background(), scaled(10*time.Second))
	defer cancel()
	if _, err := (claudeCTOHarness{}).send(ctx, n.d, "hello"); err != nil {
		t.Fatal(err)
	}
	n.d.cto.mu.Lock()
	root := n.d.claudeCTO.root.Load()
	n.d.claudeCTO.root.Store(nil)
	n.d.claudeCTO.ref = ""
	n.d.cto.mu.Unlock()
	if !n.d.adoptLiveClaudeCTO() || !n.d.isCTOPeer(root.pid) {
		t.Fatal("released job was not adopted")
	}
}

func TestClaudeCTOAdoptionRestartHello(t *testing.T) {
	for _, mode := range []string{"startup", "late", "ended"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
			g := startGateway(t, t.TempDir(), "")
			n := startClaudeNode(t, g, shortSet)
			ctx, cancel := context.WithTimeout(context.Background(), scaled(10*time.Second))
			defer cancel()
			if _, err := (claudeCTOHarness{}).send(ctx, n.d, "hello"); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(n.cfg.StateDir, "cto", "claude")
			marker := filepath.Join(dir, "fake-cto-hello.json")
			eventually(t, "initial CTO hello", func() bool { _, err := os.Stat(marker); return err == nil })
			root := n.d.claudeCTO.root.Load()
			ref := n.d.claudeCTO.ref
			n.d.cfg.testStopCTOOnExit = false
			n.d.Stop()
			os.Remove(marker)
			sessionPath := filepath.Join(n.sessions, strconv.Itoa(root.pid)+".json")
			raw, err := os.ReadFile(sessionPath)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "late" {
				if err := os.Remove(sessionPath); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "ended" {
				if err := os.WriteFile(filepath.Join(filepath.Dir(n.sessions), "jobs", ref, "state.json"), []byte(`{"state":"stopped"}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			d, err := New(n.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := d.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			n.d = d
			t.Cleanup(func() {
				// End the surviving fake job even in the ended-state case.
				d.cto.mu.Lock()
				d.claudeCTO.ref = ref
				d.stopClaudeCTOLocked()
				d.cto.mu.Unlock()
				d.Stop()
			})
			if mode == "startup" && !d.isCTOPeer(root.pid) {
				t.Fatal("startup did not adopt live job")
			}
			if mode == "late" {
				if err := os.WriteFile(sessionPath, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			// The fake CTO records every hello answer. In late mode the MCP
			// child can reconnect before the session file is back, so its first
			// answer is the rejection; the retry after adoption succeeds. Wait
			// for the answer this mode expects, not just for a file to appear.
			var response []byte
			var reply struct {
				Error json.RawMessage `json:"error"`
			}
			eventually(t, "reconnected CTO hello", func() bool {
				raw, err := os.ReadFile(marker)
				if err != nil {
					return false
				}
				var r struct {
					Error json.RawMessage `json:"error"`
				}
				if json.Unmarshal(raw, &r) != nil {
					return false
				}
				rejected := len(r.Error) != 0 && string(r.Error) != "null"
				if (mode == "ended") != rejected {
					return false
				}
				response, reply = raw, r
				return true
			})
			if mode == "ended" {
				if len(reply.Error) == 0 || string(reply.Error) == "null" || d.claudeCTO.root.Load() != nil {
					t.Fatalf("ended job adopted: %s", response)
				}
			} else {
				if len(reply.Error) != 0 && string(reply.Error) != "null" {
					t.Fatalf("hello failed: %s", response)
				}
				if !d.ctoRunning() || !d.isCTOPeer(root.pid) || d.claudeCTO.ref != ref {
					t.Fatal("CTO is not running after reconnect")
				}
			}
		})
	}
}

func TestClaudeCTOStartupAdoptsWithoutGatewayOrHello(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startClaudeNode(t, g, shortSet)
	ctx, cancel := context.WithTimeout(context.Background(), scaled(10*time.Second))
	defer cancel()
	if _, err := (claudeCTOHarness{}).send(ctx, n.d, "hello"); err != nil {
		t.Fatal(err)
	}
	root := n.d.claudeCTO.root.Load()
	ref := n.d.claudeCTO.ref
	n.d.cfg.testStopCTOOnExit = false
	n.d.Stop()
	g.stop()
	cfg := n.cfg
	// The existing MCP child retries the old socket. It cannot reach this daemon.
	cfg.SocketPath = filepath.Join(shortDir(t), "offline.sock")
	logs := &logBuf{}
	cfg.Logger = log.New(logs, "", 0)
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		d.cto.mu.Lock()
		d.claudeCTO.ref = ref
		d.stopClaudeCTOLocked()
		d.cto.mu.Unlock()
		d.Stop()
	})
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d.Connected() {
		t.Fatal("unexpected gateway connection")
	}
	if d.claudeCTO.root.Load() == nil || !d.isCTOPeer(root.pid) {
		t.Fatal("startup did not adopt live CTO without gateway or hello")
	}
	if len(logs.lines("cto.start harness=claude mode=adopted")) == 0 {
		t.Fatal("missing startup adoption log")
	}
}

// The CTO starts with --setting-sources "", so the owner's user settings do not
// reach it. The renderer is one of them: "tui": "fullscreen" gives the owner
// scrollback in every other session, and the CTO fell back to the classic
// renderer, where a long reply (a table) runs off the screen. The CTO gets the
// owner's renderer choice in its own launch settings, and nothing else.
func TestClaudeCTOKeepsOwnerRenderer(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startClaudeNode(t, g, shortSet)
	user := `{"tui":"fullscreen","model":"sonnet","hooks":{"Stop":[]},"enabledPlugins":{"x@y":true}}`
	if err := os.WriteFile(filepath.Join(filepath.Dir(n.sessions), "settings.json"), []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), scaled(10*time.Second))
	defer cancel()
	if _, err := (claudeCTOHarness{}).send(ctx, n.d, "hello"); err != nil {
		t.Fatal(err)
	}
	rec := readClaude(t, filepath.Join(n.cfg.StateDir, "cto", "claude"))
	if rec.Settings["tui"] != "fullscreen" {
		t.Errorf("CTO tui setting = %v, want the owner's fullscreen renderer", rec.Settings["tui"])
	}
	if _, ok := rec.Settings["model"]; ok {
		t.Error("CTO took the owner's model from user settings")
	}
	if plugins, ok := rec.Settings["enabledPlugins"].(map[string]any); !ok || len(plugins) != 0 || rec.Settings["disableAllHooks"] != true {
		t.Error("CTO took owner plugins or hooks")
	}
}

// An owner with no renderer setting gets the Claude Code default, as the other
// sessions do, so no "tui" key is written.
func TestClaudeCTOWithoutOwnerRenderer(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startClaudeNode(t, g, shortSet)
	ctx, cancel := context.WithTimeout(context.Background(), scaled(10*time.Second))
	defer cancel()
	if _, err := (claudeCTOHarness{}).send(ctx, n.d, "hello"); err != nil {
		t.Fatal(err)
	}
	rec := readClaude(t, filepath.Join(n.cfg.StateDir, "cto", "claude"))
	if v, ok := rec.Settings["tui"]; ok {
		t.Errorf("CTO tui setting = %v with no owner setting", v)
	}
}

// The CTO's agent_message_send is allowed in its launch settings, so Claude
// Code's auto-mode check does not block it. That is the only allow entry.
func TestClaudeCTOAllowsOnlyItsMessageTool(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startClaudeNode(t, g, shortSet)
	ctx, cancel := context.WithTimeout(context.Background(), scaled(10*time.Second))
	defer cancel()
	if _, err := (claudeCTOHarness{}).send(ctx, n.d, "hello"); err != nil {
		t.Fatal(err)
	}
	rec := readClaude(t, filepath.Join(n.cfg.StateDir, "cto", "claude"))
	permissions, ok := rec.Settings["permissions"].(map[string]any)
	if !ok || len(permissions) != 1 {
		t.Fatalf("CTO permissions = %v, want only an allow list", rec.Settings["permissions"])
	}
	allow, ok := permissions["allow"].([]any)
	if !ok || len(allow) != 1 || allow[0] != "mcp__agent-net-cto__agent_message_send" {
		t.Fatalf("CTO allow = %v, want only mcp__agent-net-cto__agent_message_send", permissions["allow"])
	}
}
