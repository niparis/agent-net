package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// urgentHookBudget is how long `agent-net urgent-hook` may take in all. It
// runs after every tool call, so it gives up quickly and then prints nothing.
const urgentHookBudget = 1800 * time.Millisecond

// urgentContextHead is agent-net's own line before the message, so the model
// knows why it is reading it between two tool calls (decision 0180).
const urgentContextHead = "[agent-net] An urgent message arrived while you were working. Read it now, before your next step, and act on it. Do not wait for the end of your turn.\n\n"

type urgentHookInput struct {
	SessionID string `json:"session_id"`
	EventName string `json:"hook_event_name"`
}

type urgentHookOutput struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// runUrgentHook is `agent-net urgent-hook`, a Claude Code PostToolUse and
// PostToolUseFailure hook (decision 0180). It asks the daemon for the urgent
// messages held for this session and prints them as additionalContext, which
// the model reads before its next step. A message is shown once: the daemon
// reports it delivered when it hands the text over. On anything else it prints
// nothing and never fails a tool call.
func runUrgentHook(ctx context.Context, stdin io.Reader, stdout io.Writer,
	take func(ctx context.Context, req protocol.UrgentTakeRequest) (protocol.UrgentTakeResult, error)) {
	raw, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
	if err != nil {
		return
	}
	var in urgentHookInput
	if json.Unmarshal(raw, &in) != nil || in.SessionID == "" || (in.EventName != "PostToolUse" && in.EventName != "PostToolUseFailure") {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, urgentHookBudget)
	defer cancel()
	res, err := take(ctx, protocol.UrgentTakeRequest{Harness: "claude", NativeID: in.SessionID})
	if err != nil || res.Text == "" {
		return
	}
	var out urgentHookOutput
	out.HookSpecificOutput.HookEventName = in.EventName
	out.HookSpecificOutput.AdditionalContext = urgentContextHead + res.Text
	b, err := json.Marshal(out)
	if err != nil {
		return
	}
	stdout.Write(append(b, '\n'))
}

func cmdUrgentHook(ctx context.Context) error {
	runUrgentHook(ctx, os.Stdin, os.Stdout, func(ctx context.Context, req protocol.UrgentTakeRequest) (protocol.UrgentTakeResult, error) {
		var res protocol.UrgentTakeResult
		cl, err := dialCLI(ctx)
		if err != nil {
			return res, err
		}
		defer cl.Close()
		raw, err := cl.Call(ctx, protocol.MethodUrgentTake, req)
		if err != nil {
			return res, err
		}
		err = json.Unmarshal(raw, &res)
		return res, err
	})
	return nil
}
