package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// replyLineTo writes result as the reply to a request on lc and returns the
// line the other end reads, the way the daemon sends it.
func replyLineTo(t *testing.T, lc *localConn, result any) string {
	t.Helper()
	client, server := net.Pipe()
	defer client.Close()
	lc.c = server
	go lc.respond(json.RawMessage(`1`), result, nil)
	line, err := bufio.NewReader(client).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	return line
}

// The CTO's resume of an OpenCode session returns the enter plan, whose env
// holds the owner service's password. The reply to the CTO's connection must
// keep the names and lose the value (docs/security.md, "Entering a session").
func TestCTOSessionResumeReplyHidesOpenCodePassword(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "opencode")
	n := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = bins })
	repo := newRepo(t, "shop")
	n.register(t, repo)
	native := "ses_cto_resume"
	id, err := g.gw.Store().UpsertSession(n.d.NodeID(), protocol.SessionInfo{Harness: "opencode", NativeID: native, Cwd: repo})
	if err != nil {
		t.Fatal(err)
	}
	g.gw.Store().SetSessionOffline(n.d.NodeID(), id)
	server := fakeOwnerOpenCodeLoad(t, repo, native)
	defer server.Close()
	writeOwnerOpenCodeService(t, state, server.URL)
	n.d.cto.pid.Store(int64(os.Getpid()))
	defer n.d.cto.pid.Store(0)
	cto := &localConn{d: n.d, client: "cto-mcp", peerPID: os.Getpid()}
	call := json.RawMessage(`{"name":"session_change","arguments":{"action":"resume","session_id":"` + id + `"}}`)
	result, err := cto.dispatch(context.Background(), "cto_tool", call)
	if err != nil {
		t.Fatal(err)
	}
	line := replyLineTo(t, cto, result)
	if !strings.Contains(line, `"enter_plan"`) || !strings.Contains(line, `"OPENCODE_SERVER_PASSWORD":"[redacted]"`) {
		t.Fatalf("CTO reply has no redacted enter plan: %s", line)
	}
	if strings.Contains(line, "test-owner-password") {
		t.Fatalf("CTO reply carries the OpenCode password: %s", line)
	}
}

// The owner's own command line and TUI start the client with the value, so
// their replies keep it. Agent connections get the names with a marker.
func TestReplyEnvValuesReachOnlyTheOwnerCommandLine(t *testing.T) {
	plan := protocol.StartedSession{ID: "start_env", Status: "started", EnterPlan: &protocol.EnterPlan{
		Argv: []string{"opencode", "--server", "http://127.0.0.1:1", "--session", "ses_env"},
		Env:  map[string]string{"OPENCODE_SERVER_PASSWORD": "test-owner-password"},
	}}
	owner := replyLineTo(t, &localConn{client: "cli"}, plan)
	if !strings.Contains(owner, `"OPENCODE_SERVER_PASSWORD":"test-owner-password"`) {
		t.Fatalf("owner reply lost the env value: %s", owner)
	}
	for _, client := range []string{"mcp", "cto-mcp"} {
		agent := replyLineTo(t, &localConn{client: client}, plan)
		if strings.Contains(agent, "test-owner-password") || !strings.Contains(agent, `"OPENCODE_SERVER_PASSWORD":"[redacted]"`) || !strings.Contains(agent, `"--session","ses_env"`) {
			t.Fatalf("%s reply: %s", client, agent)
		}
	}
}
