package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// fakeCTODaemon answers hello and cto_tool on a unix socket, like the daemon.
func fakeCTODaemon(t *testing.T, socket string) net.Listener {
	t.Helper()
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewScanner(c)
				for r.Scan() {
					var req struct {
						ID     json.RawMessage `json:"id"`
						Method string          `json:"method"`
					}
					if json.Unmarshal(r.Bytes(), &req) != nil {
						return
					}
					out, _ := json.Marshal(map[string]any{"id": req.ID, "result": map[string]string{"method": req.Method}})
					if _, err := c.Write(append(out, '\n')); err != nil {
						return
					}
				}
			}(c)
		}
	}()
	return ln
}

// The CTO's Claude job outlives daemon restarts (every auto-upgrade), and so
// does its MCP child. The child must reach the new daemon instead of failing
// every tool call with "connection closed".
func TestCTOChannelReconnectsAfterADaemonRestart(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "d.sock")
	ln := fakeCTODaemon(t, socket)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := dialCTOChannel(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	defer ch.close()
	if _, err := ch.call(ctx, "graph", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}
	ln.Close()
	ch.dropForTest()
	ln = fakeCTODaemon(t, socket)
	defer ln.Close()
	if _, err := ch.call(ctx, "graph", nil); err != nil {
		t.Fatalf("call after a daemon restart: %v", err)
	}
}

// At start the daemon may be restarting; the channel waits for it briefly.
func TestCTOChannelWaitsForTheDaemonAtStart(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "d.sock")
	go func() {
		time.Sleep(300 * time.Millisecond)
		fakeCTODaemon(t, socket)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := dialCTOChannel(ctx, socket)
	if err != nil {
		t.Fatalf("dial while the daemon starts: %v", err)
	}
	ch.close()
}

func TestCTOChannelReportsDaemonVersion(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "d.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	// Like the daemon, the fake keeps the connection open after its reply. A
	// close right after the reply races the client's read and makes it dial
	// again, and nothing answers a second connection.
	conns := make(chan net.Conn, 1)
	t.Cleanup(func() {
		select {
		case c := <-conns:
			c.Close()
		default:
		}
	})
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		conns <- c
		r := bufio.NewScanner(c)
		if !r.Scan() {
			return
		}
		var req struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal(r.Bytes(), &req) != nil {
			return
		}
		out, _ := json.Marshal(map[string]any{"id": req.ID, "result": map[string]string{"version": "v9.8.7"}})
		_, _ = c.Write(append(out, '\n'))
	}()
	versions := make(chan string, 1)
	ctx, cancel := context.WithTimeout(context.Background(), waitMax)
	defer cancel()
	ch, err := dialCTOChannel(ctx, socket, func(v string) { versions <- v })
	if err != nil {
		t.Fatal(err)
	}
	defer ch.close()
	select {
	case got := <-versions:
		if got != "v9.8.7" {
			t.Fatalf("daemon version = %q", got)
		}
	case <-ctx.Done():
		t.Fatal("CTO channel did not report daemon version")
	}
}

// dropForTest closes the current connection, as a daemon restart does.
func (ch *ctoChannel) dropForTest() {
	ch.mu.Lock()
	c := ch.c
	ch.mu.Unlock()
	c.Close()
}
