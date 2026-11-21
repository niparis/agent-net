package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func runUrgent(t *testing.T, input string, answer protocol.UrgentTakeResult, err error) (string, *protocol.UrgentTakeRequest) {
	t.Helper()
	var asked *protocol.UrgentTakeRequest
	var out bytes.Buffer
	runUrgentHook(context.Background(), strings.NewReader(input), &out,
		func(_ context.Context, req protocol.UrgentTakeRequest) (protocol.UrgentTakeResult, error) {
			asked = &req
			return answer, err
		})
	return out.String(), asked
}

// After a tool call the hook shows the model what the daemon collected, as
// additionalContext of the same event, so the model reads it before its next
// step.
func TestUrgentHookShowsTheMessageAfterAToolCall(t *testing.T) {
	for _, event := range []string{"PostToolUse", "PostToolUseFailure"} {
		out, asked := runUrgent(t, `{"session_id":"abc","hook_event_name":"`+event+`","tool_name":"Bash"}`,
			protocol.UrgentTakeResult{Text: "STOP copying"}, nil)
		if asked == nil || asked.Harness != "claude" || asked.NativeID != "abc" {
			t.Fatalf("%s: asked %+v", event, asked)
		}
		var got struct {
			HookSpecificOutput struct {
				HookEventName     string `json:"hookEventName"`
				AdditionalContext string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%s: output %q: %v", event, out, err)
		}
		if got.HookSpecificOutput.HookEventName != event || !strings.Contains(got.HookSpecificOutput.AdditionalContext, "STOP copying") {
			t.Fatalf("%s: output = %q", event, out)
		}
		if !strings.Contains(got.HookSpecificOutput.AdditionalContext, "urgent") {
			t.Fatalf("%s: the model is not told the message is urgent: %q", event, out)
		}
	}
}

// Nothing waiting, a daemon error or a foreign event prints nothing, so the
// hook never disturbs a turn.
func TestUrgentHookIsSilentWhenThereIsNothingToShow(t *testing.T) {
	if out, _ := runUrgent(t, `{"session_id":"abc","hook_event_name":"PostToolUse"}`, protocol.UrgentTakeResult{}, nil); out != "" {
		t.Fatalf("printed %q with nothing waiting", out)
	}
	if out, _ := runUrgent(t, `{"session_id":"abc","hook_event_name":"PostToolUse"}`, protocol.UrgentTakeResult{Text: "x"}, errors.New("no daemon")); out != "" {
		t.Fatalf("printed %q after a daemon error", out)
	}
	if out, asked := runUrgent(t, `{"session_id":"abc","hook_event_name":"UserPromptSubmit"}`, protocol.UrgentTakeResult{Text: "x"}, nil); out != "" || asked != nil {
		t.Fatalf("a prompt event asked the daemon or printed: %q %+v", out, asked)
	}
}
