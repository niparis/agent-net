package daemon

import (
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

// labelledServer is the test MCP server over streamable HTTP, in this process.
func labelledServer(t *testing.T, label string) *httptest.Server {
	t.Helper()
	srv := tools.NewWith(label)
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil))
	// The host keeps a stream open; Close alone would wait for it until the
	// daemon stops, which runs later.
	t.Cleanup(func() { ts.CloseClientConnections(); ts.Close() })
	return ts
}

func httpSpec(name, url string) protocol.MCPServer {
	return protocol.MCPServer{Name: name, Transport: protocol.MCPHTTP, URL: url}
}

// featureList calls mcp_feature for a list and returns the answer by server.
func featureList(t *testing.T, lc *localClient, op string) map[string]protocol.MCPServerItems {
	t.Helper()
	var r struct {
		Servers []protocol.MCPServerItems `json:"servers"`
	}
	if err := json.Unmarshal(lc.mustCall("mcp_feature", protocol.MCPFeature{Op: op}), &r); err != nil {
		t.Fatal(err)
	}
	out := map[string]protocol.MCPServerItems{}
	for _, s := range r.Servers {
		out[s.Server] = s
	}
	return out
}

// itemKeys returns the uri, uriTemplate or name of each item.
func itemKeys(s protocol.MCPServerItems) string {
	var items []map[string]any
	json.Unmarshal(s.Items, &items)
	var keys []string
	for _, it := range items {
		for _, k := range []string{"uri", "uriTemplate", "name"} {
			if v, ok := it[k].(string); ok {
				keys = append(keys, v)
				break
			}
		}
	}
	return strings.Join(keys, ",")
}

type readOut struct {
	Contents []struct {
		URI      string `json:"uri"`
		MIMEType string `json:"mimeType"`
		Text     string `json:"text"`
		Blob     []byte `json:"blob"`
	} `json:"contents"`
}

func readResource(t *testing.T, lc *localClient, server, uri string) (readOut, error) {
	t.Helper()
	var out readOut
	raw, err := lc.call("mcp_feature", protocol.MCPFeature{Op: protocol.MCPOpRead, Server: server, URI: uri})
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("bad read answer %s", raw)
	}
	return out, nil
}

func getPrompt(t *testing.T, lc *localClient, server, name string, args map[string]string) (string, error) {
	t.Helper()
	raw, err := lc.call("mcp_feature", protocol.MCPFeature{Op: protocol.MCPOpPrompt, Server: server, Name: name, Arguments: args})
	if err != nil {
		return "", err
	}
	var r struct {
		Messages []struct {
			Content struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &r); err != nil || len(r.Messages) == 0 {
		t.Fatalf("bad prompt answer %s", raw)
	}
	return r.Messages[0].Content.Text, nil
}

func drainPushes(lc *localClient) {
	for {
		select {
		case <-lc.Other:
		default:
			return
		}
	}
}

// Two servers on machine A with the same resource URIs and prompt names are
// listed, read and used from a session on machine B, each under its own
// name. Blobs keep their mime type, an oversize read is refused, and a
// server's list_changed reaches the session at once, not only when the
// host's short cache runs out.
func TestMCPFeaturesEndToEnd(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	// The host's list cache lasts longer than the test: a list that changes
	// proves the notification.
	a := startNode(t, g, nodeOpts{cmdline: "pi", cfg: func(c *Config) { c.MCPListTTL = time.Hour }})
	b := startNode(t, g, nodeOpts{cmdline: "pi"})
	agentB, _ := helloPi(t, b, "nb", "/work/b", false)
	cliA := cliClient(t, a)
	cliA.mustCall("mcp_add", protocol.MCPAdd{Server: httpSpec("one", labelledServer(t, "one").URL)})
	cliA.mustCall("mcp_add", protocol.MCPAdd{Server: httpSpec("two", labelledServer(t, "two").URL)})

	res := featureList(t, agentB, protocol.MCPOpResources)
	for _, name := range []string{"one", "two"} {
		s := res[name]
		if s.Error != "" || s.Host != a.d.cfg.Hostname || itemKeys(s) != "test://big,test://hello,test://logo.png" {
			t.Fatalf("resources of %s = %+v (%s)", name, s, itemKeys(s))
		}
	}
	if s := featureList(t, agentB, protocol.MCPOpTemplates)["two"]; itemKeys(s) != "test://add/{name},test://item/{id}" {
		t.Fatalf("templates of two = %s", itemKeys(s))
	}
	if s := featureList(t, agentB, protocol.MCPOpPrompts)["one"]; itemKeys(s) != "greet" {
		t.Fatalf("prompts of one = %s", itemKeys(s))
	}

	// The same URI on two servers gives each server's own text.
	for _, name := range []string{"one", "two"} {
		out, err := readResource(t, agentB, name, "test://hello")
		if err != nil || len(out.Contents) != 1 || out.Contents[0].Text != "hello from "+name || out.Contents[0].MIMEType != "text/plain" {
			t.Fatalf("read hello on %s = %+v, %v", name, out, err)
		}
	}
	out, err := readResource(t, agentB, "two", "test://item/9")
	if err != nil || out.Contents[0].Text != "item 9 from two" {
		t.Fatalf("template read = %+v, %v", out, err)
	}
	out, err = readResource(t, agentB, "one", "test://logo.png")
	if err != nil || out.Contents[0].MIMEType != "image/png" || string(out.Contents[0].Blob) != "\x89PNG\x00\x01\x02\xff" {
		t.Fatalf("blob read = %+v, %v", out, err)
	}
	_, err = readResource(t, agentB, "one", "test://big")
	if err == nil || !strings.Contains(err.Error(), "more than the limit") || !strings.Contains(err.Error(), "test://big") {
		t.Fatalf("oversize read: %v", err)
	}
	if _, err = readResource(t, agentB, "one", "test://nothing"); err == nil {
		t.Fatal("read of an unknown resource: want an error")
	}

	text, err := getPrompt(t, agentB, "two", "greet", map[string]string{"name": "Ann"})
	if err != nil || text != "Hello, Ann! (from two)" {
		t.Fatalf("prompt = %q, %v", text, err)
	}
	if _, err := getPrompt(t, agentB, "two", "nope", nil); err == nil {
		t.Fatal("unknown prompt: want an error")
	}

	// The server adds a resource and sends list_changed: the session is told
	// and the next list has it, though the host's cache would hold the old one.
	drainPushes(agentB)
	if out, err := readResource(t, agentB, "one", "test://add/zed"); err != nil || out.Contents[0].Text != "added test://extra/zed" {
		t.Fatalf("add read = %+v, %v", out, err)
	}
	waitPush(t, agentB, "mcp_changed")
	eventually(t, "the new resource is listed", func() bool {
		return strings.Contains(itemKeys(featureList(t, agentB, protocol.MCPOpResources)["one"]), "test://extra/zed")
	})
	if strings.Contains(itemKeys(featureList(t, agentB, protocol.MCPOpResources)["two"]), "extra") {
		t.Fatal("the change of one shows on two")
	}

	// Bad requests are refused before any host is asked.
	for _, bad := range []protocol.MCPFeature{
		{Op: "tools/call", Server: "one"},
		{Op: protocol.MCPOpRead, Server: "one"},
		{Op: protocol.MCPOpPrompt, Server: "one"},
		{Op: protocol.MCPOpRead, Server: "Bad_Name", URI: "test://hello"},
		{Op: protocol.MCPOpRead, Server: "one", URI: strings.Repeat("x", protocol.MCPMaxURILen+1)},
	} {
		if _, err := agentB.call("mcp_feature", bad); err == nil {
			t.Fatalf("%+v: want an error", bad)
		}
	}
	if _, err := readResource(t, agentB, "ghost", "test://hello"); err == nil || !strings.Contains(err.Error(), `no MCP server named "ghost"`) {
		t.Fatalf("unknown server: %v", err)
	}
}

// Resources and prompts follow the same access rules as tools: a server
// scoped to one project is not listed to another project's session, and its
// reads and prompts are refused at the gateway, whatever the request claims.
// A disabled server is refused. An offline host is named in the list and in
// the error, and the other servers still list.
func TestMCPFeaturesAccessAndOffline(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	host := startNode(t, g, nodeOpts{cmdline: "pi"})
	work := startNode(t, g, nodeOpts{cmdline: "pi"})
	other := startNode(t, g, nodeOpts{cmdline: "pi"})
	root := t.TempDir()
	repos := map[string]string{}
	for _, name := range []string{"alpha", "beta"} {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		gitIn(t, dir, "init", "-q", "-b", "main")
		repos[name] = dir
	}
	cli := cliClient(t, work)
	cli.mustCall("project_add", protocol.ProjectAddRequest{Paths: []string{repos["alpha"], repos["beta"]}})
	alpha, alphaID := helloPi(t, work, "na", repos["alpha"], false)
	beta, _ := helloPi(t, work, "nb", repos["beta"], false)

	cliClient(t, host).mustCall("mcp_add", protocol.MCPAdd{Server: httpSpec("secret", labelledServer(t, "secret").URL)})
	cliClient(t, other).mustCall("mcp_add", protocol.MCPAdd{Server: httpSpec("open", labelledServer(t, "open").URL)})

	// Assign "secret" to alpha.
	var page protocol.ToolsPage
	json.Unmarshal(cli.mustCall("tools_items", map[string]any{"kind": "mcp"}), &page)
	var item protocol.ToolItem
	for _, it := range page.Items {
		if it.Name == "secret" {
			item = it
		}
	}
	var plan protocol.ToolPlan
	json.Unmarshal(cli.mustCall("tools_plan", protocol.ToolPlanRequest{Item: item.ID,
		Target: protocol.ToolAssignment{Scope: "projects", Projects: []string{"alpha"}}}), &plan)
	cli.mustCall("tools_apply", protocol.ToolPlanID{ID: plan.ID})

	for _, op := range []string{protocol.MCPOpResources, protocol.MCPOpTemplates, protocol.MCPOpPrompts} {
		if _, ok := featureList(t, alpha, op)["secret"]; !ok {
			t.Fatalf("%s: alpha does not see secret", op)
		}
		got := featureList(t, beta, op)
		if _, ok := got["secret"]; ok {
			t.Fatalf("%s: beta sees secret", op)
		}
		if _, ok := got["open"]; !ok {
			t.Fatalf("%s: beta does not see open", op)
		}
	}
	if out, err := readResource(t, alpha, "secret", "test://hello"); err != nil || out.Contents[0].Text != "hello from secret" {
		t.Fatalf("alpha read = %+v, %v", out, err)
	}
	if _, err := readResource(t, beta, "secret", "test://hello"); err == nil || !strings.Contains(err.Error(), "not assigned to this project") {
		t.Fatalf("beta read: %v", err)
	}
	if _, err := getPrompt(t, beta, "secret", "greet", map[string]string{"name": "x"}); err == nil || !strings.Contains(err.Error(), "not assigned to this project") {
		t.Fatalf("beta prompt: %v", err)
	}
	// Naming one server in a list gets the same check.
	if _, err := beta.call("mcp_feature", protocol.MCPFeature{Op: protocol.MCPOpResources, Server: "secret"}); err == nil || !strings.Contains(err.Error(), "not assigned to this project") {
		t.Fatalf("beta named list: %v", err)
	}
	// Claiming alpha's session or the owner does not help.
	eventually(t, "owner session", func() bool { return work.d.ownerSessionID() != "" })
	for _, claim := range []map[string]any{{"session_id": alphaID}, {"owner": work.d.ownerSessionID()}, {"project": "alpha"}} {
		req := map[string]any{"op": protocol.MCPOpRead, "server": "secret", "uri": "test://hello"}
		for k, v := range claim {
			req[k] = v
		}
		if _, err := beta.call("mcp_feature", req); err == nil || !strings.Contains(err.Error(), "not assigned to this project") {
			t.Fatalf("claim %v: %v", claim, err)
		}
	}
	// The owner's command line has the owner's authority.
	if out, err := readResource(t, cli, "secret", "test://hello"); err != nil || out.Contents[0].Text != "hello from secret" {
		t.Fatalf("owner read = %+v, %v", out, err)
	}
	if _, ok := featureList(t, cli, protocol.MCPOpResources)["secret"]; !ok {
		t.Fatal("the owner's list does not show secret")
	}

	// Disabled: not listed, and a read says so.
	cli.mustCall("mcp_enable", protocol.MCPSetEnabled{Name: "open", Enabled: false})
	if _, ok := featureList(t, beta, protocol.MCPOpResources)["open"]; ok {
		t.Fatal("a disabled server is listed")
	}
	if _, err := readResource(t, beta, "open", "test://hello"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled read: %v", err)
	}
	cli.mustCall("mcp_enable", protocol.MCPSetEnabled{Name: "open", Enabled: true})

	// The host of "secret" goes offline: alpha's list names it with the
	// reason and still has "open"; a read says the host is offline.
	host.d.Stop()
	eventually(t, "host offline", func() bool {
		for _, s := range listServers(t, cli) {
			if s.Name == "secret" {
				return !s.HostOnline
			}
		}
		return false
	})
	got := featureList(t, alpha, protocol.MCPOpResources)
	if s := got["secret"]; !strings.Contains(s.Error, "offline") || len(s.Items) != 0 {
		t.Fatalf("offline server in the list = %+v", s)
	}
	if s := got["open"]; s.Error != "" || !strings.Contains(itemKeys(s), "test://hello") {
		t.Fatalf("open with a host offline elsewhere = %+v", s)
	}
	if _, err := readResource(t, alpha, "secret", "test://hello"); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline read: %v", err)
	}
}
