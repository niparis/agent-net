package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// setWorkersLevel sets the workers level of the project that repo belongs to,
// as the owner does: the node setting of the project.
func anyStrings(v any) []string {
	var out []string
	list, _ := v.([]any)
	for _, x := range list {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

func TestCodexConfigUsesTheConfiguredHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[mcp_servers.agent-net]\ncommand = \"agent-net\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{CodexHome: home}}
	if !codexConfigDefinesAgentNet(codexHome(d.cfg)) {
		t.Error("the daemon's Codex home was ignored: its config defines the agent-net server")
	}
}

func TestCodexWorkerTurnsOffTheAgentNetMCPServer(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		want         bool
	}{
		{"defined", "[mcp_servers.other]\ncommand = \"x\"\n\n[mcp_servers.agent-net]\ncommand = \"agent-net\"\n", true},
		{"quoted", "[mcp_servers.\"agent-net\"]\ncommand = \"agent-net\"\n", true},
		{"other only", "[mcp_servers.other]\ncommand = \"x\"\n", false},
		{"no config", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CODEX_HOME", t.TempDir())
			g := startGateway(t, t.TempDir(), "")
			w := startCodexNode(t, g, shortSet)
			home := os.Getenv("CODEX_HOME")
			if tc.config != "" {
				if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(tc.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			repo := newRepo(t, "shop")
			w.register(t, repo)
			w.mustStart(t, protocol.WorkerStartRequest{Model: "gpt-fake", Project: "shop", Harness: "codex", Task: "say ok"})
			rec := readCodex(t, repo, func(r fakeCodexRecord) bool { return len(r.Turns) == 1 })
			joined := strings.Join(rec.Args, " ")
			if got := strings.Contains(joined, "-c mcp_servers.agent-net.enabled=false"); got != tc.want {
				t.Errorf("override present = %v, want %v: %v", got, tc.want, rec.Args)
			}
			if strings.Contains(joined, "mcp_servers.other") {
				t.Errorf("another MCP server is touched: %v", rec.Args)
			}
			w.waitNotice(t, 1)
			eventually(t, "the worker ends", func() bool { return w.d.workerCount() == 0 })
		})
	}
}

// The brief for a Codex worker says the end of the turn is the report and
// does not tell it to use agent-net tools (it names agent_announce only to say to ignore it).
func TestCodexBriefHasNoAgentNetTools(t *testing.T) {
	b := protocol.WorkerTaskBodyBriefBar("codex", protocol.WorkerBrief{Notes: "do it"}, "", "", "", "", "", "", "", settings.Snapshot{}, "", nil)
	for _, bad := range []string{"agent_handback", "agent_message_reply"} {
		if strings.Contains(b, bad) {
			t.Errorf("the Codex brief mentions %s", bad)
		}
	}
	if !strings.Contains(b, "Your last message of the run is your final report") {
		t.Errorf("the Codex brief does not say the end of the turn is the report:\n%s", b)
	}
}

// An approval that still arrives is declined.
func TestCodexApprovalStillDeclinedWithTheSandbox(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	w.mustStart(t, protocol.WorkerStartRequest{Model: "gpt-fake", Project: "shop", Harness: "codex", Task: "FAKE:approve x"})
	rec := readCodex(t, repo, func(r fakeCodexRecord) bool { return len(r.Answers) == 1 })
	if m, _ := rec.Answers[0].(map[string]any); m["decision"] != "decline" {
		t.Errorf("answer: %v", rec.Answers[0])
	}
	// The fake process and its cleanup can still write in the temporary repo
	// after recording the answer. Wait for the notice and the worker's end
	// before TempDir removes that repo.
	w.waitNotice(t, 1)
	eventually(t, "worker cleanup", func() bool { return w.d.workerCount() == 0 })
}

// Only the owner sets the level: an agent session is refused, and the default
// holds.
func TestWorkersLevelGate(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	agent, _ := helloPi(t, w.node, "agent-1", repo, true)
	_, err := agent.call("node_settings_set", map[string]any{"node": "shop", "setting": protocol.SettingWorkerSandbox, "value": protocol.WorkersSandboxed})
	if err == nil || !strings.Contains(err.Error(), "only available to the owner") {
		t.Errorf("an agent set the workers level: %v", err)
	}
	// Nothing was stored by the refusal: the default holds.
	res := w.mustStart(t, protocol.WorkerStartRequest{Model: "gpt-fake", Project: "shop", Harness: "codex", Task: "say ok"})
	if res.Permissions != protocol.WorkersSandboxed {
		t.Errorf("level after the refusal: %q", res.Permissions)
	}
	w.waitNotice(t, 1)
	eventually(t, "the worker ends", func() bool { return w.d.workerCount() == 0 })
}

// A start that waits in the queue keeps the level the project had when it was
// queued, even if the owner changes the level while it waits.
