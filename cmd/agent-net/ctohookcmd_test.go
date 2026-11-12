package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// The CTO's PreToolUse hook allows every cto_op method at once, except the
// ones the owner keeps (decision 0157). Those get no decision, so Claude
// Code's normal permission check asks the owner.
func TestCTOHookAllowsRevertibleOps(t *testing.T) {
	run := func(input string) string {
		var out bytes.Buffer
		runCTOHook(strings.NewReader(input), &out)
		return out.String()
	}
	call := func(method string) string {
		raw, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "mcp__agent-net-cto__cto_op", "tool_input": map[string]any{"method": method, "args": map[string]any{}}})
		return string(raw)
	}
	for _, method := range []string{"session_replace", "merger_start", "settings_values_write", "mcp_import"} {
		var got struct {
			Out struct {
				Event    string `json:"hookEventName"`
				Decision string `json:"permissionDecision"`
				Reason   string `json:"permissionDecisionReason"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(run(call(method))), &got); err != nil || got.Out.Event != "PreToolUse" || got.Out.Decision != "allow" || got.Out.Reason == "" {
			t.Errorf("%s: %+v err=%v", method, got, err)
		}
	}
	for _, method := range []string{"app_revoke", "process_stop", "machine_remove", "machine_rotate", "agent_tombstone", "node_remove", "repo_archive"} {
		if out := run(call(method)); out != "" {
			t.Errorf("%s must be left to the owner, got %q", method, out)
		}
	}
	for _, input := range []string{
		"",
		"not json",
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"method":"session_replace"}}`,
		`{"hook_event_name":"PostToolUse","tool_name":"mcp__agent-net-cto__cto_op","tool_input":{"method":"session_replace"}}`,
		`{"hook_event_name":"PreToolUse","tool_name":"mcp__agent-net-cto__cto_op","tool_input":{}}`,
	} {
		if out := run(input); out != "" {
			t.Errorf("input %q: want no decision, got %q", input, out)
		}
	}
}
