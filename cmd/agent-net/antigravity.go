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

func runAntigravityHook(args []string, in io.Reader, out io.Writer, socket string) {
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
