package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/integrations"
	"github.com/niparis/agent-net/internal/mcpserver"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/quotaaxi"
	"github.com/niparis/agent-net/internal/testmcp/tools"
	"github.com/niparis/agent-net/internal/tui"
)

const helperEnv = "AGENT_NET_TEST_MCP_SERVER"

// TestMain lets the test binary double as a stdio MCP server: a hosted server
// is started with this binary and the helper variable set.
func TestMain(m *testing.M) {
	// A test daemon must never run npm. The quota tests use a fake binary.
	ensureQuotaAXI = func(context.Context, func(string, ...any)) error { return nil }

	if len(os.Args) > 1 && os.Args[1] == "fake-tui" {
		if err := tui.Run(context.Background(), tui.NewDaemonSource(os.Getenv("AGENT_NET_SOCKET"))); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if os.Getenv(helperEnv) != "" {
		if err := tools.New().Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "cto-mcp" {
		if err := mcpserver.RunCTO(context.Background(), os.Getenv("AGENT_NET_SOCKET"), "test"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	// The test binary is also the fake claude, pi and codex of the worker tests.
	if runFakeHarness() {
		return
	}
	if len(os.Args) > 2 && os.Args[1] == "auth" {
		if os.Args[2] == "export" {
			os.Stdout.WriteString("[]")
		} else if os.Args[2] == "import" && os.Getenv("FAKE_OC_AUTH_RECORD") == "1" {
			data, _ := io.ReadAll(os.Stdin)
			if string(data) != "[]" {
				os.Exit(1)
			}
			if err := os.WriteFile(filepath.Join(os.Getenv("XDG_DATA_HOME"), "fake-imported-auth"), data, 0o600); err != nil {
				os.Exit(1)
			}
		}
		return
	}
	// The test binary is also the fake `opencode serve` of the worker tests.
	if len(os.Args) > 1 && os.Args[1] == "serve" && os.Getenv("OPENCODE_SERVER_PASSWORD") != "" {
		runFakeOpenCode()
		return
	}
	// The race runtime has already initialized in this process. Child test
	// binaries do not need its one-second exit delay.
	os.Setenv("GORACE", "atexit_sleep_ms=0")
	quotaaxi.SetDiscoveryRootForTesting(os.TempDir())
	quotaaxi.SetSystemNodeDirsForTesting(nil)
	hideRealHerdr()
	// Do not run the programs of the machine the tests run on.
	defaultDetect = func(context.Context) []protocol.HarnessInfo { return nil }
	fakes, err := os.MkdirTemp("", "agent-net-fakes.")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Setenv(envFakePids, fakes)
	// OpenCode status checks must never discover the machine's shared service.
	os.Setenv("XDG_STATE_HOME", filepath.Join(fakes, "state"))
	// Test daemons must not find the machine's quota collector. It can start
	// real harnesses that outlive its parent and write into a test's home.
	bin := filepath.Join(fakes, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(bin, "quota-axi"), []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then echo 0.1.69; else printf '{}\\n'; fi\n"), 0700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.WriteFile(filepath.Join(bin, "npm"), []byte("#!/bin/sh\nexit 0\n"), 0700)
	os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\necho v22.19.0\n"), 0700)
	os.WriteFile(filepath.Join(bin, "shell"), []byte("#!/bin/sh\np=${PATH%%:*}\nprintf '%s\\0' \"$p\" '' '' '' '' '' '' '' ''\n"), 0700)
	os.Setenv("SHELL", filepath.Join(bin, "shell"))
	os.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	os.Setenv("HOME", filepath.Join(fakes, "home"))
	os.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(fakes, "claude-config"))
	config := filepath.Join(fakes, "config")
	answers := integrations.AllOn()
	answers.QuotaAXI = false
	if err := integrations.SaveApplied(config, answers); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Setenv("AGENT_NET_CONFIG_DIR", config)
	os.Setenv("AGENT_NET_STATE_DIR", filepath.Join(fakes, "state"))
	code := m.Run()
	if leaked := reapFakes(fakes); len(leaked) > 0 && code == 0 {
		fmt.Fprintf(os.Stderr, "FAIL: fake harness processes still alive after the tests (killed): %v\n", leaked)
		code = 1
	}
	os.RemoveAll(fakes)
	os.Exit(code)
}

// useHelper makes the daemon (this process) pass the helper variable on to hosted servers.
func useHelper(t *testing.T) { t.Setenv(helperEnv, "1") }

func stdioSpec(name string, env ...string) protocol.MCPServer {
	return protocol.MCPServer{Name: name, Transport: protocol.MCPStdio, Command: os.Args[0],
		Args: []string{"-test.run=XXX"}, Env: append([]string{helperEnv, "GORACE"}, env...)}
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
	deadline := time.After(scaled(20 * time.Second))
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

func TestMCPPingHealth(t *testing.T) {
	for _, unsupported := range []bool{false, true} {
		t.Run(fmt.Sprint("unsupported=", unsupported), func(t *testing.T) {
			ctx := context.Background()
			ct, st := mcp.NewInMemoryTransports()
			server := tools.New()
			validPing := make(chan bool, 1)
			if unsupported {
				server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
					return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
						if method == "ping" {
							return nil, fmt.Errorf("method not found")
						}
						return next(ctx, method, req)
					}
				})
			}
			ss, err := server.Connect(ctx, st, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ss.Close()
			client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
			client.AddSendingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
				return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
					if method == "ping" && !unsupported {
						p, ok := req.GetParams().(*mcp.PingParams)
						validPing <- (ok && p.Meta[mcp.MetaKeyProtocolVersion] == "2026-07-28")
					}
					return next(ctx, method, req)
				}
			})
			version := "2026-07-28"
			if unsupported {
				version = "2025-11-25"
			}
			cs, err := client.Connect(ctx, ct, &mcp.ClientSessionOptions{ProtocolVersion: version})
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			if err := pingHealth(ctx, cs); err != nil {
				t.Fatal(err)
			}
			if !unsupported {
				select {
				case valid := <-validPing:
					if !valid {
						t.Fatal("ping missing negotiated version")
					}
				default:
					t.Fatal("ping was not received")
				}
			}
		})
	}
}
