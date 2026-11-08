package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
)

const botPublicHost = "bots.example.test"

type botHostTransport struct{ token string }

func (x botHostTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Host = botPublicHost
	r.Header.Set("Authorization", "Bearer "+x.token)
	return http.DefaultTransport.RoundTrip(r)
}

// botClient connects a new bot app to the gateway's public /mcp.
func botClient(t *testing.T, g *testGateway, base, name string) (gateway.App, *mcp.ClientSession) {
	t.Helper()
	app, err := g.gw.Store().CreateApp(name, "bot", "client-"+name, "Client "+name, []string{"https://" + botPublicHost + "/cb"})
	if err != nil {
		t.Fatal(err)
	}
	token, err := g.gw.Store().IssueToken(app.ID, "access", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	c := mcp.NewClient(&mcp.Implementation{Name: name, Version: "1"}, nil)
	sess, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: base + "/mcp",
		HTTPClient: &http.Client{Transport: botHostTransport{token}}, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	return app, sess
}

func botToolNames(t *testing.T, sess *mcp.ClientSession) []string {
	t.Helper()
	res, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

func hasTool(names []string, prefix string) []string {
	var out []string
	for _, n := range names {
		if strings.HasPrefix(n, prefix) {
			out = append(out, n)
		}
	}
	return out
}

func botEcho(sess *mcp.ClientSession, tool string) (string, bool, error) {
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: map[string]any{"text": "hi"}})
	if err != nil {
		return "", true, err
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	return text, res.IsError, nil
}

// The owner grants one bot the use of one shared MCP server. The bot then sees
// and calls only the allowed tools of that server, through the host daemon.
// Another bot, and the same bot after the grant is removed, see nothing.
func TestBotMCPServerGrant(t *testing.T) {
	useHelper(t)
	g := startGatewayWith(t, t.TempDir(), "", 0, func(o *gateway.Options) { o.PublicURL = "https://" + botPublicHost })
	host := startNode(t, g, nodeOpts{cmdline: "pi"})
	cli := cliClient(t, host)
	srv := stdioSpec("probe")
	srv.AllowedTools = []string{"echo"}
	srv.TimeoutSec = 30
	cli.mustCall("mcp_add", protocol.MCPAdd{Server: srv})
	other := stdioSpec("secret")
	other.TimeoutSec = 30
	cli.mustCall("mcp_add", protocol.MCPAdd{Server: other})

	ts := httptest.NewServer(g.gw.AppsHandler())
	t.Cleanup(ts.Close)
	trade, tradeSess := botClient(t, g, ts.URL, "trade-bot")
	_, otherSess := botClient(t, g, ts.URL, "other-bot")

	// Without a grant: no tool of any shared server, and a call is unknown.
	if got := hasTool(botToolNames(t, tradeSess), "probe__"); len(got) != 0 {
		t.Fatalf("ungranted bot sees %v", got)
	}
	if _, isErr, err := botEcho(tradeSess, "probe__echo"); err == nil && !isErr {
		t.Fatal("ungranted bot called probe__echo")
	}

	// The owner grants it, from the TUI, through the daemon.
	owner := cliClient(t, host)
	owner.mustCall("apps.grant", map[string]string{"id": trade.ID, "grant": "mcp server probe"})

	got := botToolNames(t, tradeSess)
	if p := hasTool(got, "probe__"); len(p) != 1 || p[0] != "probe__echo" {
		t.Fatalf("granted bot sees %v, want only probe__echo (all: %v)", p, got)
	}
	if s := hasTool(got, "secret__"); len(s) != 0 {
		t.Fatalf("a server that was not granted shows: %v", s)
	}
	if text, isErr, err := botEcho(tradeSess, "probe__echo"); err != nil || isErr || text != "echo: hi" {
		t.Fatalf("granted call = %q %v %v", text, isErr, err)
	}
	// A tool outside the allow-list and a server that was not granted are unknown.
	if _, isErr, err := botEcho(tradeSess, "probe__pid"); err == nil && !isErr {
		t.Fatal("bot called a tool outside the allow-list")
	}
	if _, isErr, err := botEcho(tradeSess, "secret__echo"); err == nil && !isErr {
		t.Fatal("bot called a server that was not granted")
	}
	// Other bots are unaffected.
	if p := hasTool(botToolNames(t, otherSess), "probe__"); len(p) != 0 {
		t.Fatalf("other bot sees %v", p)
	}
	if _, isErr, err := botEcho(otherSess, "probe__echo"); err == nil && !isErr {
		t.Fatal("other bot called probe__echo")
	}
	// The grant shows on the app's record.
	apps, err := g.gw.Store().ListApps()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range apps {
		if a.ID == trade.ID && (len(a.Grants) != 1 || a.Grants[0] != "mcp server probe") {
			t.Fatalf("grants = %v", a.Grants)
		}
	}

	// Removing the grant hides the tools again at once.
	owner.mustCall("apps.ungrant", map[string]string{"id": trade.ID, "grant": "mcp server probe"})
	if p := hasTool(botToolNames(t, tradeSess), "probe__"); len(p) != 0 {
		t.Fatalf("after removal the bot sees %v", p)
	}
	if _, isErr, err := botEcho(tradeSess, "probe__echo"); err == nil && !isErr {
		t.Fatal("bot called probe__echo after the grant was removed")
	}
}
