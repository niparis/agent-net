package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/hint"
	"github.com/niparis/agent-net/internal/protocol"
)

// hintBudget is how long `agent-net hint` may take in all. The Claude Code hook
// runs after a tool call, so it must be quick; it gives up silently.
const hintBudget = 280 * time.Millisecond

// hookInput is the part of the Claude Code hook input that the hint needs.
// PostToolUseFailure has "error" and no "tool_response"; PostToolUse has
// "tool_response" (https://code.claude.com/docs/en/hooks).
type hookInput struct {
	SessionID    string          `json:"session_id"`
	EventName    string          `json:"hook_event_name"`
	ToolName     string          `json:"tool_name"`
	Error        string          `json:"error"`
	IsInterrupt  bool            `json:"is_interrupt"`
	ToolResponse json.RawMessage `json:"tool_response"`
}

// hookOutput is what the hook prints when there is a suggestion.
type hookOutput struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// flattenStrings joins every string in a JSON value (the tool_response of a Bash
// call is an object with stdout and stderr), keys in sorted order.
func flattenStrings(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	var parts []string
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case string:
			parts = append(parts, t)
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(t[k])
			}
		}
	}
	walk(v)
	return strings.Join(parts, "\n")
}

// runHint is `agent-net hint`: a Claude Code hook for PostToolUseFailure and
// PostToolUse. It reads the hook input on stdin, asks the daemon for a hint and
// prints the hook output only when there is one. It never fails: on any
// problem it prints nothing and exits 0, so it can not disturb the session.
func runHint(ctx context.Context, stdin io.Reader, stdout io.Writer,
	ask func(ctx context.Context, req protocol.HintRequest) (protocol.HintResult, error)) {
	raw, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
	if err != nil {
		return
	}
	var in hookInput
	if json.Unmarshal(raw, &in) != nil || in.SessionID == "" || in.IsInterrupt {
		return
	}
	var text string
	var failed bool
	switch in.EventName {
	case "PostToolUseFailure":
		text, failed = in.Error, true
	case "PostToolUse":
		// A command can fail without a non-zero exit (curl without --fail).
		// Only a shell command's output is looked at; a file read is not.
		if in.ToolName != "Bash" {
			return
		}
		text = flattenStrings(in.ToolResponse)
		if !hint.LooksLikeFailure(text) {
			return
		}
	default:
		return
	}
	if strings.TrimSpace(text) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, hintBudget)
	defer cancel()
	res, err := ask(ctx, protocol.HintRequest{Text: text, Failed: failed, Harness: "claude", NativeID: in.SessionID})
	if err != nil || res.Text == "" {
		return
	}
	var out hookOutput
	out.HookSpecificOutput.HookEventName = in.EventName
	out.HookSpecificOutput.AdditionalContext = res.Text
	b, err := json.Marshal(out)
	if err != nil {
		return
	}
	stdout.Write(append(b, '\n'))
}

func cmdHint(ctx context.Context, _ []string) error {
	runHint(ctx, os.Stdin, os.Stdout, func(ctx context.Context, req protocol.HintRequest) (protocol.HintResult, error) {
		var res protocol.HintResult
		cl, err := dialCLI(ctx)
		if err != nil {
			return res, err
		}
		defer cl.Close()
		raw, err := cl.Call(ctx, protocol.MethodHint, req)
		if err != nil {
			return res, err
		}
		err = json.Unmarshal(raw, &res)
		return res, err
	})
	return nil
}
