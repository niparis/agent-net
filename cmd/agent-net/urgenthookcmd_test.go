package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/daemon"
	"github.com/niparis/agent-net/internal/protocol"
)

// runUrgent runs the hook for session "abc" with its marker in place, as the
// daemon leaves it when an urgent message is held.
func runUrgent(t *testing.T, input string, answer protocol.UrgentTakeResult, err error) (string, *protocol.UrgentTakeRequest) {
	t.Helper()
	state := t.TempDir()
	setUrgentMarker(t, state, "abc")
	return runUrgentIn(t, state, input, answer, err)
}

func setUrgentMarker(t *testing.T, state, native string) {
	t.Helper()
	p := daemon.UrgentMarkerPath(state, "claude", native)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func runUrgentIn(t *testing.T, state, input string, answer protocol.UrgentTakeResult, err error) (string, *protocol.UrgentTakeRequest) {
	t.Helper()
	var asked *protocol.UrgentTakeRequest
	var out bytes.Buffer
	runUrgentHook(context.Background(), strings.NewReader(input), &out, state,
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

// Almost every tool call has nothing urgent waiting. Without a marker the hook
// must not call the daemon at all; with one it takes, and the take carries a
// deadline shorter than the hook's own budget (decision 0180).
func TestUrgentHookCallsTheDaemonOnlyWhenAMarkerExists(t *testing.T) {
	state := t.TempDir()
	input := `{"session_id":"abc","hook_event_name":"PostToolUse"}`
	out, asked := runUrgentIn(t, state, input, protocol.UrgentTakeResult{Text: "STOP"}, nil)
	if out != "" || asked != nil {
		t.Fatalf("no marker: printed %q and asked %+v, want neither", out, asked)
	}
	// A marker of another session does not count.
	setUrgentMarker(t, state, "other")
	if _, asked = runUrgentIn(t, state, input, protocol.UrgentTakeResult{}, nil); asked != nil {
		t.Fatalf("another session's marker made the hook call the daemon: %+v", asked)
	}
	setUrgentMarker(t, state, "abc")
	out, asked = runUrgentIn(t, state, input, protocol.UrgentTakeResult{Text: "STOP"}, nil)
	if asked == nil || !strings.Contains(out, "STOP") {
		t.Fatalf("marker: printed %q and asked %+v, want a take that printed the message", out, asked)
	}
	if want := int(urgentTakeDeadline / time.Millisecond); asked.DeadlineMS != want || urgentTakeDeadline >= urgentHookBudget {
		t.Fatalf("take deadline = %dms with a hook budget of %v, want %dms and shorter than the budget", asked.DeadlineMS, urgentHookBudget, want)
	}
}

// A session ID from the hook input must not point outside the marker folder.
func TestUrgentHookIgnoresAnUnsafeSessionID(t *testing.T) {
	state := t.TempDir()
	setUrgentMarker(t, state, "abc")
	_, asked := runUrgentIn(t, state, `{"session_id":"../urgent/claude-abc","hook_event_name":"PostToolUse"}`, protocol.UrgentTakeResult{}, nil)
	if asked != nil {
		t.Fatalf("a path as session ID reached the daemon: %+v", asked)
	}
}
