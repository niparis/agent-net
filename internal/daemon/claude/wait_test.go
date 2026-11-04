package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func askInput(questions ...string) map[string]any {
	var qs []any
	for _, q := range questions {
		qs = append(qs, map[string]any{"question": q, "header": "Merge", "multiSelect": false,
			"options": []any{map[string]any{"label": "Merge now", "description": "fast-forward"}, map[string]any{"label": "Wait"}}})
	}
	return map[string]any{"questions": qs}
}

func TestWaitForAskUserQuestionIsTheQuestion(t *testing.T) {
	w := WaitFor("AskUserQuestion", askInput("Merge branch w_1 into main?"))
	if w.Kind != protocol.WaitingQuestion || w.Text != "Merge branch w_1 into main?" || !reflect.DeepEqual(w.Options, []string{"Merge now", "Wait"}) {
		t.Fatalf("one question: %+v", w)
	}
	w = WaitFor("AskUserQuestion", askInput("Merge branch w_1 into main?", "Release after?"))
	if w.Text != "Merge branch w_1 into main? (+1 more)" {
		t.Fatalf("two questions: %+v", w)
	}
	if w := WaitFor("AskUserQuestion", map[string]any{}); w.Kind != protocol.WaitingQuestion || w.Text != "" {
		t.Fatalf("no questions: %+v", w)
	}
}

func TestWaitForOtherTools(t *testing.T) {
	for _, tc := range []struct {
		tool  string
		input map[string]any
		kind  string
		text  string
	}{
		{"Bash", map[string]any{"command": "git push   origin main", "description": "push"}, protocol.WaitingPermission, "Bash(git push origin main)"},
		{"Edit", map[string]any{"file_path": "/work/x/main.go"}, protocol.WaitingPermission, "Edit(/work/x/main.go)"},
		{"mcp__x__y", nil, protocol.WaitingPermission, "mcp__x__y"},
		{"ExitPlanMode", map[string]any{"plan": "1. do it"}, protocol.WaitingPermission, "approve the plan"},
	} {
		if w := WaitFor(tc.tool, tc.input); w.Kind != tc.kind || w.Text != tc.text {
			t.Errorf("%s: %+v", tc.tool, w)
		}
	}
}

func line(v m) string {
	b, _ := json.Marshal(v)
	return string(b) + "\n"
}

func toolUse(id, name string, input m, side bool) string {
	return line(m{"type": "assistant", "isSidechain": side, "message": m{"role": "assistant", "model": "claude-x",
		"content": []m{{"type": "text", "text": "let me ask"}, {"type": "tool_use", "id": id, "name": name, "input": input}}}})
}

func toolResult(id string) string {
	return line(m{"type": "user", "message": m{"role": "user", "content": []m{{"type": "tool_result", "tool_use_id": id, "content": "ok"}}}})
}

func prompt(text string) string {
	return line(m{"type": "user", "message": m{"role": "user", "content": text}})
}

func pendingOf(t *testing.T, lines ...string) *protocol.Waiting {
	t.Helper()
	p := writeTranscript(t, lines...)
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return pendingTool(p, fi.Size())
}

func TestPendingToolFromTheTranscript(t *testing.T) {
	ask := toolUse("t9", "AskUserQuestion", askInput("Merge branch w_1 into main?"), false)
	if w := pendingOf(t, prompt("merge it"), toolUse("t1", "Bash", m{"command": "ls"}, false), toolResult("t1"), ask); w == nil || w.Text != "Merge branch w_1 into main?" {
		t.Fatalf("pending question: %+v", w)
	}
	// Answered: nothing pending.
	if w := pendingOf(t, prompt("merge it"), ask, toolResult("t9")); w != nil {
		t.Fatalf("answered question still pending: %+v", w)
	}
	// A tool waiting for permission after another one ran.
	if w := pendingOf(t, prompt("push"), toolUse("t1", "Read", m{"file_path": "/a"}, false), toolResult("t1"),
		toolUse("t2", "Bash", m{"command": "git push"}, false)); w == nil || w.Kind != protocol.WaitingPermission || w.Text != "Bash(git push)" {
		t.Fatalf("pending permission: %+v", w)
	}
	// An interrupted turn ended with a prompt: its tool is not waiting.
	if w := pendingOf(t, ask, prompt("[Request interrupted by user for tool use]")); w != nil {
		t.Fatalf("interrupted tool pending: %+v", w)
	}
	// A sub-agent's own entries are not the main conversation's wait.
	if w := pendingOf(t, prompt("go"), toolUse("t5", "AskUserQuestion", askInput("side?"), true)); w != nil {
		t.Fatalf("side chain pending: %+v", w)
	}
}

func writeSessionFile(t *testing.T, r *rig, status, waitingFor string) {
	t.Helper()
	extra := ""
	if waitingFor != "" {
		extra = fmt.Sprintf(`,"waitingFor":%q`, waitingFor)
	}
	body := fmt.Sprintf(`{"pid":%d,"sessionId":"sess-1","cwd":"/work/x","name":"claude-x","status":%q%s,"kind":"interactive","messagingSocketPath":%q}`,
		os.Getpid(), status, extra, r.inboxPath)
	if err := os.WriteFile(filepath.Join(r.sessions, fmt.Sprintf("%d.json", os.Getpid())), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Claude Code writes status "waiting" and what it waits for into its session
// file. The adapter reports the wait with the question from the transcript.
func TestAdapterReportsTheHarnessWait(t *testing.T) {
	r := newRig(t)
	projects := filepath.Join(filepath.Dir(r.sessions), "projects")
	transcript := filepath.Join(projects, transcriptDir("/work/x"), "sess-1.jsonl")
	writeLines(t, transcript, strings.TrimSuffix(prompt("merge it"), "\n"),
		strings.TrimSuffix(toolUse("t9", "AskUserQuestion", askInput("Merge branch w_1 into main?"), false), "\n"))
	writeSessionFile(t, r, "waiting", "permission prompt")
	eventually(t, "wait reported", func() bool {
		c := r.host.current()
		return len(c) == 1 && c[0].HarnessWaits && c[0].Waiting != nil && c[0].Waiting.Text == "Merge branch w_1 into main?"
	})
	// The status stays unknown: a worker stuck on a prompt is not busy.
	if s := r.host.current()[0]; s.Status != "unknown" || s.Waiting.Kind != protocol.WaitingQuestion {
		t.Fatalf("waiting session: %+v %+v", s, s.Waiting)
	}

	// The user answered: the session works again.
	writeLines(t, transcript, strings.TrimSuffix(prompt("merge it"), "\n"),
		strings.TrimSuffix(toolUse("t9", "AskUserQuestion", askInput("Merge branch w_1 into main?"), false), "\n"),
		strings.TrimSuffix(toolResult("t9"), "\n"))
	writeSessionFile(t, r, "busy", "")
	eventually(t, "wait ended", func() bool {
		c := r.host.current()
		return len(c) == 1 && !c[0].HarnessWaits && c[0].Waiting == nil && c[0].Status == "busy"
	})

	// A wait the transcript does not explain still counts, with its kind.
	writeSessionFile(t, r, "waiting", "input needed")
	eventually(t, "input wait", func() bool {
		c := r.host.current()
		return len(c) == 1 && c[0].HarnessWaits && c[0].Waiting != nil && c[0].Waiting.Kind == protocol.WaitingInput && c[0].Waiting.Text == ""
	})

	// A dialog the owner opened (/config) is not a wait for the owner.
	writeSessionFile(t, r, "waiting", "dialog open")
	eventually(t, "dialog open", func() bool {
		c := r.host.current()
		return len(c) == 1 && !c[0].HarnessWaits && c[0].Waiting == nil && c[0].Status == "unknown"
	})
}
