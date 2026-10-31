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

func runPolicyWith(t *testing.T, input string, res protocol.PolicyResult, err error) (string, []protocol.PolicyRequest) {
	t.Helper()
	var asked []protocol.PolicyRequest
	var out bytes.Buffer
	runPolicyHook(context.Background(), strings.NewReader(input), &out, func(_ context.Context, r protocol.PolicyRequest) (protocol.PolicyResult, error) {
		asked = append(asked, r)
		return res, err
	})
	return out.String(), asked
}

var onePolicy = protocol.PolicyResult{Text: protocol.PolicyLead + "do the code changes yourself."}

func TestPolicyHookOutputSchema(t *testing.T) {
	in := `{"session_id":"abc","prompt_id":"p1","transcript_path":"/x","cwd":"/w","hook_event_name":"UserPromptSubmit","prompt":"fix it"}`
	out, asked := runPolicyWith(t, in, onePolicy, nil)
	if len(asked) != 1 || asked[0].Harness != "claude" || asked[0].NativeID != "abc" {
		t.Fatalf("asked: %+v", asked)
	}
	// Documented shape (https://code.claude.com/docs/en/hooks): hookSpecificOutput
	// with hookEventName and additionalContext, one JSON object on stdout.
	var got struct {
		H map[string]string `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %q", out)
	}
	if got.H["hookEventName"] != "UserPromptSubmit" || got.H["additionalContext"] != onePolicy.Text || len(got.H) != 2 {
		t.Fatalf("output: %q", out)
	}
	if strings.Count(out, "\n") != 1 {
		t.Errorf("one JSON line expected: %q", out)
	}
	if len(got.H["additionalContext"]) > 10000 {
		t.Error("over the 10000 character cap of additionalContext")
	}
}

func TestPolicyHookIsSilentWithoutAPolicyAndOnEveryProblem(t *testing.T) {
	good := `{"session_id":"abc","hook_event_name":"UserPromptSubmit"}`
	for name, c := range map[string]struct {
		in  string
		res protocol.PolicyResult
		err error
	}{
		"no policy":         {good, protocol.PolicyResult{}, nil},
		"daemon down":       {good, onePolicy, errors.New("no daemon")},
		"not json":          {"{", onePolicy, nil},
		"no session":        {`{"hook_event_name":"UserPromptSubmit"}`, onePolicy, nil},
		"another event":     {`{"session_id":"abc","hook_event_name":"PostToolUse"}`, onePolicy, nil},
		"empty input":       {"", onePolicy, nil},
		"only a main model": {good, protocol.PolicyResult{Main: "x"}, nil},
	} {
		if out, _ := runPolicyWith(t, c.in, c.res, c.err); out != "" {
			t.Errorf("%s: printed %q", name, out)
		}
	}
}

func TestPolicyHookStopsAfterItsBudget(t *testing.T) {
	var out bytes.Buffer
	start := time.Now()
	runPolicyHook(context.Background(), strings.NewReader(`{"session_id":"abc","hook_event_name":"UserPromptSubmit"}`), &out,
		func(ctx context.Context, _ protocol.PolicyRequest) (protocol.PolicyResult, error) {
			<-ctx.Done()
			return protocol.PolicyResult{}, ctx.Err()
		})
	if out.Len() != 0 || time.Since(start) > 2*time.Second {
		t.Fatalf("out %q after %v", out.String(), time.Since(start))
	}
}

func TestPolicyHookCarriesTheCharterEvenWithoutAPolicy(t *testing.T) {
	in := `{"session_id":"abc","hook_event_name":"UserPromptSubmit"}`
	charter := "[agent-net] Your node's charter for cogforge, from agent-net:\nmaturity: early - speed over safety.\nDecide, do not defer."
	out, _ := runPolicyWith(t, in, protocol.PolicyResult{Charter: charter}, nil)
	if out == "" {
		t.Fatal("a charter alone printed nothing")
	}
	var got struct {
		H map[string]string `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %q", out)
	}
	if !strings.Contains(got.H["additionalContext"], charter) {
		t.Fatalf("charter missing: %q", got.H["additionalContext"])
	}
	both, _ := runPolicyWith(t, in, protocol.PolicyResult{Text: onePolicy.Text, Charter: charter}, nil)
	if !strings.Contains(both, onePolicy.Text) || !strings.Contains(both, "Decide, do not defer") {
		t.Fatalf("policy and charter not both there: %q", both)
	}
}

func TestPolicyHookTruncatesPrompt(t *testing.T) {
	input := `{"session_id":"abc","prompt":"` + strings.Repeat("é", 400) + `"}`
	_, asked := runPolicyWith(t, input, onePolicy, nil)
	if len(asked) != 1 || len([]rune(asked[0].Prompt)) != 300 {
		t.Fatalf("prompt not truncated: %+v", asked)
	}
}

func TestPolicyHookCarriesOwnerPrompt(t *testing.T) {
	_, asked := runPolicyWith(t, `{"session_id":"abc","hook_event_name":"UserPromptSubmit","prompt":"ask homelab"}`, onePolicy, nil)
	if len(asked) != 1 || asked[0].Prompt != "ask homelab" {
		t.Fatalf("asked: %+v", asked)
	}
}
