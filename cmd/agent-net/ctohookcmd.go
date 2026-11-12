package main

import (
	"encoding/json"
	"io"
	"os"
)

// ctoOwnerKeptOps are the cto_op methods the owner did not hand to the CTO:
// each one cannot be undone or removes a machine, an app or an identity
// (decision 0157). The hook leaves them to Claude Code's normal check.
var ctoOwnerKeptOps = map[string]bool{
	"app_revoke": true, "process_stop": true, "machine_remove": true, "machine_rotate": true,
	"agent_tombstone": true, "node_remove": true, "repo_archive": true,
}

// ctoOpToolName is the CTO's cto_op tool as Claude Code names it:
// mcp__<server>__<tool>.
const ctoOpToolName = "mcp__agent-net-cto__cto_op"

type ctoHookInput struct {
	EventName string `json:"hook_event_name"`
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Method string `json:"method"`
	} `json:"tool_input"`
}

type ctoHookOutput struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

// runCTOHook is `agent-net cto-hook`, the CTO's PreToolUse hook on cto_op.
// The owner's rule: "anything that can easily be reverted, the CTO may do".
// It allows every method except ctoOwnerKeptOps. For those, and for any input
// it does not understand, it prints nothing, so Claude Code asks as usual.
func runCTOHook(stdin io.Reader, stdout io.Writer) {
	raw, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
	if err != nil {
		return
	}
	var in ctoHookInput
	if json.Unmarshal(raw, &in) != nil || in.EventName != "PreToolUse" || in.ToolName != ctoOpToolName {
		return
	}
	if in.ToolInput.Method == "" || ctoOwnerKeptOps[in.ToolInput.Method] {
		return
	}
	var out ctoHookOutput
	out.HookSpecificOutput.HookEventName = "PreToolUse"
	out.HookSpecificOutput.PermissionDecision = "allow"
	out.HookSpecificOutput.PermissionDecisionReason = "The owner lets the CTO run operations that are easy to revert."
	b, err := json.Marshal(out)
	if err != nil {
		return
	}
	stdout.Write(append(b, '\n'))
}

func cmdCTOHook() error {
	runCTOHook(os.Stdin, os.Stdout)
	return nil
}
