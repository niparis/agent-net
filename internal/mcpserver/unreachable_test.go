package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// logLines collects what a Daemon logs.
type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) logf(format string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, a...))
}

func (l *logLines) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

// A Codex MCP child runs with a few environment variables only. Without the
// socket path it dials the fallback path, which has no daemon. The error must
// name that path and the reason, not only "not reachable".
func TestDaemonCallNamesTheSocketAndTheReason(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "missing", "daemon.sock")
	d := NewDaemon(socket, map[string]any{"client": "mcp"})
	t.Cleanup(d.Close)
	_, err := d.Call(context.Background(), "list", nil)
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("want ErrUnreachable, got %v", err)
	}
	if !strings.Contains(err.Error(), socket) || !strings.Contains(err.Error(), "no such file or directory") {
		t.Fatalf("the error lacks the socket and the reason: %v", err)
	}
}

// One line per outage on the log, not one per call or per reconnect.
func TestDaemonLogsOneLineWhenItCannotConnect(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "missing", "daemon.sock")
	var logs logLines
	d := NewDaemon(socket, map[string]any{"client": "mcp"})
	d.Logf = logs.logf
	t.Cleanup(d.Close)
	for i := 0; i < 2; i++ {
		if _, err := d.Call(context.Background(), "list", nil); !errors.Is(err, ErrUnreachable) {
			t.Fatalf("call %d: want ErrUnreachable, got %v", i, err)
		}
	}
	got := logs.all()
	if len(got) != 1 {
		t.Fatalf("want one log line, got %d: %q", len(got), got)
	}
	if !strings.Contains(got[0], socket) || !strings.Contains(got[0], "no such file or directory") {
		t.Fatalf("the log line lacks the socket and the reason: %q", got[0])
	}
}

// A refused hello is not "not reachable": the refusal reaches the caller, and
// the log says why the session could not start.
func TestDaemonLogsARefusedHelloAndReturnsTheRefusal(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "d.sock")
	refuseHellos(t, socket, "peer check failed: test refusal")
	var logs logLines
	d := NewDaemon(socket, map[string]any{"client": "mcp", "harness": "codex"})
	d.Logf = logs.logf
	t.Cleanup(d.Close)
	_, err := d.Call(context.Background(), "list", nil)
	if err == nil || errors.Is(err, ErrUnreachable) || !strings.Contains(err.Error(), "peer check failed: test refusal") {
		t.Fatalf("want the refusal, got %v", err)
	}
	got := logs.all()
	if len(got) != 1 || !strings.Contains(got[0], "hello refused") || !strings.Contains(got[0], "peer check failed: test refusal") {
		t.Fatalf("log lines: %q", got)
	}
}

// refuseHellos answers every hello with an error, the way the daemon does when
// it refuses a peer, and then closes the connection.
func refuseHellos(t *testing.T, socket, reason string) {
	t.Helper()
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				line, err := bufio.NewReader(c).ReadBytes('\n')
				if err != nil {
					return
				}
				var req struct {
					ID json.RawMessage `json:"id"`
				}
				json.Unmarshal(line, &req)
				out, _ := json.Marshal(map[string]any{"id": req.ID, "error": reason})
				c.Write(append(out, '\n'))
			}(c)
		}
	}()
}

// A Codex MCP process has one connection for the root and one per thread. An
// outage is logged once for the process, not once per connection.
func TestThreadConnectionsLogAnOutageOnce(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "missing", "daemon.sock")
	var logs logLines
	rt := &threadRouter{socket: socket, harness: "codex", logf: logs.logf, failures: &failureLog{}, perThread: map[string]*Daemon{}}
	rt.root = rt.newDaemon("")
	t.Cleanup(rt.Close)
	for _, thread := range []string{"", "t1", "t2"} {
		ctx := context.Background()
		if thread != "" {
			ctx = context.WithValue(ctx, threadKey{}, thread)
		}
		if _, err := rt.Call(ctx, "list", nil); !errors.Is(err, ErrUnreachable) {
			t.Fatalf("thread %q: want ErrUnreachable, got %v", thread, err)
		}
	}
	if got := logs.all(); len(got) != 1 {
		t.Fatalf("want one log line for the process, got %d: %q", len(got), got)
	}
}
