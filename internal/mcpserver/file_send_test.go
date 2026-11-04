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
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAgentFileSendForwardsAndValidates(t *testing.T) {
	var calls []call
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		raw, _ := json.Marshal(params)
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		calls = append(calls, call{method, p})
		return json.RawMessage(`{"message_id":"m_1","files":[{"name":"a.txt","path":"inbox/s_b/m_1/a.txt"}]}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_file_send", Arguments: map[string]any{"to": "s_b", "paths": []string{"/tmp/a.txt"}, "note": "read"}})
	if err != nil || res.IsError {
		t.Fatalf("file send: %v %+v", err, res)
	}
	if len(calls) != 1 || calls[0].method != "file_send" || calls[0].params["to"] != "s_b" || calls[0].params["note"] != "read" {
		t.Fatalf("calls: %+v", calls)
	}
	validateAgainstCatalog(t, "agent_file_send", res.StructuredContent)
	bad, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_file_send", Arguments: map[string]any{"to": "s_b", "paths": []string{}, "note": "read"}})
	if err != nil || !bad.IsError || len(calls) != 1 {
		t.Fatalf("bad args: %v %+v calls %v", err, bad, calls)
	}
}

func TestAgentFileSendDaemonError(t *testing.T) {
	cs := connect(t, func(context.Context, string, any) (json.RawMessage, error) {
		return nil, errors.New("workers send files with agent_handback")
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_file_send", Arguments: map[string]any{"to": "s_b", "paths": []string{"/tmp/a"}, "note": "x"}})
	if err != nil || !res.IsError {
		t.Fatalf("daemon error: %v %+v", err, res)
	}
}

// A send may have reached the daemon before the socket breaks. Retrying it
// blindly would store another message and transfer a second set of files.
func TestAgentFileSendConnectionLossDoesNotRetry(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "daemon.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var count atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				scan := bufio.NewScanner(c)
				for scan.Scan() {
					var req struct {
						ID     string `json:"id"`
						Method string `json:"method"`
					}
					_ = json.Unmarshal(scan.Bytes(), &req)
					if req.Method == "hello" {
						fmt.Fprintf(c, "{\"id\":%q,\"result\":{}}\n", req.ID)
						continue
					}
					if req.Method == "file_send" {
						count.Add(1)
						return
					}
				}
			}()
		}
	}()
	d := NewDaemon(socket, map[string]any{"client": "mcp"})
	t.Cleanup(d.Close)
	_, err = d.Call(context.Background(), "file_send", map[string]any{"to": "s_b", "paths": []string{"/tmp/a"}, "note": "read"})
	if err == nil || !strings.Contains(err.Error(), "may have been sent") {
		t.Errorf("error = %v", err)
	}
	if got := count.Load(); got != 1 {
		t.Errorf("file sends = %d, want 1", got)
	}
	ln.Close()
	<-done
}

func TestAgentFileSendWorkerRefusalIsOneLine(t *testing.T) {
	t.Setenv("AGENT_NET_WORKER", "1")
	cs := connect(t, func(context.Context, string, any) (json.RawMessage, error) {
		return nil, errors.New("workers send files with agent_handback")
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_file_send", Arguments: map[string]any{"to": "s_b", "paths": []string{"/tmp/a"}, "note": "x"}})
	if err != nil || !res.IsError || len(res.Content) != 1 || resultText(t, res) != "workers send files with agent_handback" {
		t.Fatalf("worker refusal: %v %+v", err, res)
	}
}
