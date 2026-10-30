package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The daemon learns the version of a session's agent-net tools from the hello
// of `agent-net mcp`, marks the session when the tools are older than the
// daemon, and hands the process a note to show once.
func TestMCPHelloMarksOldTools(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	sessDir := t.TempDir()
	// The test process stands in for `agent-net mcp`; its parent for Claude Code.
	file := filepath.Join(sessDir, strconv.Itoa(os.Getppid())+".json")
	if err := os.WriteFile(file, []byte(`{"pid":1,"sessionId":"sess-1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fa := &fakeAdapter{harness: "claude"}
	n := startNode(t, g, nodeOpts{adapter: fa, claudeDir: sessDir, cfg: func(c *Config) { c.Version = "v0.5.0" }})
	info := protocol.SessionInfo{Harness: "claude", NativeID: "sess-1", Name: "one", Status: "idle"}
	fa.setSessions(info)
	eventually(t, "session id", func() bool { return fa.host.SessionID("claude", "sess-1") != "" })
	id := fa.host.SessionID("claude", "sess-1")
	cli := cliClient(t, n)

	hello := func(version string) map[string]any {
		t.Helper()
		p := map[string]any{"client": "mcp", "harness": "claude", "native_id": "sess-1"}
		if version != "" {
			p["version"] = version
		}
		lc := dialLocal(t, n.sock)
		var res map[string]any
		if err := json.Unmarshal(lc.mustCall("hello", p), &res); err != nil {
			t.Fatal(err)
		}
		return res
	}

	// Older tools: a note naming both versions and the /mcp advice, and the
	// session is marked for the owner.
	res := hello("v0.4.5")
	note, _ := res["tools_note"].(string)
	for _, want := range []string{"agent-net:", "v0.4.5", "v0.5.0", "/mcp", "reconnect"} {
		if !strings.Contains(note, want) {
			t.Errorf("note %q lacks %q", note, want)
		}
	}
	eventually(t, "marked old", func() bool {
		a := agentByID(t, cli, id)
		return a.ToolsVersion == "v0.4.5" && a.ToolsOld
	})

	// Discovery runs again and changes the session: the mark stays.
	info.Name = "renamed"
	fa.setSessions(info)
	eventually(t, "renamed", func() bool { return agentByID(t, cli, id).Profile.Name == "renamed" })
	if a := agentByID(t, cli, id); a.ToolsVersion != "v0.4.5" || !a.ToolsOld {
		t.Errorf("discovery erased the tools mark: %+v", a)
	}

	// No version: unknown, and no note (that process cannot show one).
	res = hello("")
	if _, ok := res["tools_note"]; ok {
		t.Errorf("a process with no version got a note: %v", res)
	}
	eventually(t, "marked unknown", func() bool {
		a := agentByID(t, cli, id)
		return a.ToolsVersion == protocol.ToolsUnknown && a.ToolsOld
	})

	// Current tools: no note, and the mark clears.
	res = hello("v0.5.0")
	if _, ok := res["tools_note"]; ok {
		t.Errorf("current tools got a note: %v", res)
	}
	eventually(t, "mark cleared", func() bool {
		a := agentByID(t, cli, id)
		return a.ToolsVersion == "v0.5.0" && !a.ToolsOld
	})
}

// An agent that runs an owner-only command is told what to use instead, and
// that a missing tool means old tools.
func TestInsideSessionAdvice(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa, realGate: true})
	info := fakeSession("in1", "inside")
	info.PID = os.Getpid()
	fa.setSessions(info)
	eventually(t, "session known", func() bool { return len(n.d.Sessions()) == 1 })
	cli := cliClient(t, n)
	_, err := cli.call(protocol.OpWorkerStart, protocol.WorkerStartRequest{Project: "p", Harness: "opencode", Task: "t"})
	if err == nil {
		t.Fatal("worker_start was allowed from inside a session")
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, `method "worker_start" is not available from inside an agent session`) {
		t.Errorf("the refusal reason must come first: %q", msg)
	}
	want := "agents start workers with the agent_start tool, not this command. If you do not have agent_start, " +
		"your agent-net tools are out of date: ask your user to reconnect agent-net (in Claude Code: /mcp)."
	if !strings.Contains(msg, want) {
		t.Errorf("message lacks the advice %q: %q", want, msg)
	}
	_, err = cli.call("counts", nil)
	if err == nil || !strings.Contains(err.Error(), "reconnect agent-net") {
		t.Errorf("other owner-only commands need the advice too: %v", err)
	}
}
