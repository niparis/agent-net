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
	// Stop: the turn's final text, and whether a Stop hook already made the
	// session go on once in this turn.
	LastMessage    string `json:"last_assistant_message"`
	StopHookActive bool   `json:"stop_hook_active"`
	TranscriptPath string `json:"transcript_path"`
}

// stopOutput is the Stop hook answer that makes the session go on with the
// reason as its next instruction.
type stopOutput struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// runPolicyHook is `agent-net policy-hook`: a Claude Code hook on
// UserPromptSubmit, PostCompact, SessionStart (matcher compact) and Stop
// (runStopHook). It reads
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
	case "Stop":
		runStopHook(ctx, stdout, in, ask)
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

// runStopHook catches a turn that ended on a question to the owner (decision
// 0150): when the daemon says the question went to the chief, the session is
// told once to carry on. A turn that a Stop hook already continued always
// ends, so the hook can never loop.
func runStopHook(ctx context.Context, stdout io.Writer, in policyHookInput,
	ask func(ctx context.Context, req protocol.PolicyRequest) (protocol.PolicyResult, error)) {
	if in.StopHookActive {
		return
	}
	last := in.LastMessage
	if last == "" && in.TranscriptPath != "" {
		last = lastAssistantText(in.TranscriptPath)
	}
	res, err := ask(ctx, protocol.PolicyRequest{Harness: "claude", NativeID: in.SessionID, Event: "Stop", LastMessage: last})
	if err != nil || res.Continue == "" {
		return
	}
	b, err := json.Marshal(stopOutput{Decision: "block", Reason: res.Continue})
	if err != nil {
		return
	}
	stdout.Write(append(b, '\n'))
}

// lastAssistantText is the text of the newest main-conversation assistant
// entry in the end of a Claude Code transcript. Claude Code versions that do
// not give last_assistant_message to the Stop hook need it.
func lastAssistantText(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	const tail = 256 << 10
	if fi, err := f.Stat(); err == nil && fi.Size() > tail {
		if _, err := f.Seek(fi.Size()-tail, io.SeekStart); err != nil {
			return ""
		}
	}
	raw, err := io.ReadAll(io.LimitReader(f, tail))
	if err != nil {
		return ""
	}
	lines := strings.Split(string(raw), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var e struct {
			Type        string `json:"type"`
			IsSidechain bool   `json:"isSidechain"`
			Message     struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(lines[i]), &e) != nil || e.Type != "assistant" || e.IsSidechain {
			continue
		}
		var text []string
		for _, c := range e.Message.Content {
			if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
				text = append(text, c.Text)
			}
		}
		if len(text) > 0 {
			return strings.Join(text, "\n")
		}
	}
	return ""
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
