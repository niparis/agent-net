package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/mcpserver"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

// The four read tools filter, page and fetch in the daemon, once (principle
// 19). The MCP server forwards the tool input to the daemon method. A native
// plugin (Pi, OpenCode) sends the same input to the same method. These tests
// send one input down both paths on one daemon connection and expect one
// answer.

type parityRig struct {
	g      *testGateway
	n      *node
	md     *mcpserver.Daemon
	client *mcp.ClientSession
	me     string
	peer   *localClient
	peerID string
	// mid is a time after the first two messages and before the others.
	mid     string
	longMsg string
}

func newParityRig(t *testing.T) *parityRig {
	t.Helper()
	useHelper(t)
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{adapter: &fakeAdapter{harness: "fake"}, cmdline: "pi"})
	r := &parityRig{g: g, n: n}
	r.md = mcpserver.NewDaemon(n.sock, map[string]any{"client": "pi", "harness": "pi", "native_id": "me", "name": "me", "cwd": t.TempDir(), "status": "idle"})
	t.Cleanup(r.md.Close)
	ctx, cancel := context.WithTimeout(context.Background(), scaled(30*time.Second))
	t.Cleanup(cancel)
	st, ct := mcp.NewInMemoryTransports()
	server, err := mcpserver.New(r.md.Call, "").Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	r.client, err = mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.client.Close() })
	if _, err := r.md.Call(ctx, "info", nil); err != nil {
		t.Fatal(err)
	}
	eventually(t, "my session", func() bool {
		for _, s := range n.d.Sessions() {
			if s.NativeID == "me" && s.SessionID != "" {
				r.me = s.SessionID
			}
		}
		return r.me != ""
	})
	r.peer = dialLocal(t, n.sock)
	r.peer.AutoAck = true
	res := r.peer.mustCall("hello", map[string]any{"client": "pi", "harness": "pi", "native_id": "peer", "cwd": t.TempDir(), "status": "idle"})
	var h struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(res, &h)
	r.peerID = h.SessionID
	if r.peerID == "" {
		t.Fatal("peer has no session")
	}
	r.longMsg = strings.Repeat("0123456789", 60)
	// Two messages to me, then a time mark, then two more and one from me.
	r.send(t, r.peer, r.me, "first")
	r.send(t, r.peer, r.me, "second")
	time.Sleep(20 * time.Millisecond)
	r.mid = time.Now().UTC().Format(time.RFC3339Nano)
	time.Sleep(20 * time.Millisecond)
	r.send(t, r.peer, r.me, r.longMsg)
	r.send(t, r.peer, r.me, "fourth")
	if _, err := r.md.Call(ctx, "send", map[string]any{"to": r.peerID, "body": "from me", "type": "request", "idempotency_key": "k_me"}); err != nil {
		t.Fatal(err)
	}
	r.waitSettled(t)
	return r
}

// waitSettled waits until delivery has finished for every seeded message. The
// fake "me" plugin never confirms a push, so those messages go dispatched and
// then failed after the ack timeout. A read taken before that would compare
// two moments of the same message, not two paths.
func (r *parityRig) waitSettled(t *testing.T) {
	t.Helper()
	eventually(t, "every seeded message is settled", func() bool {
		items, _ := r.mcpPath(t, "agent_messages_find", map[string]any{"limit": 100})["items"].([]any)
		if len(items) != 5 {
			return false
		}
		for _, it := range items {
			switch it.(map[string]any)["state"] {
			case protocol.StateStored, protocol.StateDispatched:
				return false
			}
		}
		return true
	})
}

func (r *parityRig) send(t *testing.T, from *localClient, to, body string) {
	t.Helper()
	from.mustCall("send", map[string]any{"to": to, "body": body, "type": "request", "idempotency_key": fmt.Sprintf("k_%d_%s", time.Now().UnixNano(), body[:3])})
}

// mcpPath is the tool call of an MCP server.
func (r *parityRig) mcpPath(t *testing.T, tool string, input map[string]any) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), scaled(30*time.Second))
	defer cancel()
	res, err := r.client.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: input})
	if err != nil || res.IsError {
		text := ""
		if res != nil {
			for _, c := range res.Content {
				if item, ok := c.(*mcp.TextContent); ok {
					text += item.Text
				}
			}
		}
		t.Fatalf("MCP %s %v: %v %s", tool, input, err, text)
	}
	return normalizeRead(t, res.StructuredContent)
}

// pluginPath is what a native plugin sends: the catalog input to the catalog
// method, with the field names the generated catalog gives.
func (r *parityRig) pluginPath(t *testing.T, tool string, input map[string]any) map[string]any {
	t.Helper()
	method, fields := "", map[string]string{}
	plugin, err := toolcatalog.PluginTools()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range plugin {
		if p.Name == tool {
			method, fields = p.Method, p.DaemonFields
		}
	}
	if method == "" {
		t.Fatalf("%s is not a plugin tool", tool)
	}
	sent := map[string]any{}
	for k, v := range input {
		if name, ok := fields[k]; ok {
			k = name
		}
		sent[k] = v
	}
	ctx, cancel := context.WithTimeout(context.Background(), scaled(30*time.Second))
	defer cancel()
	raw, err := r.md.Call(ctx, method, sent)
	if err != nil {
		t.Fatalf("plugin %s via %s %v: %v", tool, method, sent, err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return normalizeRead(t, v)
}

// normalizeRead drops the fields that follow the clock, so two reads a few
// milliseconds apart compare equal.
func normalizeRead(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	var walk func(any)
	walk = func(x any) {
		switch x := x.(type) {
		case map[string]any:
			delete(x, "ago")
			delete(x, "run_time")
			for _, c := range x {
				walk(c)
			}
		case []any:
			for _, c := range x {
				walk(c)
			}
		}
	}
	walk(out)
	return out
}

func idsOf(page map[string]any) []string {
	var ids []string
	items, _ := page["items"].([]any)
	for _, it := range items {
		m, _ := it.(map[string]any)
		id, _ := m["id"].(string)
		ids = append(ids, id)
	}
	return ids
}

func sameRead(t *testing.T, r *parityRig, tool string, input map[string]any) map[string]any {
	t.Helper()
	a, b := r.mcpPath(t, tool, input), r.pluginPath(t, tool, input)
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Fatalf("%s %v differs between MCP and plugin\nMCP:    %s\nplugin: %s", tool, input, ja, jb)
	}
	return a
}

func TestMessagesFindSameForMCPAndPlugins(t *testing.T) {
	r := newParityRig(t)
	peerName := ""
	for _, s := range r.n.d.Sessions() {
		if s.SessionID == r.peerID {
			peerName = strings.ToUpper(s.Name)
		}
	}
	cases := []struct {
		name    string
		input   map[string]any
		wantLen int
	}{
		{"all", map[string]any{}, 5},
		{"incoming", map[string]any{"direction": "in"}, 4},
		{"outgoing", map[string]any{"direction": "out"}, 1},
		{"peer by name", map[string]any{"with": peerName}, 5},
		{"peer by id", map[string]any{"with": r.peerID, "direction": "in"}, 4},
		{"unknown peer", map[string]any{"with": "nobody"}, 0},
		{"since", map[string]any{"since": r.mid}, 3},
		{"first page", map[string]any{"limit": 2}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page := sameRead(t, r, "agent_messages_find", tc.input)
			if got := len(idsOf(page)); got != tc.wantLen {
				t.Fatalf("%d items, want %d: %v", got, tc.wantLen, page)
			}
		})
	}
	t.Run("cursor", func(t *testing.T) {
		first := sameRead(t, r, "agent_messages_find", map[string]any{"limit": 2})
		cursor, _ := first["next_cursor"].(string)
		if cursor == "" {
			t.Fatalf("no next_cursor: %v", first)
		}
		second := sameRead(t, r, "agent_messages_find", map[string]any{"limit": 2, "cursor": cursor})
		if len(idsOf(second)) != 2 || idsOf(second)[0] == idsOf(first)[0] {
			t.Fatalf("second page: %v after %v", idsOf(second), idsOf(first))
		}
	})
}

func TestMessageGetSameForMCPAndPlugins(t *testing.T) {
	r := newParityRig(t)
	var long string
	for _, row := range r.mcpPath(t, "agent_messages_find", map[string]any{"direction": "in"})["items"].([]any) {
		m := row.(map[string]any)
		if strings.HasPrefix(m["text"].(string), "0123456789") {
			long = m["id"].(string)
		}
	}
	if long == "" {
		t.Fatal("no long message")
	}
	whole := sameRead(t, r, "agent_message_get", map[string]any{"message": long, "max_bytes": 16384})
	if whole["body_truncated"] != false {
		t.Fatalf("whole body: %v", whole)
	}
	first := sameRead(t, r, "agent_message_get", map[string]any{"message": long, "max_bytes": 256})
	cursor, _ := first["next_body_cursor"].(string)
	if first["body_truncated"] != true || cursor == "" {
		t.Fatalf("first chunk: %v", first)
	}
	second := sameRead(t, r, "agent_message_get", map[string]any{"message": long, "max_bytes": 256, "body_cursor": cursor})
	if second["message"].(map[string]any)["body"] == first["message"].(map[string]any)["body"] {
		t.Fatalf("the second chunk repeats the first: %v", second)
	}
	// A message of another conversation is not readable by either path.
	if _, err := r.md.Call(context.Background(), "message_get", map[string]any{"message": "m_nope"}); err == nil {
		t.Fatal("an unknown message was read")
	}
}

func TestWorkersFindSameForMCPAndPlugins(t *testing.T) {
	r := newParityRig(t)
	st := r.g.gw.Store()
	for _, w := range []struct{ native, project, owner string }{{"w_alpha", "alpha", r.me}, {"w_beta", "beta", r.peerID}} {
		if _, err := st.UpsertSession(r.n.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: w.native, Cwd: "/src/" + w.project,
			Repo: protocol.RepoInfo{Root: "/src/" + w.project, Project: w.project}, Worker: &protocol.WorkerInfo{Project: w.project, Task: "t " + w.project, Owner: w.owner}}); err != nil {
			t.Fatal(err)
		}
	}
	host := r.n.d.hostName()
	cases := []struct {
		name  string
		input map[string]any
		want  int
	}{
		{"mine", map[string]any{"state": "any"}, 1},
		{"anyone", map[string]any{"started_by": "anyone", "state": "any"}, 2},
		{"project", map[string]any{"started_by": "anyone", "state": "any", "project": "BETA"}, 1},
		{"machine", map[string]any{"started_by": "anyone", "state": "any", "machine": host}, 2},
		{"other machine", map[string]any{"started_by": "anyone", "state": "any", "machine": "elsewhere"}, 0},
		{"first page", map[string]any{"started_by": "anyone", "state": "any", "limit": 1}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page := sameRead(t, r, "agent_workers_find", tc.input)
			if got := len(idsOf(page)); got != tc.want {
				t.Fatalf("%d workers, want %d: %v", got, tc.want, page)
			}
		})
	}
	t.Run("cursor", func(t *testing.T) {
		first := sameRead(t, r, "agent_workers_find", map[string]any{"started_by": "anyone", "state": "any", "limit": 1})
		cursor, _ := first["next_cursor"].(string)
		if cursor == "" {
			t.Fatalf("no next_cursor: %v", first)
		}
		second := sameRead(t, r, "agent_workers_find", map[string]any{"started_by": "anyone", "state": "any", "limit": 1, "cursor": cursor})
		if len(idsOf(second)) != 1 || idsOf(second)[0] == idsOf(first)[0] {
			t.Fatalf("second page: %v after %v", idsOf(second), idsOf(first))
		}
	})
	// With no filter a plugin gets the running workers it started, as MCP does.
	t.Run("default", func(t *testing.T) {
		sameRead(t, r, "agent_workers_find", map[string]any{})
	})
}

func TestMCPServersFindSameForMCPAndPlugins(t *testing.T) {
	r := newParityRig(t)
	cli := cliClient(t, r.n)
	for _, name := range []string{"probe-a", "probe-b"} {
		cli.mustCall("mcp_add", protocol.MCPAdd{Server: stdioSpec(name)})
	}
	eventually(t, "both servers online", func() bool {
		servers := listServers(t, cli)
		if len(servers) != 2 {
			return false
		}
		for _, s := range servers {
			if !s.HostOnline || len(s.Tools) == 0 {
				return false
			}
		}
		return true
	})
	page := sameRead(t, r, "agent_mcp_servers_find", map[string]any{})
	if got := len(page["items"].([]any)); got != 2 {
		t.Fatalf("%d servers: %v", got, page)
	}
	sameRead(t, r, "agent_mcp_servers_find", map[string]any{"usable_only": false})
	first := sameRead(t, r, "agent_mcp_servers_find", map[string]any{"limit": 1})
	cursor, _ := first["next_cursor"].(string)
	if cursor == "" {
		t.Fatalf("no next_cursor: %v", first)
	}
	second := sameRead(t, r, "agent_mcp_servers_find", map[string]any{"limit": 1, "cursor": cursor})
	if len(second["items"].([]any)) != 1 {
		t.Fatalf("second page: %v", second)
	}
}
