package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func runHintWith(t *testing.T, input string, res protocol.HintResult, err error) (string, []protocol.HintRequest) {
	t.Helper()
	out, asked, _ := runHintWithLead(t, input, res, err, protocol.TeamLeadResult{}, nil)
	return out, asked
}

// runHintWithLead runs the hook with a fake daemon for both its reads. leadRes
// and leadErr answer the team lead read.
func runHintWithLead(t *testing.T, input string, res protocol.HintResult, err error,
	leadRes protocol.TeamLeadResult, leadErr error) (string, []protocol.HintRequest, []protocol.TeamLeadRequest) {
	t.Helper()
	var asked []protocol.HintRequest
	var leads []protocol.TeamLeadRequest
	var out bytes.Buffer
	runHint(context.Background(), strings.NewReader(input), &out,
		func(_ context.Context, r protocol.HintRequest) (protocol.HintResult, error) {
			asked = append(asked, r)
			return res, err
		},
		func(_ context.Context, r protocol.TeamLeadRequest) (protocol.TeamLeadResult, error) {
			leads = append(leads, r)
			return leadRes, leadErr
		})
	return out.String(), asked, leads
}

// deniedMerge is a failed gh pr merge that Claude Code's permission system or
// GitHub refused.
func deniedMerge(failure string) string {
	return `{"session_id":"abc","hook_event_name":"PostToolUseFailure","tool_name":"Bash",` +
		`"tool_input":{"command":"gh pr merge 12 --squash"},"error":` + strconv.Quote(failure) + `}`
}

const leadLine = "agent-net: this action was denied here. Your team sends blocked steps to its lead lead (s_lead): " +
	"send it what you need done (for example the PR link and your review) with agent_message_send, then carry on."

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

func TestHintHookSuccessfulBashUsesStderrAndShortNonReadStdout(t *testing.T) {
	diff := `{"session_id":"abc","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"git diff -- internal/hint"},"tool_response":{"stdout":"error: connection refused","stderr":""}}`
	if out, asked := runHintWith(t, diff, oneHint, nil); out != "" || len(asked) != 0 {
		t.Fatalf("successful git diff triggered a hint: %q %+v", out, asked)
	}

	curl := `{"session_id":"abc","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"curl https://example.invalid"},"tool_response":{"stdout":"502 Bad Gateway","stderr":""}}`
	out, asked := runHintWith(t, curl, oneHint, nil)
	if len(asked) != 1 || asked[0].Failed || !strings.Contains(asked[0].Text, "502 Bad Gateway") || out == "" {
		t.Fatalf("short curl response was ignored: %q %+v", out, asked)
	}

	readError := `{"session_id":"abc","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"cat file"},"tool_response":{"stdout":"all good","stderr":"502 Bad Gateway"}}`
	_, asked = runHintWith(t, readError, oneHint, nil)
	if len(asked) != 1 || asked[0].Text != "502 Bad Gateway" {
		t.Fatalf("read-command stderr was ignored or stdout was included: %+v", asked)
	}
}

func TestHintHookSkipsReadCommandStdout(t *testing.T) {
	commands := []string{
		"git diff -- file", "git log --oneline", "git show HEAD", "git grep error", "git blame file",
		"cat file", "sed -n '1,5p' file", "head -n 5 file", "tail -n 5 file",
		"grep error file", "rg error", "less file", "jq . file",
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			in, err := json.Marshal(map[string]any{
				"session_id": "abc", "hook_event_name": "PostToolUse", "tool_name": "Bash",
				"tool_input":    map[string]string{"command": command},
				"tool_response": map[string]string{"stdout": "502 Bad Gateway", "stderr": ""},
			})
			if err != nil {
				t.Fatal(err)
			}
			if out, asked := runHintWith(t, string(in), oneHint, nil); out != "" || len(asked) != 0 {
				t.Fatalf("read command stdout triggered a hint: %q %+v", out, asked)
			}
		})
	}
}

func TestHintHookIgnoresStdoutAt2000Bytes(t *testing.T) {
	stdout := strings.Repeat("x", 1985) + "502 Bad Gateway"
	if len(stdout) != 2000 {
		t.Fatalf("test output is %d bytes, want 2000", len(stdout))
	}
	in, err := json.Marshal(map[string]any{
		"session_id": "abc", "hook_event_name": "PostToolUse", "tool_name": "Bash",
		"tool_input":    map[string]string{"command": "curl https://example.invalid"},
		"tool_response": map[string]string{"stdout": stdout, "stderr": ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out, asked := runHintWith(t, string(in), oneHint, nil); out != "" || len(asked) != 0 {
		t.Fatalf("2000-byte stdout triggered a hint: %q %+v", out, asked)
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
		},
		func(context.Context, protocol.TeamLeadRequest) (protocol.TeamLeadResult, error) {
			return protocol.TeamLeadResult{}, errors.New("no lead")
		})
	if d := time.Since(start); d > time.Second || out.Len() != 0 {
		t.Fatalf("took %v, printed %q", d, out.String())
	}
}

// A denied gh pr merge in a team that sends blocked steps to its lead prints
// one line naming that lead. The line is the hook's additionalContext, and the
// member sends the message itself.
func TestHintHookPermissionDenialNamesTheTeamLead(t *testing.T) {
	denials := []string{
		"Exit code 1\nHTTP 403: Resource not accessible by integration (https://api.github.com/repos/acme/shop/pulls/12/merge)",
		"Exit code 1\nGraphQL: Resource not accessible by personal access token (mergePullRequest)",
		"Exit code 1\npermission denied",
		"Exit code 1\nremote: Permission to acme/shop.git denied to bot.",
		"Exit code 1\nPermission to use Bash with command gh pr merge 12 --squash has been denied.",
		"Exit code 1\nHTTP 403: Must have admin rights to Repository. (https://api.github.com/repos/acme/shop/actions/workflows/deploy.yml/dispatches)",
	}
	for i, failure := range denials {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			lead := protocol.TeamLeadResult{Team: "forge", Lead: "lead", LeadSession: "s_lead"}
			out, asked, leads := runHintWithLead(t, deniedMerge(failure), oneHint, nil, lead, nil)
			if len(asked) != 0 {
				t.Fatalf("a permission denial also asked for an ordinary hint: %+v", asked)
			}
			if len(leads) != 1 || leads[0].Harness != "claude" || leads[0].NativeID != "abc" {
				t.Fatalf("lead read: %+v", leads)
			}
			var got struct {
				H map[string]string `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("not JSON: %q", out)
			}
			if got.H["hookEventName"] != "PostToolUseFailure" || got.H["additionalContext"] != leadLine || len(got.H) != 2 {
				t.Fatalf("output: %q", out)
			}
			if strings.Count(out, "\n") != 1 {
				t.Errorf("one JSON line expected: %q", out)
			}
		})
	}
}

// An ordinary command error keeps today's behaviour: the ordinary hint, and no
// team lead read. A read-only command that cannot read a file is ordinary too.
func TestHintHookOrdinaryFailureDoesNotAskForALead(t *testing.T) {
	for name, in := range map[string]string{
		"service error": `{"session_id":"abc","hook_event_name":"PostToolUseFailure","tool_name":"Bash",` +
			`"tool_input":{"command":"curl https://example.invalid"},"error":"Exit code 1\nconnection refused"}`,
		"file permission": `{"session_id":"abc","hook_event_name":"PostToolUseFailure","tool_name":"Bash",` +
			`"tool_input":{"command":"cat /root/notes"},"error":"Exit code 1\ncat: /root/notes: Permission denied"}`,
	} {
		out, asked, leads := runHintWithLead(t, in, oneHint, nil,
			protocol.TeamLeadResult{Team: "forge", Lead: "lead", LeadSession: "s_lead"}, nil)
		if len(leads) != 0 {
			t.Fatalf("%s: ordinary failure asked for a lead: %+v", name, leads)
		}
		if len(asked) != 1 || !strings.Contains(out, oneHint.Text) {
			t.Fatalf("%s: ordinary hint: %q %+v", name, out, asked)
		}
	}
}

// A denial outside a team that sends blocked steps to its lead, and a daemon
// that cannot be reached, print nothing new.
func TestHintHookDenialWithoutALeadIsSilent(t *testing.T) {
	for name, tc := range map[string]struct {
		res protocol.TeamLeadResult
		err error
	}{
		"no team":                  {protocol.TeamLeadResult{}, nil},
		"asks_to is not lead":      {protocol.TeamLeadResult{}, nil},
		"daemon cannot be reached": {protocol.TeamLeadResult{}, errors.New("daemon down")},
		"no lead session":          {protocol.TeamLeadResult{Team: "forge", Lead: "lead"}, nil},
	} {
		if out, _, _ := runHintWithLead(t, deniedMerge("Exit code 1\npermission denied"), oneHint, nil, tc.res, tc.err); out != "" {
			t.Errorf("%s printed %q", name, out)
		}
	}
}

// The lead read has the same budget as the hint. A daemon that never answers
// makes the hook give up quietly.
func TestHintHookDenialStopsAfterItsBudget(t *testing.T) {
	var out bytes.Buffer
	start := time.Now()
	runHint(context.Background(), strings.NewReader(deniedMerge("Exit code 1\npermission denied")), &out,
		func(context.Context, protocol.HintRequest) (protocol.HintResult, error) {
			return oneHint, nil
		},
		func(ctx context.Context, _ protocol.TeamLeadRequest) (protocol.TeamLeadResult, error) {
			<-ctx.Done()
			return protocol.TeamLeadResult{}, ctx.Err()
		})
	if d := time.Since(start); d > time.Second || out.Len() != 0 {
		t.Fatalf("took %v, printed %q", d, out.String())
	}
}
