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

// needsBudget is how long `agent-net needs-hook` may take in all. It runs
// inside a Claude Code turn, on every notification and prompt, so it gives up
// quickly and silently.
const needsBudget = 300 * time.Millisecond

// needsHookInput is the part of the Claude Code hook input that tells whether
// a session waits for its user (https://code.claude.com/docs/en/hooks).
// PermissionRequest carries the tool and its input; Notification carries
// notification_type, message and title; UserPromptSubmit carries the prompt.
type needsHookInput struct {
	SessionID        string         `json:"session_id"`
	EventName        string         `json:"hook_event_name"`
	NotificationType string         `json:"notification_type"`
	Message          string         `json:"message"`
	Title            string         `json:"title"`
	ToolName         string         `json:"tool_name"`
	ToolInput        map[string]any `json:"tool_input"`
}

// toolSummary is the short form of a tool call for the "Needs you" line: the
// command of a shell call, the path of a file call, or the first text field.
func toolSummary(in map[string]any) string {
	for _, key := range []string{"command", "file_path", "path", "url", "pattern", "query", "description", "prompt"} {
		if s, ok := in[key].(string); ok && strings.TrimSpace(s) != "" {
			return strings.Join(strings.Fields(s), " ")
		}
	}
	return ""
}

// needsText cleans the generic wording around a notification so the TUI line
// says what is needed, for example "permission: Bash" instead of "permission:
// Claude needs your permission to use Bash".
func needsText(in needsHookInput) (kind, text string) {
	text = strings.TrimSpace(in.Message)
	switch {
	case in.EventName == "PermissionRequest" || in.NotificationType == "permission_prompt":
		// PermissionRequest fires at once and names the tool; a permission
		// notification arrives seconds later and is often vaguer.
		kind = protocol.WaitingPermission
		if in.EventName == "PermissionRequest" && strings.TrimSpace(in.ToolName) != "" {
			text = strings.TrimSpace(in.ToolName)
			if summary := toolSummary(in.ToolInput); summary != "" {
				text += "(" + summary + ")"
			}
			return kind, text
		}
		for _, prefix := range []string{"Claude needs your permission to use ", "Claude needs your permission"} {
			if rest, ok := strings.CutPrefix(text, prefix); ok {
				text = strings.TrimSpace(rest)
				break
			}
		}
		if text == "" {
			text = strings.TrimSpace(in.Title)
		}
	case in.NotificationType == "idle_prompt", in.NotificationType == "agent_needs_input", in.NotificationType == "elicitation_url_dialog":
		kind = protocol.WaitingInput
	case in.NotificationType == "elicitation_dialog":
		kind = protocol.WaitingQuestion
	}
	return kind, text
}

// runNeedsHook is `agent-net needs-hook`: a Claude Code Notification hook that
// reports a waiting session, and a UserPromptSubmit hook that clears it. It
// reads the hook input on stdin, talks to the local daemon and prints nothing.
// On any problem it exits 0 without a word, so it never disturbs the session.
func runNeedsHook(ctx context.Context, stdin io.Reader, stdout io.Writer,
	notify func(ctx context.Context, req protocol.WaitingRequest) error) {
	raw, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
	if err != nil {
		return
	}
	var in needsHookInput
	if json.Unmarshal(raw, &in) != nil || in.SessionID == "" {
		return
	}
	req := protocol.WaitingRequest{Harness: "claude", NativeID: in.SessionID}
	switch {
	case in.EventName == "UserPromptSubmit", in.EventName == "PreToolUse":
		// A prompt, or a tool that ran after an approval, means the session
		// moved on. A denied permission sends the model on, and its next call
		// clears too.
		req.Clear = true
	case in.EventName == "PermissionRequest" || in.EventName == "Notification" || in.NotificationType != "":
		req.Kind, req.Text = needsText(in)
		if req.Kind == "" || req.Text == "" {
			return
		}
	default:
		return
	}
	ctx, cancel := context.WithTimeout(ctx, needsBudget)
	defer cancel()
	notify(ctx, req)
}

func cmdNeedsHook(ctx context.Context, _ []string) error {
	runNeedsHook(ctx, os.Stdin, os.Stdout, func(ctx context.Context, req protocol.WaitingRequest) error {
		cl, err := dialCLI(ctx)
		if err != nil {
			return err
		}
		defer cl.Close()
		_, err = cl.Call(ctx, protocol.MethodWaiting, req)
		return err
	})
	return nil
}
