package protocol

import (
	"strings"
	"testing"
)

// A model list in the style of a real OpenCode machine.
var testModels = []string{
	"opencode-go/deepseek-v4.1-flash",
	"opencode/deepseek-v4.1-flash",
	"opencode/deepseek-v4.1-flash-free",
	"opencode-go/deepseek-v4-pro",
	"ollama-cloud/deepseek-v4.1-flash:1017",
	"anthropic/claude-sonnet-5",
	"anthropic/claude-sonnet-5-20260801",
	"openai/gpt-6.1",
	"openai/gpt-6.1-mini",
	"openai/gpt-6",
}

func TestResolveModel(t *testing.T) {
	cases := []struct {
		name      string
		query     string
		usage     map[string]int
		providers []string
		want      string
		errHas    []string
	}{
		{name: "exact match wins", query: "opencode/deepseek-v4.1-flash-free", want: "opencode/deepseek-v4.1-flash-free"},
		{name: "exact match wins over a shorter loose match", query: "openai/gpt-6", want: "openai/gpt-6"},
		{name: "one candidate", query: "gpt 6.1 mini", want: "openai/gpt-6.1-mini"},
		{name: "v4-pro is not 4.1", query: "deepseek 4.1 pro", errHas: []string{"no model in the list matches"}},
		{name: "v in the query is optional, case does not matter", query: "DeepSeek v4 pro", want: "opencode-go/deepseek-v4-pro"},
		{name: "a digit word does not match inside a longer number", query: "gpt 1", errHas: []string{"no model in the list matches"}},
		{name: "free and dated variants lose", query: "sonnet", want: "anthropic/claude-sonnet-5"},
		{name: "same model from two providers is a tie", query: "deepseek 4.1",
			errHas: []string{"matches 2 models equally well", "opencode-go/deepseek-v4.1-flash", "opencode/deepseek-v4.1-flash"}},
		{name: "usage breaks the tie", query: "deepseek 4.1", usage: map[string]int{"opencode-go/deepseek-v4.1-flash": 3, "opencode/deepseek-v4.1-flash": 1},
			want: "opencode-go/deepseek-v4.1-flash"},
		{name: "provider usage resolves a model never used itself", query: "deepseek 4.1", usage: map[string]int{"opencode-go/minimax-m3.1-flash": 4},
			want: "opencode-go/deepseek-v4.1-flash"},
		{name: "provider usage beats the provider list", query: "deepseek 4.1", usage: map[string]int{"opencode/other": 2},
			providers: []string{"opencode-go"}, want: "opencode/deepseek-v4.1-flash"},
		{name: "model usage beats provider usage", query: "deepseek 4.1",
			usage: map[string]int{"opencode-go/minimax-m3.1-flash": 9, "opencode/deepseek-v4.1-flash": 1}, want: "opencode/deepseek-v4.1-flash"},
		{name: "usage beats the provider list", query: "deepseek 4.1", usage: map[string]int{"opencode/deepseek-v4.1-flash": 2},
			providers: []string{"opencode-go"}, want: "opencode/deepseek-v4.1-flash"},
		{name: "equal usage falls through to the provider list", query: "deepseek 4.1",
			usage:     map[string]int{"opencode/deepseek-v4.1-flash": 2, "opencode-go/deepseek-v4.1-flash": 2},
			providers: []string{"ollama-cloud", "opencode-go"}, want: "opencode-go/deepseek-v4.1-flash"},
		{name: "provider list alone", query: "deepseek 4.1", providers: []string{"opencode-go", "opencode"}, want: "opencode-go/deepseek-v4.1-flash"},
		{name: "usage can choose a free model", query: "deepseek 4.1", usage: map[string]int{"opencode/deepseek-v4.1-flash-free": 5},
			want: "opencode/deepseek-v4.1-flash-free"},
		{name: "no match", query: "llama 9", errHas: []string{`no model in the list matches "llama 9"`}},
		{name: "empty words", query: " -- ", errHas: []string{"no letters or digits"}},
	}
	for _, c := range cases {
		got, err := ResolveModel(c.query, testModels, c.usage, c.providers)
		if len(c.errHas) > 0 {
			if err == nil {
				t.Errorf("%s: got %q, want an error", c.name, got)
				continue
			}
			for _, w := range c.errHas {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("%s: error %q lacks %q", c.name, err, w)
				}
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

// An ambiguity error lists at most five candidates.
func TestResolveModelListsFiveAtMost(t *testing.T) {
	var models []string
	for _, p := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		models = append(models, p+"/same-model")
	}
	_, err := ResolveModel("same", models, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "matches 7 models") || strings.Contains(err.Error(), "f/same-model") {
		t.Fatalf("error: %v", err)
	}
}

func TestNoticeEndsWithTheChecklist(t *testing.T) {
	n := WorkerNotice{Status: StatusDone, Session: "s_1", Harness: "opencode", Project: "shop", Host: "h", Worktree: "/wt/w_1", Branch: "agent-net/w_1"}
	body := FormatWorkerNotice(n, "I did it.")
	last := body[strings.LastIndex(body, "\n")+1:]
	if !strings.HasPrefix(last, ChecklistMarker) || !strings.Contains(last, "read the diff in /wt/w_1, run the project's tests there") ||
		!strings.Contains(last, "Merge only if your user asked.") {
		t.Errorf("last line: %q", last)
	}
	if strings.Index(body, ReportMarker) > strings.Index(body, ChecklistMarker) {
		t.Error("the checklist is inside the worker's report")
	}
	// Without a worktree, and without a report, the line is still there.
	n.Worktree = ""
	body = FormatWorkerNotice(n, "")
	if !strings.Contains(body, "read the changes in the project folder") || strings.Contains(body, ReportMarker) {
		t.Errorf("no-worktree notice:\n%s", body)
	}
}
