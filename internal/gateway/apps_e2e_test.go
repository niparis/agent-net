package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/phone"
	"github.com/niparis/agent-net/internal/protocol"
)

type e2ePhoneOperator struct{ actions chan protocol.HeldAnswer }

func (*e2ePhoneOperator) Snapshot(context.Context) (operator.Snapshot, error) {
	return operator.Snapshot{Connected: true}, nil
}
func (*e2ePhoneOperator) SessionText(context.Context, protocol.TextQuery) (protocol.TextPage, error) {
	return protocol.TextPage{}, nil
}
func (o *e2ePhoneOperator) AnswerHeld(_ context.Context, q protocol.HeldAnswer) error {
	o.actions <- q
	return nil
}
func (*e2ePhoneOperator) ResolveOutcome(context.Context, string, string) error { return nil }
func (*e2ePhoneOperator) SendAsOwner(context.Context, string, string) (protocol.MessageSent, error) {
	return protocol.MessageSent{}, nil
}
func (*e2ePhoneOperator) Call(context.Context, string, any) (json.RawMessage, error) {
	return json.RawMessage(`{"items":[]}`), nil
}

type e2eTransport struct{ token string }

func (x e2eTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Host = "test.example.ts.net:8443"
	if x.token != "" {
		r.Header.Set("Authorization", "Bearer "+x.token)
	}
	return http.DefaultTransport.RoundTrip(r)
}
func TestAppsEndToEnd(t *testing.T) {
	runner := &publicRunner{}
	phoneOperator := &e2ePhoneOperator{actions: make(chan protocol.HeldAnswer, 1)}
	g, err := New(Options{DataDir: seededDataDir(t), AppsListen: "127.0.0.1:0", FunnelRunner: runner, PhoneApp: phone.New(phoneOperator)})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	ownerNode, _, err := g.store.CreateNode("owner-test", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.store.UpsertSession(ownerNode, owner("owner-test")); err != nil {
		t.Fatal(err)
	}
	ownerCall := func(op string, v any) {
		t.Helper()
		raw, _ := json.Marshal(v)
		if _, err := g.handle(t.Context(), &nodeConn{id: ownerNode}, op, raw); err != nil {
			t.Fatal(err)
		}
	}
	publicCall(t, g, "apps.public.on", nil)
	base := "http://" + g.publicAddr
	client := &http.Client{Transport: e2eTransport{}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(method, path, body, contentType, origin string, cookie *http.Cookie) (*http.Response, string) {
		t.Helper()
		req, err := http.NewRequest(method, base+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return resp, string(b)
	}
	decode := func(body string) map[string]any {
		t.Helper()
		var v map[string]any
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			t.Fatalf("decode %s: %v", body, err)
		}
		return v
	}
	callback := "https://www.cursor.com/agents/mcp/oauth/callback"
	resp, body := request("POST", "/oauth/register", `{"client_name":"Cursor","redirect_uris":["https://www.cursor.com/agents/mcp/oauth/callback","http://localhost:8787/callback"],"token_endpoint_auth_method":"none","grant_types":["authorization_code","refresh_token"]}`, "application/json", "", nil)
	if resp.StatusCode != 201 {
		t.Fatalf("register %d %s", resp.StatusCode, body)
	}
	reg := decode(body)
	uris := reg["redirect_uris"].([]any)
	if len(uris) != 2 || uris[0] != callback || uris[1] != "http://localhost:8787/callback" {
		t.Fatal(reg)
	}
	clientID := reg["client_id"].(string)
	q := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {callback}, "code_challenge": {oauthChallenge(oauthVerifier)}, "code_challenge_method": {"S256"}}
	resp, body = request("GET", "/oauth/authorize?"+q.Encode(), "", "", "", nil)
	code := oauthPageCode.FindStringSubmatch(body)
	id := oauthPageID.FindStringSubmatch(body)
	if resp.StatusCode != 200 || len(code) != 2 || len(id) != 2 {
		t.Fatalf("authorize %d %s", resp.StatusCode, body)
	}
	ownerCall("apps.pending", map[string]string{"code": code[1]})
	ownerCall("apps.approve", map[string]string{"app_id": id[1], "kind": "bot", "name": "grok"})
	_, body = request("GET", "/oauth/authorize/status?id="+id[1], "", "", "", nil)
	status := decode(body)
	u, err := url.Parse(status["redirect"].(string))
	if err != nil || u.Query().Get("code") == "" {
		t.Fatal(status, err)
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {u.Query().Get("code")}, "client_id": {clientID}, "redirect_uri": {callback}, "code_verifier": {oauthVerifier}}
	resp, body = request("POST", "/oauth/token", form.Encode(), "application/x-www-form-urlencoded", "", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("token %d %s", resp.StatusCode, body)
	}
	tokens := decode(body)
	access := tokens["access_token"].(string)
	refresh := tokens["refresh_token"].(string)
	mc := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "1"}, nil)
	bot, err := mc.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: base + "/mcp", HTTPClient: &http.Client{Transport: e2eTransport{token: access}}, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bot.Close()
	tools, err := bot.ListTools(t.Context(), nil)
	if err != nil || len(tools.Tools) != 11 {
		t.Fatalf("tools %+v %v", tools, err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	wantTools := []string{"agent_graph_read", "agent_machine_get", "agent_machines_find", "agent_message_get", "agent_message_reply", "agent_message_send", "agent_messages_find", "agent_projects_find", "agent_session_get", "agent_sessions_find", "agent_settings_read"}
	if !reflect.DeepEqual(names, wantTools) {
		t.Fatalf("bot tools: %v", names)
	}
	local, _ := twoSessions(t, g.store)
	node, err := g.store.SessionNode(local)
	if err != nil {
		t.Fatal(err)
	}
	connections := make(chan *websocket.Conn, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, e := websocket.Accept(w, r, nil)
		if e == nil {
			connections <- ws
		}
	}))
	defer wsServer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	wsClient, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer wsClient.CloseNow()
	ws := <-connections
	defer ws.CloseNow()
	g.mu.Lock()
	g.conns[node] = &nodeConn{id: node, ws: ws, cancel: func() {}}
	g.mu.Unlock()
	sent := botCall(t, bot, "agent_message_send", map[string]any{"to": local, "body": "hello"})
	msgID := sent["sent"].([]any)[0].(map[string]any)["message_id"].(string)
	_, raw, err := wsClient.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var delivery protocol.MsgDeliver
	if err = json.Unmarshal(raw, &delivery); err != nil {
		t.Fatal(err)
	}
	msg := delivery.Message
	if msg.ID != msgID || !strings.Contains(msg.ApprovalNote, "from grok, a bot") {
		t.Fatalf("delivery %+v", msg)
	}
	raw, _ = json.Marshal(protocol.MsgSend{Sender: local, Recipient: msg.From, Body: "reply from session", ParentID: msg.ID})
	if _, err = g.handle(ctx, &nodeConn{id: node}, "msg.send", raw); err != nil {
		t.Fatal(err)
	}
	found := botCall(t, bot, "agent_messages_find", map[string]any{})
	b, _ := json.Marshal(found)
	if !strings.Contains(string(b), "reply from session") {
		t.Fatalf("messages %s", b)
	}
	refreshForm := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}}
	resp, body = request("POST", "/oauth/token", refreshForm.Encode(), "application/x-www-form-urlencoded", "", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("refresh %d %s", resp.StatusCode, body)
	}
	rotated := decode(body)["access_token"].(string)
	resp, _ = request("POST", "/oauth/token", refreshForm.Encode(), "application/x-www-form-urlencoded", "", nil)
	if resp.StatusCode != 400 {
		t.Fatalf("refresh replay %d", resp.StatusCode)
	}
	tokenRequest := func(token string) int {
		req, _ := http.NewRequest("POST", base+"/mcp", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+token)
		res, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if got := tokenRequest(rotated); got != 401 {
		t.Fatalf("replay access %d", got)
	}
	resp, body = request("GET", "/", "", "", "", nil)
	pc := phoneCodeRE.FindStringSubmatch(body)
	pi := phoneIDRE.FindStringSubmatch(body)
	if resp.StatusCode != 200 || len(pc) != 2 || len(pi) != 2 {
		t.Fatalf("phone page %d %s", resp.StatusCode, body)
	}
	ownerCall("apps.pending", map[string]string{"code": pc[1]})
	ownerCall("apps.approve", map[string]string{"app_id": pi[1], "kind": "owner_app", "name": "phone"})
	resp, body = request("GET", "/phone/status?id="+pi[1], "", "", "", nil)
	cookies := resp.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("phone poll %d %s", resp.StatusCode, body)
	}
	cookie := cookies[0]
	resp, body = request("GET", "/", "", "", "", cookie)
	if resp.StatusCode != 200 || !strings.Contains(body, "<!doctype html>") {
		t.Fatalf("phone home %d %s", resp.StatusCode, body)
	}
	resp, _ = request("POST", "/action", "kind=held&id=m_test&action=a", "application/x-www-form-urlencoded", "https://test.example.ts.net:8443", cookie)
	if resp.StatusCode != 303 {
		t.Fatalf("phone action %d", resp.StatusCode)
	}
	select {
	case action := <-phoneOperator.actions:
		if action.MessageID != "m_test" || action.Action != "a" {
			t.Fatal(action)
		}
	default:
		t.Fatal("phone action not delivered")
	}
	resp, _ = request("POST", "/action", "", "", "https://evil.example", cookie)
	if resp.StatusCode != 403 && resp.StatusCode != 421 {
		t.Fatalf("wrong origin %d", resp.StatusCode)
	}
	apps, err := g.store.ListApps()
	if err != nil || len(apps) != 2 {
		t.Fatalf("apps %v %v", apps, err)
	}
	var botID, phoneID string
	for _, a := range apps {
		if a.Kind == "bot" {
			botID = a.ID
		} else {
			phoneID = a.ID
		}
	}
	// The refresh replay invalidated the OAuth family; use a fresh token to
	// independently exercise immediate app revocation.
	fresh, err := g.store.IssueToken(botID, "access", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got := tokenRequest(fresh); got == 401 {
		t.Fatal("fresh token rejected")
	}
	appSessionPools.Lock()
	pool := appSessionPools.byStore[g.store]
	appSessionPools.Unlock()
	pool.mu.Lock()
	before := len(pool.live())
	pool.mu.Unlock()
	if before == 0 {
		t.Fatal("MCP session was not live before revoke")
	}
	publicCall(t, g, "apps.revoke", map[string]string{"app_id": botID})
	if got := tokenRequest(fresh); got != 401 {
		t.Fatalf("revoked access %d", got)
	}
	pool.mu.Lock()
	after := len(pool.live())
	pool.mu.Unlock()
	if after != 0 {
		t.Fatalf("MCP session still live after revoke: %d", after)
	}
	publicCall(t, g, "apps.revoke", map[string]string{"app_id": phoneID})
	if s := publicCall(t, g, "apps.public.status", nil); s["on"] != false {
		t.Fatalf("public remains on: %v", s)
	}
	if !strings.Contains(strings.Join(runner.calls, "\n"), "funnel --https=8443 off") {
		t.Fatal(runner.calls)
	}
	// The last revoke closed the listener. Restart it locally to check the
	// cookie without turning the public switch back on.
	if err := g.startPublicListener(); err != nil {
		t.Fatal(err)
	}
	base = "http://" + g.publicAddr
	resp, body = request("GET", "/", "", "", "", cookie)
	if resp.StatusCode != 200 || !phoneCodeRE.MatchString(body) {
		t.Fatalf("revoked phone %d %s", resp.StatusCode, body)
	}
}
