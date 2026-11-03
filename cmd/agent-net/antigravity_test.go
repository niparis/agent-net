package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAntigravityHook(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "socket")
	var out bytes.Buffer
	start := time.Now()
	runAntigravityHook([]string{"antigravity", "Stop"}, strings.NewReader(`{"conversationId":"one"}`), &out, socket)
	if out.String() != "{}\n" || time.Since(start) > time.Second {
		t.Fatalf("no daemon: %q", out.String())
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	received := make(chan map[string]any, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		sc := bufio.NewScanner(c)
		for sc.Scan() {
			var req struct {
				ID     string         `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			_ = json.Unmarshal(sc.Bytes(), &req)
			if req.Method == "agy_hook" {
				received <- req.Params
			}
			fmt.Fprintf(c, `{"id":%q,"result":{}}`+"\n", req.ID)
		}
	}()
	out.Reset()
	runAntigravityHook([]string{"antigravity", "Stop"}, strings.NewReader(`{"conversationId":"one","fullyIdle":true}`), &out, socket)
	if out.String() != "{}\n" {
		t.Fatal(out.String())
	}
	select {
	case p := <-received:
		if p["event"] != "Stop" || p["conversationId"] != "one" || p["fullyIdle"] != true {
			t.Fatal(p)
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
}

func TestAntigravityHookApprovesOnlyAgentNetTools(t *testing.T) {
	cases := map[string]string{
		`{"toolCall":{"name":"call_mcp_tool","args":{"ServerName":"agent-net_agent-net","ToolName":"agent_announce"}}}`: `{"decision":"auto_approve"}`,
		`{"toolCall":{"name":"call_mcp_tool","args":{"ServerName":"other_server","ToolName":"x"}}}`:                     `{}`,
		`{"toolCall":{"name":"run_command","args":{"ServerName":"agent-net_agent-net"}}}`:                               `{}`,
		`not json`: `{}`,
	}
	for in, want := range cases {
		var out strings.Builder
		runAntigravityHook([]string{"antigravity", "PreToolUse"}, strings.NewReader(in), &out, "/nonexistent")
		if strings.TrimSpace(out.String()) != want {
			t.Errorf("%s: got %q, want %q", in, out.String(), want)
		}
	}
}
