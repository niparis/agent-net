package gateway

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/niparis/agent-net/internal/tsnode"
)

// fakeTSNode stands in for the embedded Tailscale node. Its Funnel listener
// is a plain loopback listener, so tests never reach Tailscale.
type fakeTSNode struct {
	mu         sync.Mutex
	needsLogin bool
	ups        int
	listens    []int
	lns        []net.Listener
	loggedOut  bool
	closed     int
}

func (n *fakeTSNode) Up(context.Context) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ups++
	if n.needsLogin {
		return "", &tsnode.NeedsLoginError{URL: "https://login.tailscale.com/a/test123"}
	}
	return "agent-net.example.ts.net", nil
}
func (n *fakeTSNode) ListenFunnel(port int) (net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	n.mu.Lock()
	n.listens = append(n.listens, port)
	n.lns = append(n.lns, ln)
	n.mu.Unlock()
	return ln, nil
}
func (n *fakeTSNode) Logout(context.Context) error { n.loggedOut = true; return nil }
func (n *fakeTSNode) Close() error {
	n.mu.Lock()
	n.closed++
	n.mu.Unlock()
	return nil
}
func (n *fakeTSNode) lastAddr() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.lns[len(n.lns)-1].Addr().String()
}

func nodeGateway(t *testing.T, dir string, node *fakeTSNode, runner *publicRunner) *Gateway {
	t.Helper()
	g, err := New(Options{DataDir: dir, AppsListen: "127.0.0.1:0", FunnelRunner: runner,
		TailscaleNode: func(stateDir string, c tsnode.Choice) (tsnode.Node, error) {
			if stateDir != tsnode.StateDir(dir) || c.Name != "agent-net" {
				t.Errorf("open %q %+v", stateDir, c)
			}
			return node, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestAppsPublicOwnTailscaleNode(t *testing.T) {
	dir := t.TempDir()
	if err := tsnode.SaveChoice(dir, tsnode.Choice{Name: "agent-net"}); err != nil {
		t.Fatal(err)
	}
	node := &fakeTSNode{}
	runner := &publicRunner{}
	g := nodeGateway(t, dir, node, runner)
	on := publicCall(t, g, "apps.public.on", nil)
	if on["on"] != true || on["url"] != "https://agent-net.example.ts.net" || on["node"] != "agent-net" || on["port"] != float64(0) || on["error"] != "" {
		t.Fatalf("status: %v", on)
	}
	if len(node.listens) != 1 || node.listens[0] != 443 {
		t.Fatalf("funnel listens %v", node.listens)
	}
	for _, call := range runner.calls {
		if strings.HasPrefix(call, "funnel") {
			t.Fatalf("the machine's Funnel was changed: %v", runner.calls)
		}
	}
	// The Funnel listener serves the connected-apps handler on the new name.
	req, _ := http.NewRequest("GET", "http://"+node.lastAddr()+"/mcp", nil)
	req.Host = "agent-net.example.ts.net"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.Contains(resp.Header.Get("WWW-Authenticate"), "https://agent-net.example.ts.net/") {
		t.Fatalf("header %v", resp.Header)
	}
	// Routes on the machine's own name do not share this name.
	status := publicCall(t, g, "apps.public.status", nil)
	if routes, _ := status["other_routes"].([]any); len(routes) != 0 {
		t.Fatalf("other routes: %v", status["other_routes"])
	}
	if _, err := os.Stat(filepath.Join(tsnode.StateDir(dir), "agent-net-owned.json")); err != nil {
		t.Fatal("ownership marker missing:", err)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if node.closed == 0 {
		t.Fatal("node not closed with the gateway")
	}

	// A restart restores the Funnel listener on the node.
	g = nodeGateway(t, dir, node, runner)
	if s := publicCall(t, g, "apps.public.status", nil); s["on"] != true || s["url"] != "https://agent-net.example.ts.net" || len(node.listens) != 2 {
		t.Fatalf("restore: %v listens=%v", s, node.listens)
	}
	addr := node.lastAddr()
	off := publicCall(t, g, "apps.public.off", nil)
	if off["on"] != false {
		t.Fatalf("off: %v", off)
	}
	if conn, err := net.Dial("tcp", addr); err == nil {
		conn.Close()
		t.Fatal("Funnel listener still open after off")
	}
	if node.loggedOut {
		t.Fatal("off must not log the node out")
	}
	g.Close()
}

func TestAppsPublicOwnTailscaleNodeNeedsLogin(t *testing.T) {
	dir := t.TempDir()
	if err := tsnode.SaveChoice(dir, tsnode.Choice{Name: "agent-net"}); err != nil {
		t.Fatal(err)
	}
	node := &fakeTSNode{needsLogin: true}
	g := nodeGateway(t, dir, node, &publicRunner{})
	defer g.Close()
	v, err := g.handle(context.Background(), &nodeConn{}, "apps.public.on", nil)
	if err == nil || !strings.Contains(err.Error(), "https://login.tailscale.com/a/test123") || !strings.Contains(err.Error(), "press p again") {
		t.Fatalf("on: %v %v", v, err)
	}
	if g.public.On || g.tsNode == nil {
		t.Fatalf("on=%v, node kept=%v", g.public.On, g.tsNode != nil)
	}
	// After the owner logs in, p again uses the same node.
	node.needsLogin = false
	if on := publicCall(t, g, "apps.public.on", nil); on["on"] != true || on["error"] != "" {
		t.Fatalf("after login: %v", on)
	}
	if node.ups != 2 {
		t.Fatalf("ups %d", node.ups)
	}
}

func TestAppsPublicSwitchBackCleansOwnedNode(t *testing.T) {
	dir := t.TempDir()
	if err := tsnode.SaveChoice(dir, tsnode.Choice{Name: "agent-net"}); err != nil {
		t.Fatal(err)
	}
	node := &fakeTSNode{}
	runner := &publicRunner{}
	g := nodeGateway(t, dir, node, runner)
	if on := publicCall(t, g, "apps.public.on", nil); on["node"] != "agent-net" {
		t.Fatalf("on: %v", on)
	}
	g.Close()
	// The owner goes back to the machine's node with access still on.
	if err := tsnode.RemoveChoice(dir); err != nil {
		t.Fatal(err)
	}
	g = nodeGateway(t, dir, node, runner)
	defer g.Close()
	if !node.loggedOut {
		t.Fatal("owned node not logged out")
	}
	if _, err := os.Stat(tsnode.StateDir(dir)); !os.IsNotExist(err) {
		t.Fatalf("state folder kept: %v", err)
	}
	status := publicCall(t, g, "apps.public.status", nil)
	if status["on"] != true || status["url"] != "https://test.example.ts.net:8443" || status["node"] != nil {
		t.Fatalf("machine mode after restart: %v", status)
	}
}

func TestAppsPublicPastedURLWinsOverOwnNode(t *testing.T) {
	dir := t.TempDir()
	if err := tsnode.SaveChoice(dir, tsnode.Choice{Name: "agent-net"}); err != nil {
		t.Fatal(err)
	}
	node := &fakeTSNode{}
	g := nodeGateway(t, dir, node, &publicRunner{})
	publicCall(t, g, "apps.public.on", nil)
	pasted := publicCall(t, g, "apps.public.url", map[string]string{"url": "https://tunnel.example.test"})
	if pasted["url"] != "https://tunnel.example.test" || pasted["node"] != nil {
		t.Fatalf("pasted: %v", pasted)
	}
	g.Close()
	listens := len(node.listens)
	g = nodeGateway(t, dir, node, &publicRunner{})
	defer g.Close()
	if s := publicCall(t, g, "apps.public.status", nil); s["url"] != "https://tunnel.example.test" || len(node.listens) != listens {
		t.Fatalf("restore: %v", s)
	}
}
