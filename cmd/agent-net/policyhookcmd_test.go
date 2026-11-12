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

	"github.com/niparis/agent-net/internal/protocol"
)

func runPolicyWith(t *testing.T, input string, res protocol.PolicyResult, err error) (string, []protocol.PolicyRequest) {
	t.Helper()
	var asked []protocol.PolicyRequest
	var out bytes.Buffer
	runPolicyHook(context.Background(), strings.NewReader(input), &out, func(_ context.Context, r protocol.PolicyRequest) (protocol.PolicyResult, error) {
		asked = append(asked, r)
		return res, err
	}, nil)
	return out.String(), asked
}

func runPolicyCompact(t *testing.T, input string) (string, []protocol.CompactionRequest) {
	t.Helper()
	var compacted []protocol.CompactionRequest
	var out bytes.Buffer
	runPolicyHook(context.Background(), strings.NewReader(input), &out, func(_ context.Context, r protocol.PolicyRequest) (protocol.PolicyResult, error) {
		return onePolicy, nil
	}, func(_ context.Context, r protocol.CompactionRequest) error {
		compacted = append(compacted, r)
		return nil
	})
	return out.String(), compacted
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
		}, nil)
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

func TestPolicyHookReportsCompaction(t *testing.T) {
	for name, in := range map[string]string{
		"PostCompact":          `{"session_id":"abc","hook_event_name":"PostCompact","trigger":"auto"}`,
		"SessionStart compact": `{"session_id":"abc","hook_event_name":"SessionStart","source":"compact"}`,
	} {
		out, compacted := runPolicyCompact(t, in)
		if out != "" {
			t.Errorf("%s printed %q", name, out)
		}
		if len(compacted) != 1 || compacted[0].Harness != "claude" || compacted[0].NativeID != "abc" {
			t.Errorf("%s: %+v", name, compacted)
		}
	}
	// A normal start does not mean the transcript compacted.
	out, compacted := runPolicyCompact(t, `{"session_id":"abc","hook_event_name":"SessionStart","source":"startup"}`)
	if out != "" || len(compacted) != 0 {
		t.Fatalf("startup: %q %+v", out, compacted)
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

func TestPolicyHookCarriesTheDisconnectedNoticeAlone(t *testing.T) {
	in := `{"session_id":"abc","hook_event_name":"UserPromptSubmit"}`
	out, _ := runPolicyWith(t, in, protocol.PolicyResult{Notice: protocol.ToolsDisconnectedNotice}, nil)
	var got struct {
		H map[string]string `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %q", out)
	}
	if got.H["additionalContext"] != "[agent-net] Your agent-net tools are disconnected. Tell the CTO or agent-net; the CTO can restart this session (session_change restart). Until then you cannot message other agents." {
		t.Fatalf("context: %q", got.H["additionalContext"])
	}
	// With a charter, the notice comes after it.
	out, _ = runPolicyWith(t, in, protocol.PolicyResult{Charter: "charter text", Notice: "notice line"}, nil)
	if !strings.Contains(out, `charter text\n\nnotice line`) {
		t.Fatalf("order: %q", out)
	}
}

// A turn that ended on a question that went to the chief gets the documented
// Stop answer (decision block with the reason), so Claude Code goes on. A turn
// a Stop hook already continued always ends, and a plain turn end prints
// nothing.
func TestPolicyHookStopContinuesOnce(t *testing.T) {
	in := `{"session_id":"abc","transcript_path":"/x","hook_event_name":"Stop","stop_hook_active":false,"last_assistant_message":"Done.\nOwner question: ship it?"}`
	out, asked := runPolicyWith(t, in, protocol.PolicyResult{Continue: protocol.OwnerQuestionRedirect}, nil)
	if len(asked) != 1 || asked[0].Event != "Stop" || asked[0].NativeID != "abc" || asked[0].LastMessage != "Done.\nOwner question: ship it?" {
		t.Fatalf("asked: %+v", asked)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil || got["decision"] != "block" || got["reason"] != protocol.OwnerQuestionRedirect || len(got) != 2 {
		t.Fatalf("output: %q", out)
	}
	active := strings.Replace(in, `"stop_hook_active":false`, `"stop_hook_active":true`, 1)
	if out, asked := runPolicyWith(t, active, protocol.PolicyResult{Continue: protocol.OwnerQuestionRedirect}, nil); out != "" || len(asked) != 0 {
		t.Fatalf("a continued turn: output %q, asked %+v", out, asked)
	}
	if out, _ := runPolicyWith(t, in, protocol.PolicyResult{}, nil); out != "" {
		t.Fatalf("a plain turn end: output %q", out)
	}
}

// Claude Code versions without last_assistant_message: the hook reads the
// turn's final text from the end of the transcript, skipping subagent turns.
func TestPolicyHookStopReadsTheTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	lines := []string{
		`{"type":"user","message":{"content":"go"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Done.\nOwner question: which region?"}]}}`,
		`{"type":"assistant","isSidechain":true,"message":{"content":[{"type":"text","text":"subagent text"}]}}`,
		`{"type":"system","subtype":"stop_hook_summary"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in, _ := json.Marshal(map[string]any{"session_id": "abc", "hook_event_name": "Stop", "transcript_path": path})
	_, asked := runPolicyWith(t, string(in), protocol.PolicyResult{}, nil)
	if len(asked) != 1 || asked[0].LastMessage != "Done.\nOwner question: which region?" {
		t.Fatalf("asked: %+v", asked)
	}
}
