package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/daemon"
	"github.com/niparis/agent-net/internal/protocol"
)

// Hook failures are deliberately silent to avoid interrupting agy.
func cmdAntigravityHook(args []string) error {
	runAntigravityHook(args, os.Stdin, os.Stdout, daemon.SocketPath())
	return nil
}

// agentNetMCPServer is how agy names agent-net's MCP server: the plugin name,
// an underscore, then the server name.
const agentNetMCPServer = "agent-net_agent-net"

// antigravityToolApproval answers a PreToolUse hook: auto_approve for calls to
// agent-net's own MCP server, nothing for any other tool.
func antigravityToolApproval(in io.Reader) string {
	var p struct {
		ToolCall struct {
			Name string `json:"name"`
			Args struct {
				ServerName string `json:"ServerName"`
			} `json:"args"`
		} `json:"toolCall"`
	}
	b, err := io.ReadAll(io.LimitReader(in, 1<<20))
	if err != nil || json.Unmarshal(b, &p) != nil {
		return `{}`
	}
	if p.ToolCall.Name == "call_mcp_tool" && p.ToolCall.Args.ServerName == agentNetMCPServer {
		return `{"decision":"auto_approve"}`
	}
	return `{}`
}

func runAntigravityHook(args []string, in io.Reader, out io.Writer, socket string) {
	if len(args) == 2 && args[0] == "antigravity" && args[1] == "PreToolUse" {
		fmt.Fprintln(out, antigravityToolApproval(in))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan json.RawMessage, 1)
	go func() {
		result := json.RawMessage(`{}`)
		defer func() { done <- result }()
		if len(args) != 2 || args[0] != "antigravity" {
			return
		}
		switch args[1] {
		case "SessionStart", "PreInvocation", "Stop":
		default:
			return
		}
		b, err := io.ReadAll(io.LimitReader(in, 1<<20))
		if err != nil {
			return
		}
		var p map[string]any
		if json.Unmarshal(b, &p) != nil || p == nil {
			return
		}
		p["event"] = args[1]
		cl, err := client.Dial(socket)
		if err != nil {
			return
		}
		defer cl.Close()
		if _, err = cl.Call(ctx, "hello", map[string]any{"client": "cli", "protocol": protocol.LocalProtocol}); err != nil {
			return
		}
		raw, err := cl.Call(ctx, "agy_hook", p)
		if err == nil && json.Valid(raw) {
			result = raw
		}
	}()
	select {
	case b := <-done:
		fmt.Fprintln(out, string(b))
	case <-ctx.Done():
		fmt.Fprintln(out, `{}`)
	}
}
