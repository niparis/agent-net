package daemon

import (
	"context"
	"time"

	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
	ctx, cancel := context.WithTimeout(context.Background(), scaled(10*time.Second))
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

func TestCTOEventsKeepTimelineAcrossHarnessSwitchesAndRestart(t *testing.T) {
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
	err := d.startClaudeCTOLocked(context.Background(), "", "opus")
	if err == nil || !strings.Contains(err.Error(), "Claude Code 2.1.289 is too old for the CTO: run `claude update`") {
		t.Fatalf("old Claude: %v", err)
	}
	write(`if [ "$1" = --help ]; then echo '--bg --strict-mcp-config'; elif [ "$1" = --version ]; then echo '2.1.314'; else echo 'unknown option --bg' >&2; echo 'token=topsecretvalue' >&2; echo 'third line' >&2; exit 1; fi`)
	err = d.startClaudeCTOLocked(context.Background(), "", "opus")
	if err == nil || !strings.Contains(err.Error(), "unknown option --bg") || !strings.Contains(err.Error(), "[REDACTED]") || strings.Contains(err.Error(), "topsecretvalue") || strings.Contains(err.Error(), "third line") {
		t.Fatalf("startup error: %v", err)
	}
}

func TestClaudeCTOCapabilityCache(t *testing.T) {
	dir := t.TempDir()
	bin, log := filepath.Join(dir, "claude"), filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho called >> " + log + "\necho --bg --strict-mcp-config\n"
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
	if err != nil || strings.Count(string(raw), "called") != 1 {
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
	if err != nil || strings.Count(string(raw), "called") != 2 {
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
	if err != nil || strings.Count(string(raw), "called") != 3 {
		t.Fatalf("mtime: %q %v", raw, err)
	}
}

func TestClaudeCTOTrustPromptError(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := `#!/bin/sh
if [ "$1" = --help ]; then echo '--bg --strict-mcp-config'; else echo 'Accessing workspace'; echo 'Is this a project you created or one you trust?'; exit 1; fi
`
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{StateDir: dir, HarnessPaths: map[string]string{"claude": bin}}}
	err := d.startClaudeCTOLocked(context.Background(), "", "opus")
	want := "Claude Code does not trust " + filepath.Join(dir, "cto", "claude")
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want %s", err, want)
	}
}
