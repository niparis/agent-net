package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/testmcp/tools"
)

const helperEnv = "AGENT_NET_TEST_MCP_SERVER"

// TestMain lets the test binary double as a stdio MCP server: a hosted server
// is started with this binary and the helper variable set.
func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) != "" {
		if err := tools.New().Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			os.Exit(1)
		}
		return
	}
	// The test binary is also the fake claude, pi and codex of the worker tests.
	if runFakeHarness() {
		return
	}
	// The test binary is also the fake `opencode serve` of the worker tests.
	if len(os.Args) > 1 && os.Args[1] == "serve" && os.Getenv("OPENCODE_SERVER_PASSWORD") != "" {
		runFakeOpenCode()
		return
	}
	hideRealHerdr()
	// Do not run the programs of the machine the tests run on.
	defaultDetect = func(context.Context) []protocol.HarnessInfo { return nil }
	os.Exit(m.Run())
}

// useHelper makes the daemon (this process) pass the helper variable on to hosted servers.
func useHelper(t *testing.T) { t.Setenv(helperEnv, "1") }

func stdioSpec(name string, env ...string) protocol.MCPServer {
	return protocol.MCPServer{Name: name, Transport: protocol.MCPStdio, Command: os.Args[0],
		Args: []string{"-test.run=XXX"}, Env: append([]string{helperEnv}, env...)}
}

// cliClient connects to a node's socket as the CLI.
func cliClient(t *testing.T, n *node) *localClient {
	t.Helper()
	lc := dialLocal(t, n.sock)
	lc.mustCall("hello", map[string]any{"client": "cli"})
	return lc
}

type toolOut struct {
	Text    string
	IsError bool
}

func callTool(t *testing.T, lc *localClient, server, tool string, args any) (toolOut, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	res, err := lc.call("mcp_call", protocol.MCPCall{Server: server, Tool: tool, Arguments: raw})
	if err != nil {
		return toolOut{}, err
	}
	var r struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		t.Fatalf("bad result %s", res)
	}
	var out toolOut
	for _, c := range r.Content {
		out.Text += c.Text
	}
	out.IsError = r.IsError
	return out, nil
}

func listServers(t *testing.T, lc *localClient) []protocol.MCPServer {
	t.Helper()
	var r struct {
		Servers []protocol.MCPServer `json:"servers"`
	}
	json.Unmarshal(lc.mustCall("mcp_list", nil), &r)
	return r.Servers
}

func toolNames(s protocol.MCPServer) string {
	var n []string
	for _, t := range s.Tools {
		n = append(n, t.Name)
	}
	return strings.Join(n, ",")
}

func waitPush(t *testing.T, lc *localClient, method string) {
	t.Helper()
	deadline := time.After(20 * time.Second)
	for {
		select {
		case m := <-lc.Other:
			if m == method {
				return
			}
		case <-deadline:
			t.Fatalf("no %s push", method)
		}
	}
}

// A stdio server added by an agent on node A is called from node B. The
// registry changes reach B as pushes. Results are capped, calls time out,
// secrets are redacted, and a crashed server comes back.
func TestMCPStdioAcrossNodes(t *testing.T) {
	useHelper(t)
	g := startGateway(t, t.TempDir(), "")
	a := startNode(t, g, nodeOpts{cmdline: "pi"})
	b := startNode(t, g, nodeOpts{cmdline: "pi"})
	a.d.mcp.backMin, a.d.mcp.backMax = 20*time.Millisecond, 100*time.Millisecond
	agentA, sidA := helloPi(t, a, "na", "/work/a", false)
	agentB, _ := helloPi(t, b, "nb", "/work/b", false)

	t.Setenv("TEST_SECRET_TOKEN", "s3cr3t-value-123")
	srv := stdioSpec("probe", "TEST_SECRET_TOKEN")
	// A long limit, so a slow machine does not time out the ordinary calls. The
	// timeout check below lowers it for one call.
	srv.TimeoutSec = 30
	// "on" from an agent is ignored: the server lands on the agent's own node.
	res := agentA.mustCall("mcp_add", protocol.MCPAdd{Server: srv, On: b.d.NodeID()})
	var added struct {
		Server protocol.MCPServer `json:"server"`
	}
	json.Unmarshal(res, &added)
	if added.Server.NodeID != a.d.NodeID() {
		t.Fatalf("hosted on %s, want A %s", added.Server.NodeID, a.d.NodeID())
	}
	if added.Server.AddedBySession != sidA || added.Server.AddedByHarness != "pi" {
		t.Fatalf("added_by not recorded: %+v", added.Server)
	}
	if got := toolNames(added.Server); got != "big,crash,echo,env,pid,sleep" {
		t.Fatalf("tools = %s", got)
	}
	waitPush(t, agentB, "mcp_changed")

	// B sees the tools through mcp_tools and calls one; A hosts the process.
	var tl struct {
		Servers []protocol.MCPServer `json:"servers"`
	}
	json.Unmarshal(agentB.mustCall("mcp_tools", nil), &tl)
	if len(tl.Servers) != 1 || tl.Servers[0].Name != "probe" {
		t.Fatalf("mcp_tools = %+v", tl.Servers)
	}
	out, err := callTool(t, agentB, "probe", "echo", map[string]string{"text": "hi"})
	if err != nil || out.Text != "echo: hi" || out.IsError {
		t.Fatalf("echo = %+v, %v", out, err)
	}

	// Timeout: a second server with a limit of 1 s, so the limit of "probe"
	// stays long. It goes away again when the check is done.
	slow := stdioSpec("slow")
	slow.TimeoutSec = 1
	agentA.mustCall("mcp_add", protocol.MCPAdd{Server: slow})
	waitPush(t, agentB, "mcp_changed")
	start := time.Now()
	_, err = callTool(t, agentB, "slow", "sleep", map[string]float64{"seconds": 20})
	if err == nil || !strings.Contains(err.Error(), "timed out") || time.Since(start) > 15*time.Second {
		t.Fatalf("sleep: err=%v after %s", err, time.Since(start))
	}
	agentA.mustCall("mcp_remove", protocol.MCPName{Name: "slow"})
	waitPush(t, agentB, "mcp_changed")

	// Size cap.
	out, err = callTool(t, agentB, "probe", "big", map[string]int{"bytes": protocol.MCPMaxResultBytes + 64<<10})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Text) > protocol.MCPMaxResultBytes || !strings.Contains(out.Text, "the result was cut") {
		t.Fatalf("big result not capped: %d bytes", len(out.Text))
	}

	// Redaction: the server returns the secret it was given; it never arrives.
	out, err = callTool(t, agentB, "probe", "env", map[string]string{"name": "TEST_SECRET_TOKEN"})
	if err != nil || strings.Contains(out.Text, "s3cr3t-value-123") || !strings.Contains(out.Text, redacted) {
		t.Fatalf("env = %+v, %v", out, err)
	}

	// Crash: the call fails, the host restarts the process, a later call works.
	out1, _ := callTool(t, agentB, "probe", "pid", map[string]string{})
	callTool(t, agentB, "probe", "crash", map[string]string{})
	eventually(t, "server restarted with a new pid", func() bool {
		o, err := callTool(t, agentB, "probe", "pid", map[string]string{})
		return err == nil && !o.IsError && o.Text != "" && o.Text != out1.Text
	})

	// Disable removes the tools from mcp_tools and refuses calls; enable brings them back.
	cli := cliClient(t, b)
	cli.mustCall("mcp_enable", protocol.MCPSetEnabled{Name: "probe", Enabled: false})
	waitPush(t, agentB, "mcp_changed")
	json.Unmarshal(agentB.mustCall("mcp_tools", nil), &tl)
	if len(tl.Servers) != 0 {
		t.Fatalf("disabled server still in mcp_tools")
	}
	if _, err := callTool(t, agentB, "probe", "echo", map[string]string{"text": "x"}); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("call to disabled server: %v", err)
	}
	cli.mustCall("mcp_enable", protocol.MCPSetEnabled{Name: "probe", Enabled: true})
	eventually(t, "enabled again", func() bool {
		o, err := callTool(t, agentB, "probe", "echo", map[string]string{"text": "again"})
		return err == nil && o.Text == "echo: again"
	})

	// Remove: the server stops and the tools go.
	cli.mustCall("mcp_remove", protocol.MCPName{Name: "probe"})
	waitPush(t, agentB, "mcp_changed")
	if len(listServers(t, cli)) != 0 {
		t.Fatal("server still listed after remove")
	}
	if a.d.mcp.runner("probe") != nil {
		t.Fatal("host still runs the removed server")
	}
}

// The CLI may host a stdio server on another node with "on".
func TestMCPCliOn(t *testing.T) {
	useHelper(t)
	g := startGateway(t, t.TempDir(), "")
	a := startNode(t, g, nodeOpts{})
	b := startNode(t, g, nodeOpts{})
	cli := cliClient(t, b)
	cli.mustCall("mcp_add", protocol.MCPAdd{Server: stdioSpec("onbox"), On: "host-" + a.d.NodeID()})
	srvs := listServers(t, cli)
	if len(srvs) != 1 || srvs[0].NodeID != a.d.NodeID() || srvs[0].AddedByHarness != "cli" || !srvs[0].HostOnline {
		t.Fatalf("servers = %+v", srvs)
	}
	if b.d.mcp.runner("onbox") != nil {
		t.Fatal("B must not run a server hosted on A")
	}
	eventually(t, "A runs it", func() bool { return a.d.mcp.runner("onbox") != nil })
	if _, err := cli.call("mcp_add", protocol.MCPAdd{Server: stdioSpec("onbox")}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate name: %v", err)
	}
	if _, err := cli.call("mcp_add", protocol.MCPAdd{Server: stdioSpec("Bad_Name")}); err == nil {
		t.Fatal("bad name accepted")
	}
	if _, err := cli.call("mcp_add", protocol.MCPAdd{Server: stdioSpec("x"), On: "no-such-host"}); err == nil {
		t.Fatal("unknown host accepted")
	}
}

// A server that cannot start is not saved, and the error is plain.
func TestMCPAddValidates(t *testing.T) {
	useHelper(t)
	g := startGateway(t, t.TempDir(), "")
	a := startNode(t, g, nodeOpts{})
	cli := cliClient(t, a)
	bad := protocol.MCPServer{Name: "broken", Transport: protocol.MCPStdio, Command: "/nonexistent/program"}
	if _, err := cli.call("mcp_add", protocol.MCPAdd{Server: bad}); err == nil || !strings.Contains(err.Error(), "could not use the server") {
		t.Fatalf("err = %v", err)
	}
	if n := len(listServers(t, cli)); n != 0 {
		t.Fatalf("%d servers saved after a failed add", n)
	}
	// NAME=value for an env var is refused without echoing the value.
	leaky := stdioSpec("leaky", "TOKEN=hunter2hunter2")
	_, err := cli.call("mcp_add", protocol.MCPAdd{Server: leaky})
	if err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("err = %v", err)
	}
}

func bearerServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	srv := tools.New()
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts
}

// A streamable HTTP server with a bearer token read from a file on the host.
// A wrong token gives a clear error that holds no secret, before and after saving.
func TestMCPHTTPBearerFromFile(t *testing.T) {
	const token, wrong = "right-token-abcdef", "wrong-token-zzzzzz"
	ts := bearerServer(t, token)
	g := startGateway(t, t.TempDir(), "")
	a := startNode(t, g, nodeOpts{cmdline: "pi"})
	b := startNode(t, g, nodeOpts{cmdline: "pi"})
	agentA, _ := helloPi(t, a, "na", "/work/a", false)
	agentB, _ := helloPi(t, b, "nb", "/work/b", false)

	file := filepath.Join(t.TempDir(), "token")
	os.WriteFile(file, []byte(wrong+"\n"), 0o600)
	spec := protocol.MCPServer{Name: "homelab", Transport: protocol.MCPHTTP, URL: ts.URL,
		Headers: []protocol.MCPHeader{{Name: "Authorization", File: file}}}
	_, err := agentA.call("mcp_add", protocol.MCPAdd{Server: spec})
	if err == nil || !strings.Contains(err.Error(), "rejected the credentials") || strings.Contains(err.Error(), wrong) {
		t.Fatalf("wrong token: err = %v", err)
	}
	if n := len(listServers(t, agentA)); n != 0 {
		t.Fatalf("saved %d servers with a wrong token", n)
	}

	os.WriteFile(file, []byte(token+"\n"), 0o600)
	agentA.mustCall("mcp_add", protocol.MCPAdd{Server: spec})
	out, err := callTool(t, agentB, "homelab", "echo", map[string]string{"text": "net"})
	if err != nil || out.Text != "echo: net" {
		t.Fatalf("echo = %+v, %v", out, err)
	}

	// The token on the host changes to a wrong one; the next test is clear and clean.
	os.WriteFile(file, []byte(wrong+"\n"), 0o600)
	cli := cliClient(t, b)
	res := cli.mustCall("mcp_test", protocol.MCPName{Name: "homelab"})
	if !strings.Contains(string(res), "rejected the credentials") || strings.Contains(string(res), wrong) || strings.Contains(string(res), token) {
		t.Fatalf("mcp_test = %s", res)
	}
	list, _ := json.Marshal(listServers(t, cli))
	if strings.Contains(string(list), token) || strings.Contains(string(list), wrong) {
		t.Fatalf("a token is in the list output: %s", list)
	}
}

// A daemon starts only the servers registered with its own node.
func TestMCPHostRunsOnlyOwnServers(t *testing.T) {
	useHelper(t)
	g := startGateway(t, t.TempDir(), "")
	a := startNode(t, g, nodeOpts{})
	other := stdioSpec("foreign")
	other.NodeID, other.Enabled = "n_someoneelse", true
	own := stdioSpec("mine")
	own.NodeID, own.Enabled = a.d.NodeID(), true
	a.d.mcp.reconcile([]protocol.MCPServer{other, own})
	if a.d.mcp.runner("foreign") != nil {
		t.Fatal("started a server registered for another node")
	}
	if a.d.mcp.runner("mine") == nil {
		t.Fatal("did not start its own server")
	}
	// A server removed from the sync is stopped.
	a.d.mcp.reconcile(nil)
	if a.d.mcp.runner("mine") != nil {
		t.Fatal("still running after it left the sync")
	}
}
