package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// policyBudget is how long `agent-net policy-hook` may take in all. It runs on
// every prompt of a Claude Code session, so it gives up quickly and silently.
const policyBudget = 700 * time.Millisecond

// policyHookInput is the part of the hook input that is needed
// (https://code.claude.com/docs/en/hooks).
type policyHookInput struct {
	SessionID string `json:"session_id"`
	EventName string `json:"hook_event_name"`
	Source    string `json:"source"` // SessionStart: startup, resume, clear, compact, fork
	Prompt    string `json:"prompt"`
}

// runPolicyHook is `agent-net policy-hook`: a Claude Code hook on
// UserPromptSubmit, PostCompact and SessionStart (matcher compact). It reads
// the hook input on stdin, asks the daemon for the delegation policy of the
// session and the charter of its node, and prints the hook output when there
// is one:
//
//	{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"..."}}
//
// A compaction only tells the daemon, so the next prompt gets the charter
// block again. It never fails: on any problem it prints nothing and exits 0,
// so it cannot disturb the session.
func runPolicyHook(ctx context.Context, stdin io.Reader, stdout io.Writer,
	ask func(ctx context.Context, req protocol.PolicyRequest) (protocol.PolicyResult, error),
	compacted func(ctx context.Context, req protocol.CompactionRequest) error) {
	raw, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
	if err != nil {
		return
	}
	var in policyHookInput
	if json.Unmarshal(raw, &in) != nil || in.SessionID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, policyBudget)
	defer cancel()
	switch in.EventName {
	case "PostCompact":
		signalCompaction(ctx, compacted, in.SessionID)
		return
	case "SessionStart":
		if in.Source == "compact" {
			signalCompaction(ctx, compacted, in.SessionID)
		}
		return
	case "", "UserPromptSubmit":
	default:
		return
	}
	if r := []rune(in.Prompt); len(r) > 300 {
		in.Prompt = string(r[:300])
	}
	res, err := ask(ctx, protocol.PolicyRequest{Harness: "claude", NativeID: in.SessionID, Prompt: in.Prompt})
	if err != nil || (res.Text == "" && res.Charter == "" && res.Notice == "") {
		return
	}
	context := res.Text
	for _, part := range []string{res.Charter, res.Notice} {
		if part == "" {
			continue
		}
		if context != "" {
			context += "\n\n"
		}
		context += part
	}
	var out hookOutput
	out.HookSpecificOutput.HookEventName = "UserPromptSubmit"
	out.HookSpecificOutput.AdditionalContext = context
	b, err := json.Marshal(out)
	if err != nil {
		return
	}
	stdout.Write(append(b, '\n'))
}

func signalCompaction(ctx context.Context, compacted func(context.Context, protocol.CompactionRequest) error, sid string) {
	if compacted == nil {
		return
	}
	_ = compacted(ctx, protocol.CompactionRequest{Harness: "claude", NativeID: sid})
}

func cmdPolicyHook(ctx context.Context, _ []string) error {
	runPolicyHook(ctx, os.Stdin, os.Stdout, func(ctx context.Context, req protocol.PolicyRequest) (protocol.PolicyResult, error) {
		var res protocol.PolicyResult
		cl, err := dialCLI(ctx)
		if err != nil {
			return res, err
		}
		defer cl.Close()
		raw, err := cl.Call(ctx, protocol.MethodPolicy, req)
		if err != nil {
			return res, err
		}
		err = json.Unmarshal(raw, &res)
		return res, err
	}, func(ctx context.Context, req protocol.CompactionRequest) error {
		cl, err := dialCLI(ctx)
		if err != nil {
			return err
		}
		defer cl.Close()
		_, err = cl.Call(ctx, protocol.MethodCompaction, req)
		return err
	})
	return nil
}
