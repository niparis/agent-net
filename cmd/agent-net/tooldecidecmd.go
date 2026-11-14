package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// toolDecideBudget is how long `agent-net tool-decide` may take in all. Claude
// Code waits for the hook before each tool call, so it gives up quickly and
// then prints nothing.
const toolDecideBudget = 400 * time.Millisecond

type toolDecideInput struct {
	SessionID string          `json:"session_id"`
	EventName string          `json:"hook_event_name"`
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	Cwd       string          `json:"cwd"`
}

type toolDecideOutput struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

// runToolDecide is `agent-net tool-decide`, a Claude Code PreToolUse hook for
// Bash and agent-net tools (decision 0165). It asks the daemon whether a scope
// the owner delegated allows the call. On allow it prints Claude Code's
// permissionDecision allow. On anything else it prints nothing, so Claude Code
// asks as it did before: the hook never blocks and never fails a call.
func runToolDecide(ctx context.Context, stdin io.Reader, stdout io.Writer,
	ask func(ctx context.Context, req protocol.ToolDecideRequest) (protocol.ToolDecideResult, error)) {
	raw, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
	if err != nil {
		return
	}
	var in toolDecideInput
	if json.Unmarshal(raw, &in) != nil || in.SessionID == "" || in.EventName != "PreToolUse" || in.ToolName == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, toolDecideBudget)
	defer cancel()
	res, err := ask(ctx, protocol.ToolDecideRequest{Harness: "claude", NativeID: in.SessionID, Tool: in.ToolName, Input: in.ToolInput, Folder: in.Cwd})
	if err != nil || !res.Allow {
		return
	}
	var out toolDecideOutput
	out.HookSpecificOutput.HookEventName = "PreToolUse"
	out.HookSpecificOutput.PermissionDecision = "allow"
	out.HookSpecificOutput.PermissionDecisionReason = "The owner delegated this to the session's role. " + strings.TrimSpace(res.Reason)
	b, err := json.Marshal(out)
	if err != nil {
		return
	}
	stdout.Write(append(b, '\n'))
}

func cmdToolDecide(ctx context.Context) error {
	runToolDecide(ctx, os.Stdin, os.Stdout, func(ctx context.Context, req protocol.ToolDecideRequest) (protocol.ToolDecideResult, error) {
		var res protocol.ToolDecideResult
		cl, err := dialCLI(ctx)
		if err != nil {
			return res, err
		}
		defer cl.Close()
		raw, err := cl.Call(ctx, protocol.MethodToolDecide, req)
		if err != nil {
			return res, err
		}
		err = json.Unmarshal(raw, &res)
		return res, err
	})
	return nil
}
