package main

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// notification runs `agent-net needs-hook` with one hook input and returns
// what it asked the daemon, if anything.
func notification(t *testing.T, input string) (protocol.WaitingRequest, bool) {
	t.Helper()
	var got protocol.WaitingRequest
	called := false
	runNeedsHook(context.Background(), strings.NewReader(input), io.Discard,
		func(_ context.Context, req protocol.WaitingRequest) error {
			called, got = true, req
			return nil
		})
	return got, called
}

// PermissionRequest fires as soon as a tool needs approval and names the
// tool and its input, which is what the TUI line should say.
func TestNeedsHookReportsAPermissionRequest(t *testing.T) {
	got, called := notification(t, `{"session_id":"abc","hook_event_name":"PermissionRequest",
		"tool_name":"Bash","tool_input":{"command":"git push origin main","description":"push"}}`)
	if !called {
		t.Fatal("the permission request was not reported")
	}
	if got.Harness != "claude" || got.NativeID != "abc" || got.Kind != protocol.WaitingPermission {
		t.Fatalf("request = %+v", got)
	}
	if got.Text != "Bash(git push origin main)" {
		t.Fatalf("text = %q", got.Text)
	}
	// A tool with no useful text still names itself.
	got, called = notification(t, `{"session_id":"abc","hook_event_name":"PermissionRequest","tool_name":"Edit","tool_input":{}}`)
	if !called || got.Text != "Edit" {
		t.Fatalf("request = %+v", got)
	}
}

// An owner may have added a Notification permission hook by hand; it still
// means a wait, even without the tool.
func TestNeedsHookReportsAPermissionNotification(t *testing.T) {
	got, called := notification(t, `{"session_id":"abc","hook_event_name":"Notification",
		"notification_type":"permission_prompt","message":"Claude needs your permission to use Bash","title":"Permission needed"}`)
	if !called {
		t.Fatal("the permission prompt was not reported")
	}
	if got.Harness != "claude" || got.NativeID != "abc" || got.Kind != protocol.WaitingPermission {
		t.Fatalf("request = %+v", got)
	}
	if got.Text != "Bash" {
		t.Fatalf("text = %q, want the tool name without the generic wording", got.Text)
	}
}

func TestNeedsHookReportsInputAndQuestion(t *testing.T) {
	for _, c := range []struct {
		typ, msg, kind string
	}{
		{"agent_needs_input", "A background session needs input", protocol.WaitingInput},
		{"elicitation_dialog", "An MCP server asks a question", protocol.WaitingQuestion},
	} {
		got, called := notification(t, `{"session_id":"abc","hook_event_name":"Notification","notification_type":"`+c.typ+`","message":"`+c.msg+`"}`)
		if !called || got.Kind != c.kind || got.Text != c.msg {
			t.Errorf("%s: called=%v request=%+v", c.typ, called, got)
		}
	}
}

// Claude Code sends idle_prompt about 60 seconds after any turn ends. It means
// the session finished, not that the owner is needed, so the hook reports
// nothing and the session stays out of "Needs you".
func TestNeedsHookIgnoresIdlePrompt(t *testing.T) {
	_, called := notification(t, `{"session_id":"abc","hook_event_name":"Notification",
		"notification_type":"idle_prompt","message":"Claude is waiting for your input"}`)
	if called {
		t.Fatal("idle_prompt was reported as a wait")
	}
}

// A prompt from the user is the answer: the wait is over, even when the
// policy hook is not installed. A tool that runs also means the session moved
// on, which is how an approved permission clears while the turn stays busy.
func TestNeedsHookClearsOnUserPromptAndToolUse(t *testing.T) {
	for _, input := range []string{
		`{"session_id":"abc","hook_event_name":"UserPromptSubmit","prompt":"yes, push it"}`,
		`{"session_id":"abc","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git push"}}`,
	} {
		got, called := notification(t, input)
		if !called || !got.Clear || got.NativeID != "abc" {
			t.Fatalf("called=%v request=%+v", called, got)
		}
	}
}

func TestNeedsHookIgnoresOtherNotifications(t *testing.T) {
	for _, input := range []string{
		`{"session_id":"abc","hook_event_name":"Notification","notification_type":"auth_success","message":"logged in"}`,
		`{"session_id":"","hook_event_name":"Notification","notification_type":"permission_prompt","message":"x"}`,
		`not json`,
		`{"session_id":"abc","hook_event_name":"Stop"}`,
	} {
		if _, called := notification(t, input); called {
			t.Errorf("reported a wait for %q", input)
		}
	}
}

// AskUserQuestion and ExitPlanMode always stop for the user. PreToolUse fires
// just before the dialog with the full input, so it starts the wait with the
// question itself (decision 0057). A PermissionRequest for the same call
// gives the same words, so the two hooks describe one wait.
func TestNeedsHookReportsAskUserQuestion(t *testing.T) {
	ask := `"tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Merge branch w_1 into main?","header":"Merge",
		"options":[{"label":"Merge now","description":"fast-forward"},{"label":"Wait"}],"multiSelect":false}]}`
	for _, event := range []string{"PreToolUse", "PermissionRequest"} {
		got, called := notification(t, `{"session_id":"abc","hook_event_name":"`+event+`",`+ask+`}`)
		if !called || got.Clear || got.Kind != protocol.WaitingQuestion || got.Text != "Merge branch w_1 into main?" ||
			strings.Join(got.Options, "|") != "Merge now|Wait" || got.NativeID != "abc" || got.Harness != "claude" {
			t.Fatalf("%s: called=%v request=%+v", event, called, got)
		}
	}
	got, called := notification(t, `{"session_id":"abc","hook_event_name":"PreToolUse","tool_name":"ExitPlanMode","tool_input":{"plan":"1. do it"}}`)
	if !called || got.Clear || got.Kind != protocol.WaitingPermission || got.Text != "approve the plan" {
		t.Fatalf("ExitPlanMode: called=%v request=%+v", called, got)
	}
}

// The answer to the dialog is the tool's result: PostToolUse (or
// PostToolUseFailure for a refused call) ends the wait at once, even when the
// model then ends its turn without another tool.
func TestNeedsHookClearsWhenTheDialogIsAnswered(t *testing.T) {
	for _, event := range []string{"PostToolUse", "PostToolUseFailure"} {
		got, called := notification(t, `{"session_id":"abc","hook_event_name":"`+event+`","tool_name":"AskUserQuestion","tool_input":{}}`)
		if !called || !got.Clear || got.NativeID != "abc" {
			t.Fatalf("%s: called=%v request=%+v", event, called, got)
		}
	}
}
