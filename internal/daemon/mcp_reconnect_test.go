package daemon

import (
	"bufio"
	"context"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/a2a-ocs/internal/mcpserver"
)

func piHello() map[string]any {
	return map[string]any{"client": "pi", "harness": "pi", "native_id": "nb", "cwd": "/work/bob", "status": "idle"}
}

func TestMCPClientReconnectsAfterDaemonRestart(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	b := startNode(t, g, nodeOpts{cmdline: "pi"})
	md := mcpserver.NewDaemon(b.sock, piHello())
	t.Cleanup(md.Close)
	ctx := context.Background()

	if _, err := md.Call(ctx, "list", map[string]any{"online_only": true}); err != nil {
		t.Fatal(err)
	}
	b.restart()
	if _, err := md.Call(ctx, "list", map[string]any{"online_only": true}); err != nil {
		t.Fatalf("call after daemon restart: %v", err)
	}

	// With the daemon down the error is clear, and the next call recovers.
	b.d.Stop()
	_, err := md.Call(ctx, "list", map[string]any{})
	if err != mcpserver.ErrUnreachable {
		t.Fatalf("want ErrUnreachable, got %v", err)
	}
	b.restart()
	if _, err := md.Call(ctx, "list", map[string]any{}); err != nil {
		t.Fatalf("call after daemon came back: %v", err)
	}
}

// A send whose connection drops after the daemon got it is retried with the
// same idempotency key and is stored once.
func TestMCPSendRetriedAfterDropStoredOnce(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	startNode(t, g, nodeOpts{adapter: fa})
	b := startNode(t, g, nodeOpts{cmdline: "pi"})
	fa.setSessions(fakeSession("na", "alice"))
	eventually(t, "alice id", func() bool { return fa.host.SessionID("fake", "na") != "" })

	// A proxy that forwards the first send to the daemon, then cuts the client off.
	proxy := filepath.Join(shortDir(t), "p.sock")
	ln, err := net.Listen("unix", proxy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var dropped atomic.Bool
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			up, err := net.Dial("unix", b.sock)
			if err != nil {
				c.Close()
				continue
			}
			go func() {
				sc := bufio.NewScanner(c)
				sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
				for sc.Scan() {
					up.Write(append(sc.Bytes(), '\n'))
					if strings.Contains(sc.Text(), `"method":"send"`) && dropped.CompareAndSwap(false, true) {
						c.Close() // the response is lost
						time.Sleep(200 * time.Millisecond)
						up.Close()
						return
					}
				}
				up.Close()
			}()
			go func() {
				bufio.NewReader(up).WriteTo(c)
				c.Close()
			}()
		}
	}()

	md := mcpserver.NewDaemon(proxy, piHello())
	t.Cleanup(md.Close)
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := mcpserver.New(md.Call).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "agent_send", Arguments: map[string]any{"to": "alice", "body": "retry-me"}})
	if err != nil || res.IsError {
		t.Fatalf("agent_send: %v %+v", err, res)
	}
	if !dropped.Load() {
		t.Fatal("the connection was never dropped")
	}
	eventually(t, "alice got it", func() bool { return len(fa.delivered()) >= 1 })
	time.Sleep(300 * time.Millisecond)
	if n := len(fa.delivered()); n != 1 {
		t.Fatalf("message delivered %d times", n)
	}
	direct := dialLocal(t, b.sock)
	direct.mustCall("hello", piHello())
	raw := direct.mustCall("messages", map[string]any{})
	if n := strings.Count(string(raw), `"body":"retry-me"`); n != 1 {
		t.Fatalf("message stored %d times: %s", n, raw)
	}
}
