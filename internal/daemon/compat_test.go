package daemon

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/protocol"
)

// An older daemon sends a hello without harnesses and sessions without repo
// info. The new gateway must accept both and show the node as "not reported".
func TestOldDaemonAgainstNewGateway(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	cfgDir := t.TempDir()
	if _, err := Join(context.Background(), g.joinString(), cfgDir); err != nil {
		t.Fatal(err)
	}
	nc, ca, err := LoadNode(cfgDir)
	if err != nil {
		t.Fatal(err)
	}
	hc, _ := PinnedClient(ca)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, nc.GatewayURL+"/v1/node", &websocket.DialOptions{
		HTTPClient: hc, HTTPHeader: http.Header{"Authorization": {"Bearer " + nc.Token}}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")
	call := func(frame string) map[string]json.RawMessage {
		t.Helper()
		if err := ws.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
			t.Fatal(err)
		}
		for {
			_, raw, err := ws.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]json.RawMessage
			json.Unmarshal(raw, &m)
			if string(m["type"]) != `"result"` {
				continue // a push from the gateway, such as mcp.sync
			}
			if string(m["ok"]) != "true" {
				t.Fatalf("%s -> %s", frame, raw)
			}
			return m
		}
	}
	call(`{"type":"hello","id":"1","version":"v0.3.2","hostname":"oldbox"}`)
	call(`{"type":"session.sync","id":"2","sessions":[{"harness":"claude","native_id":"n1","pid":1,"cwd":"/work/legacy","name":"legacy","status":"idle","delivery":"native-claude"}]}`)
	m := call(`{"type":"query.nodes","id":"3"}`)
	var info protocol.NodesInfo
	json.Unmarshal(m["data"], &info)
	if len(info.Nodes) != 1 || info.Nodes[0].Hostname != "oldbox" || info.Nodes[0].Harnesses != nil {
		t.Fatalf("nodes: %+v", info.Nodes)
	}
	m = call(`{"type":"query.projects","id":"4"}`)
	var pr struct {
		Projects []protocol.ProjectView `json:"projects"`
	}
	json.Unmarshal(m["data"], &pr)
	if len(pr.Projects) != 1 || pr.Projects[0].Name != "legacy" || pr.Projects[0].Path != "/work/legacy" {
		t.Fatalf("projects: %+v", pr.Projects)
	}
	if len(g.logs.lines("event=session.new", "name=legacy")) != 1 {
		t.Fatalf("no session.new line: %v", g.logs.lines())
	}
}

// fakeOldGateway speaks the protocol of a gateway from before the inventory:
// it accepts hello and session frames (ignoring fields it does not know) and
// answers "unknown frame type" to node.harnesses and query.projects.
func fakeOldGateway(t *testing.T) (url string, helloSeen func() map[string]json.RawMessage) {
	t.Helper()
	var mu sync.Mutex
	var hello map[string]json.RawMessage
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		for {
			_, raw, err := ws.Read(ctx)
			if err != nil {
				return
			}
			var m map[string]json.RawMessage
			json.Unmarshal(raw, &m)
			var typ, id string
			json.Unmarshal(m["type"], &typ)
			json.Unmarshal(m["id"], &id)
			reply := map[string]any{"type": "result", "id": id, "ok": true}
			switch typ {
			case "hello":
				mu.Lock()
				hello = m
				mu.Unlock()
				reply["data"] = map[string]string{"node_id": "n_old"}
			case "session.sync":
				reply["data"] = map[string]any{"sessions": []any{}}
			case "session.upsert":
				reply["data"] = map[string]string{"session_id": "s_old"}
			default:
				reply["ok"] = false
				reply["error"] = `unknown frame type "` + typ + `"`
			}
			b, _ := json.Marshal(reply)
			ws.Write(ctx, websocket.MessageText, b)
		}
	}))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return srv.URL, func() map[string]json.RawMessage {
		mu.Lock()
		defer mu.Unlock()
		return hello
	}
}

func TestNewDaemonAgainstOldGateway(t *testing.T) {
	url, helloSeen := fakeOldGateway(t)
	detected := make(chan struct{}, 4)
	fa := &fakeAdapter{harness: "fake"}
	cfg := Config{GatewayURL: url, NodeID: "n_old", Token: "t", Hostname: "newbox", StateDir: t.TempDir(),
		SocketPath: filepath.Join(shortDir(t), "d.sock"), BackoffMin: 20 * time.Millisecond, BackoffMax: 100 * time.Millisecond,
		Adapters: []Adapter{fa}, HarnessInterval: 50 * time.Millisecond,
		DetectHarnesses: func(context.Context) []protocol.HarnessInfo {
			select {
			case detected <- struct{}{}:
			default:
			}
			return []protocol.HarnessInfo{{Name: "pi", Path: "/u/pi", Version: "1"}}
		}}
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	eventually(t, "connected to the old gateway", d.Connected)
	fa.setSessions(fakeSession("na", "alice"))
	eventually(t, "session registered", func() bool { return fa.host.SessionID("fake", "na") == "s_old" })
	// Several detections run; each update is refused and the link stays up.
	for i := 0; i < 3; i++ {
		select {
		case <-detected:
		case <-time.After(2 * time.Second):
			t.Fatal("detection did not run again")
		}
	}
	time.Sleep(100 * time.Millisecond)
	if !d.Connected() {
		t.Fatal("the link dropped after the gateway refused node.harnesses")
	}
	eventually(t, "hello seen", func() bool { return helloSeen() != nil })

	// A request the old gateway does not know is a plain error for the caller.
	cli := dialLocal(t, cfg.SocketPath)
	cli.mustCall("hello", map[string]any{"client": "cli"})
	if _, err := cli.call("projects", nil); err == nil || !strings.Contains(err.Error(), "unknown frame type") {
		t.Fatalf("projects against an old gateway: %v", err)
	}
}
