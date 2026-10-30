package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func runHintWith(t *testing.T, input string, res protocol.HintResult, err error) (string, []protocol.HintRequest) {
	t.Helper()
	var asked []protocol.HintRequest
	var out bytes.Buffer
	runHint(context.Background(), strings.NewReader(input), &out, func(_ context.Context, r protocol.HintRequest) (protocol.HintResult, error) {
		asked = append(asked, r)
		return res, err
	})
	return out.String(), asked
}

var oneHint = protocol.HintResult{Text: "agent-net: ask cogforge (session s_1)."}

func TestHintHookFailureOutputSchema(t *testing.T) {
	in := `{"session_id":"abc","hook_event_name":"PostToolUseFailure","tool_name":"Bash",
	"tool_input":{"command":"curl x"},"tool_use_id":"t1","error":"Exit code 22\ncurl: (22) returned error: 503","is_interrupt":false}`
	out, asked := runHintWith(t, in, oneHint, nil)
	if len(asked) != 1 || !asked[0].Failed || asked[0].NativeID != "abc" || asked[0].Harness != "claude" ||
		!strings.Contains(asked[0].Text, "503") {
		t.Fatalf("asked: %+v", asked)
	}
	// Documented shape: hookSpecificOutput.hookEventName and additionalContext.
	var got struct {
		H map[string]string `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %q", out)
	}
	if got.H["hookEventName"] != "PostToolUseFailure" || got.H["additionalContext"] != oneHint.Text || len(got.H) != 2 {
		t.Fatalf("output: %q", out)
	}
	if strings.Count(out, "\n") != 1 {
		t.Errorf("one JSON line expected: %q", out)
	}
}

func TestHintHookPostToolUseOnlyBashFailures(t *testing.T) {
	bash := `{"session_id":"abc","hook_event_name":"PostToolUse","tool_name":"Bash","tool_response":{"stdout":"","stderr":"HTTP/1.1 503 Service Unavailable","interrupted":false}}`
	out, asked := runHintWith(t, bash, oneHint, nil)
	if len(asked) != 1 || asked[0].Failed || !strings.Contains(out, `"hookEventName":"PostToolUse"`) {
		t.Fatalf("bash failure: %q %+v", out, asked)
	}
	// Output that does not look like a failure never reaches the daemon.
	ok := `{"session_id":"abc","hook_event_name":"PostToolUse","tool_name":"Bash","tool_response":{"stdout":"all good","stderr":""}}`
	if out, asked := runHintWith(t, ok, oneHint, nil); out != "" || len(asked) != 0 {
		t.Errorf("plain output: %q %+v", out, asked)
	}
	// A file read that contains an error text is not a failed call.
	read := `{"session_id":"abc","hook_event_name":"PostToolUse","tool_name":"Read","tool_response":{"content":"HTTP 503 Service Unavailable"}}`
	if out, asked := runHintWith(t, read, oneHint, nil); out != "" || len(asked) != 0 {
		t.Errorf("read tool: %q %+v", out, asked)
	}
}

func TestHintHookIsSilentOnEveryProblem(t *testing.T) {
	fail := `{"session_id":"abc","hook_event_name":"PostToolUseFailure","tool_name":"Bash","error":"Exit code 1\nconnection refused"}`
	for name, tc := range map[string]struct {
		in  string
		res protocol.HintResult
		err error
	}{
		"daemon error":  {fail, protocol.HintResult{}, errors.New("daemon down")},
		"no suggestion": {fail, protocol.HintResult{}, nil},
		"bad json":      {`{nope`, oneHint, nil},
		"empty":         {``, oneHint, nil},
		"no session":    {`{"hook_event_name":"PostToolUseFailure","error":"x"}`, oneHint, nil},
		"interrupt":     {`{"session_id":"a","hook_event_name":"PostToolUseFailure","error":"x","is_interrupt":true}`, oneHint, nil},
		"other event":   {`{"session_id":"a","hook_event_name":"Stop"}`, oneHint, nil},
		"blank error":   {`{"session_id":"a","hook_event_name":"PostToolUseFailure","error":"  "}`, oneHint, nil},
	} {
		if out, _ := runHintWith(t, tc.in, tc.res, tc.err); out != "" {
			t.Errorf("%s printed %q", name, out)
		}
	}
}

func TestHintHookStopsAfterItsBudget(t *testing.T) {
	var out bytes.Buffer
	start := time.Now()
	runHint(context.Background(), strings.NewReader(`{"session_id":"a","hook_event_name":"PostToolUseFailure","error":"boom"}`), &out,
		func(ctx context.Context, _ protocol.HintRequest) (protocol.HintResult, error) {
			<-ctx.Done()
			return protocol.HintResult{}, ctx.Err()
		})
	if d := time.Since(start); d > time.Second || out.Len() != 0 {
		t.Fatalf("took %v, printed %q", d, out.String())
	}
}
