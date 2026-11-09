package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

// botNamedClient connects like botClient, with an optional X-Agent-Net-As
// header on every request and a clientInfo name.
func botNamedClient(t *testing.T, url, token, header, clientName string) *mcp.ClientSession {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: clientName, Version: "1"}, nil)
	base := botTokenTransport{token, http.DefaultTransport}
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if header != "" {
			r = r.Clone(r.Context())
			r.Header.Set("X-Agent-Net-As", header)
		}
		return base.RoundTrip(r)
	})
	s, err := c.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: url + "/mcp", HTTPClient: &http.Client{Transport: transport}, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// sendFrom sends one message to "to" and returns the stored sender session.
func sendFrom(t *testing.T, g *Gateway, bot *mcp.ClientSession, to string, args map[string]any) string {
	t.Helper()
	args["to"] = to
	args["body"] = "hello"
	out := botCall(t, bot, "agent_message_send", args)
	id := out["sent"].([]any)[0].(map[string]any)["message_id"].(string)
	m, ok, err := g.store.Message(id)
	if err != nil || !ok {
		t.Fatalf("message %s: %v %v", id, ok, err)
	}
	return m.From
}

func sessionName(t *testing.T, g *Gateway, sid string) string {
	t.Helper()
	var name string
	if err := g.store.db.QueryRow(`SELECT name FROM sessions WHERE id=?`, sid).Scan(&name); err != nil {
		t.Fatal(err)
	}
	return name
}

// Reproduction: two names on one app are two senders, and a call without a name
// keeps the shared one.
func TestBotNamesSendFromOwnSession(t *testing.T) {
	g, app, token, _ := botFixture(t)
	local, _ := twoSessions(t, g.store)
	server := httptest.NewServer(testAppsHandler(g))
	defer server.Close()
	bot := botClient(t, server.URL, token)

	shared, err := g.store.touchBot(app)
	if err != nil {
		t.Fatal(err)
	}
	cto := sendFrom(t, g, bot, local, map[string]any{"as": "CTO"})
	shop := sendFrom(t, g, bot, local, map[string]any{"as": "Shopping"})
	plain := sendFrom(t, g, bot, local, map[string]any{})
	again := sendFrom(t, g, bot, local, map[string]any{"as": "cto"})
	if cto == shop {
		t.Fatalf("CTO and Shopping both send from %s", cto)
	}
	if plain != shared || cto == shared || shop == shared {
		t.Fatalf("shared %s, plain %s, CTO %s, Shopping %s", shared, plain, cto, shop)
	}
	if again != cto {
		t.Fatalf("name is case-insensitive: %s vs %s", again, cto)
	}
	if got := sessionName(t, g, cto); got != "CTO (test bot)" {
		t.Fatalf("session name %q", got)
	}
	// Each name has its own agent node, a child of the app's node.
	var role string
	var tomb int
	var display string
	if err := g.store.db.QueryRow(`SELECT a.role,a.tombstone,a.display_name FROM sessions s JOIN agents a ON a.id=s.agent_id WHERE s.id=?`, cto).Scan(&role, &tomb, &display); err != nil {
		t.Fatal(err)
	}
	if role != "bot" || tomb != 0 || display != "CTO (test bot)" {
		t.Fatalf("agent node %q %d %q", role, tomb, display)
	}
	var agent string
	if err := g.store.db.QueryRow(`SELECT agent_id FROM sessions WHERE id=?`, cto).Scan(&agent); err != nil || !strings.HasPrefix(agent, app.ID+"/") {
		t.Fatalf("agent id %q is not a child of %s: %v", agent, app.ID, err)
	}
}

// A reply goes to the name that asked, and each name reads only its own mail.
func TestBotNamesReplyAndInboxPerName(t *testing.T) {
	g, _, token, _ := botFixture(t)
	local, _ := twoSessions(t, g.store)
	server := httptest.NewServer(testAppsHandler(g))
	defer server.Close()
	bot := botClient(t, server.URL, token)

	cto := sendFrom(t, g, bot, local, map[string]any{"as": "CTO"})
	sendFrom(t, g, bot, local, map[string]any{"as": "Shopping"})
	// The local session answers the CTO name only.
	reply, _, err := g.store.StoreMessage(protocol.MsgSend{Sender: local, Recipient: cto, Body: "for the CTO"})
	if err != nil {
		t.Fatal(err)
	}
	if reply.To != cto {
		t.Fatalf("reply to %s", reply.To)
	}
	inbox := func(as string) string {
		args := map[string]any{"direction": "in"}
		if as != "" {
			args["as"] = as
		}
		b, _ := json.Marshal(botCall(t, bot, "agent_messages_find", args))
		return string(b)
	}
	if got := inbox("CTO"); !strings.Contains(got, "for the CTO") {
		t.Fatalf("CTO inbox: %s", got)
	}
	if got := inbox("Shopping"); strings.Contains(got, "for the CTO") {
		t.Fatalf("Shopping sees the CTO mail: %s", got)
	}
	if got := inbox(""); strings.Contains(got, "for the CTO") {
		t.Fatalf("shared inbox sees the CTO mail: %s", got)
	}
	// Another name cannot reply to, or read, the CTO's message.
	r, err := bot.CallTool(t.Context(), &mcp.CallToolParams{Name: "agent_message_reply", Arguments: map[string]any{"message": reply.ID, "body": "stolen", "as": "Shopping"}})
	if err != nil || !r.IsError {
		t.Fatalf("Shopping replied to the CTO's mail: %+v %v", r, err)
	}
	r, err = bot.CallTool(t.Context(), &mcp.CallToolParams{Name: "agent_message_get", Arguments: map[string]any{"message": reply.ID, "as": "Shopping"}})
	if err != nil || !r.IsError {
		t.Fatalf("Shopping read the CTO's mail: %+v %v", r, err)
	}
	out := botCall(t, bot, "agent_message_reply", map[string]any{"message": reply.ID, "body": "thanks", "as": "CTO"})
	id := out["message_id"].(string)
	m, _, _ := g.store.Message(id)
	if m.From != cto || m.To != local {
		t.Fatalf("reply from %s to %s, want from %s to %s", m.From, m.To, cto, local)
	}
}

// The name may also come from a header or from clientInfo. A per-call as wins.
func TestBotNameFromHeaderAndClientInfo(t *testing.T) {
	g, _, token, _ := botFixture(t)
	local, _ := twoSessions(t, g.store)
	server := httptest.NewServer(testAppsHandler(g))
	defer server.Close()
	viaHeader := botNamedClient(t, server.URL, token, "Devops", "test")
	viaInfo := botNamedClient(t, server.URL, token, "", "as:Trade")
	plain := botClient(t, server.URL, token)

	header := sendFrom(t, g, viaHeader, local, map[string]any{})
	info := sendFrom(t, g, viaInfo, local, map[string]any{})
	shared := sendFrom(t, g, plain, local, map[string]any{})
	perCall := sendFrom(t, g, viaHeader, local, map[string]any{"as": "Shopping"})
	if got := sessionName(t, g, header); got != "Devops (test bot)" {
		t.Fatalf("header name %q", got)
	}
	if got := sessionName(t, g, info); got != "Trade (test bot)" {
		t.Fatalf("clientInfo name %q", got)
	}
	if got := sessionName(t, g, perCall); got != "Shopping (test bot)" {
		t.Fatalf("per-call name %q", got)
	}
	if got := sessionName(t, g, shared); got != "test bot" {
		t.Fatalf("a client name that is not marked keeps the shared session, got %q", got)
	}
}

func TestBotNameFormat(t *testing.T) {
	g, _, token, _ := botFixture(t)
	local, _ := twoSessions(t, g.store)
	server := httptest.NewServer(testAppsHandler(g))
	defer server.Close()
	bot := botClient(t, server.URL, token)
	for _, name := range []string{"", "   ", strings.Repeat("x", 33), "a/b", "a:b", "emoji 😀", "a\nb", "a(b)"} {
		r, err := bot.CallTool(t.Context(), &mcp.CallToolParams{Name: "agent_message_send", Arguments: map[string]any{"to": local, "body": "x", "as": name}})
		if err != nil || !r.IsError {
			t.Fatalf("name %q accepted: %+v %v", name, r, err)
		}
	}
	for _, name := range []string{"a", "My Bot-1_x", strings.Repeat("x", 32), "Café"} {
		sendFrom(t, g, bot, local, map[string]any{"as": name})
	}
}

// Revoking the app archives every named node with it; renaming the app renames
// the suffix of every named session and node. Nothing is deleted.
func TestBotNamesRevokeAndRename(t *testing.T) {
	g, app, token, _ := botFixture(t)
	local, _ := twoSessions(t, g.store)
	server := httptest.NewServer(testAppsHandler(g))
	defer server.Close()
	bot := botClient(t, server.URL, token)
	cto := sendFrom(t, g, bot, local, map[string]any{"as": "CTO"})
	if err := g.store.RenameApp(app.ID, "grok"); err != nil {
		t.Fatal(err)
	}
	var display string
	if err := g.store.db.QueryRow(`SELECT a.display_name FROM sessions s JOIN agents a ON a.id=s.agent_id WHERE s.id=?`, cto).Scan(&display); err != nil {
		t.Fatal(err)
	}
	if got := sessionName(t, g, cto); got != "CTO (grok)" || display != "CTO (grok)" {
		t.Fatalf("after rename: session %q node %q", got, display)
	}
	if err := g.store.RevokeApp(app.ID); err != nil {
		t.Fatal(err)
	}
	var tomb int
	if err := g.store.db.QueryRow(`SELECT a.tombstone FROM sessions s JOIN agents a ON a.id=s.agent_id WHERE s.id=?`, cto).Scan(&tomb); err != nil || tomb != 1 {
		t.Fatalf("named node tombstone = %d: %v", tomb, err)
	}
	var n int
	if err := g.store.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id=?`, cto).Scan(&n); err != nil || n != 1 {
		t.Fatalf("named session deleted: %d %v", n, err)
	}
	if _, err := g.store.touchBotNamed(app, "CTO"); err == nil {
		t.Fatal("a revoked app got a named session")
	}
}

func TestListAppsNamesTheBotNames(t *testing.T) {
	g, app, token, _ := botFixture(t)
	local, _ := twoSessions(t, g.store)
	server := httptest.NewServer(testAppsHandler(g))
	defer server.Close()
	bot := botClient(t, server.URL, token)
	sendFrom(t, g, bot, local, map[string]any{"as": "Shopping"})
	sendFrom(t, g, bot, local, map[string]any{"as": "CTO"})
	apps, err := g.store.ListApps()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range apps {
		if a.ID == app.ID {
			if strings.Join(a.Names, ",") != "CTO,Shopping" {
				t.Fatalf("names %v", a.Names)
			}
			return
		}
	}
	t.Fatal("app missing")
}

func TestBotToolSchemaOffersAs(t *testing.T) {
	g, _, token, _ := botFixture(t)
	server := httptest.NewServer(testAppsHandler(g))
	defer server.Close()
	bot := botClient(t, server.URL, token)
	tools, err := bot.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		b, _ := json.Marshal(tool.InputSchema)
		if !strings.Contains(string(b), `"as"`) {
			t.Fatalf("%s has no as argument: %s", tool.Name, b)
		}
	}
}

// A name is a child node in the Graph. It carries the app's details and draws
// no calls edge of its own.
func TestBotNameNodeInGraph(t *testing.T) {
	s, app := botAgentFixture(t)
	if err := s.SetAppGrant(app.ID, protocol.MCPServerGrant("ingest-forge"), true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.touchBotNamed(app, "CTO"); err != nil {
		t.Fatal(err)
	}
	g, err := s.GraphForRef(nil)
	if err != nil {
		t.Fatal(err)
	}
	var child, parent *protocol.Node
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Ref != nil && n.Ref.Kind == "agent" && n.Ref.ID == app.ID+"/cto" {
			child = n
		}
		if n.Ref != nil && n.Ref.Kind == "agent" && n.Ref.ID == app.ID {
			parent = n
		}
	}
	if child == nil || parent == nil || child.Bot == nil || child.Bot.Parent != app.ID || parent.Bot == nil || parent.Bot.Parent != "" {
		t.Fatalf("child %+v parent %+v", child, parent)
	}
	if got := strings.Join(child.Bot.CanCall, ","); got != "ingest-forge" {
		t.Fatalf("child can call %q", got)
	}
	for _, e := range g.Edges {
		if e.Kind == "calls" && e.From == app.ID+"/cto" {
			t.Fatalf("calls edge from the named node: %+v", e)
		}
	}
}
