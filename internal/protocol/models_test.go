package protocol

import (
	"errors"
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

// A model that failed a worker recently ranks after every other match, whatever
// its usage, so a retry of the same short request picks the other provider.
// A reasoning or thinking level after the last colon is not part of the model
// id: the name before it is resolved and the suffix is kept.
func TestResolveModelKeepsEffortSuffix(t *testing.T) {
	for _, c := range []struct{ query, want string }{
		{"gpt 6.1 mini:low", "openai/gpt-6.1-mini:low"},
		{"opencode/deepseek-v4.1-flash:xhigh", "opencode/deepseek-v4.1-flash:xhigh"},
		{"deepseek v4 pro:high", "opencode-go/deepseek-v4-pro:high"},
	} {
		got, err := ResolveModel(c.query, testModels, nil, nil)
		if err != nil || got != c.want {
			t.Errorf("ResolveModel(%q) = %q, %v, want %q", c.query, got, err, c.want)
		}
	}
	// A colon that is not a level is part of the id, as the dated suffix is.
	if got, err := ResolveModel("deepseek 4.1 flash:1017", testModels, nil, nil); err != nil || !strings.HasSuffix(got, ":1017") {
		t.Errorf("a numbered suffix: %q, %v", got, err)
	}
}

func TestResolveModelDemotesRecentFailures(t *testing.T) {
	usage := map[string]int{"opencode/deepseek-v4.1-flash": 3}
	r, err := ResolveModelFailed("deepseek 4.1", testModels, usage, nil, nil)
	if err != nil || r.Model != "opencode/deepseek-v4.1-flash" || len(r.Demoted) != 0 {
		t.Fatalf("without a failure: %+v, %v", r, err)
	}
	failed := map[string]bool{"opencode/deepseek-v4.1-flash": true}
	r, err = ResolveModelFailed("deepseek 4.1", testModels, usage, nil, failed)
	if err != nil || r.Model != "opencode-go/deepseek-v4.1-flash" {
		t.Fatalf("after a failure: %+v, %v", r, err)
	}
	if len(r.Demoted) != 1 || r.Demoted[0] != "opencode/deepseek-v4.1-flash" {
		t.Errorf("demoted: %v", r.Demoted)
	}
	// Other models of the failed provider rank after other providers, before the failed model.
	r, err = ResolveModelFailed("deepseek 4.1 flash", []string{"opencode/deepseek-v4.1-flash", "opencode/deepseek-v4.1-flash-free"}, usage, nil, failed)
	if err != nil || r.Model != "opencode/deepseek-v4.1-flash-free" {
		t.Errorf("same provider: %+v, %v", r, err)
	}
	// Nothing else matches: the failed model is still the answer, and it is flagged.
	r, err = ResolveModelFailed("claude sonnet 5 20260801", testModels, nil, nil, map[string]bool{"anthropic/claude-sonnet-5-20260801": true})
	if err != nil || r.Model != "anthropic/claude-sonnet-5-20260801" || len(r.Demoted) != 1 {
		t.Errorf("only candidate: %+v, %v", r, err)
	}
	// An exact id is what the caller asked for: it wins and is not flagged.
	r, err = ResolveModelFailed("opencode/deepseek-v4.1-flash", testModels, usage, nil, failed)
	if err != nil || r.Model != "opencode/deepseek-v4.1-flash" || len(r.Demoted) != 0 {
		t.Errorf("exact: %+v, %v", r, err)
	}
	// ResolveModel itself is unchanged.
	if got, _ := ResolveModel("deepseek 4.1", testModels, usage, nil); got != "opencode/deepseek-v4.1-flash" {
		t.Errorf("ResolveModel: %q", got)
	}
}

// An ambiguity error lists at most five candidates.
// A model that recently finished a worker successfully ranks before the other
// matches, and before usage. A recent failure still ranks last.
func TestResolveModelRanksRecentSuccessFirst(t *testing.T) {
	models := []string{"openai/gpt-6-luna", "openai/gpt-6.1-luna", "openai/gpt-6.1"}
	usage := map[string]int{"openai/gpt-6-luna": 9}
	r, err := ResolveModelRanked("luna 6", models, usage, nil, nil, nil)
	if err != nil || r.Model != "openai/gpt-6-luna" {
		t.Fatalf("without a success: %+v, %v", r, err)
	}
	ok := map[string]bool{"openai/gpt-6.1-luna": true}
	r, err = ResolveModelRanked("luna 6", models, usage, nil, nil, ok)
	if err != nil || r.Model != "openai/gpt-6.1-luna" {
		t.Fatalf("recent success should win: %+v, %v", r, err)
	}
	// A failure wins over a success: the model failed recently is still last.
	failed := map[string]bool{"openai/gpt-6.1-luna": true}
	r, err = ResolveModelRanked("luna 6", models, usage, nil, failed, ok)
	if err != nil || r.Model != "openai/gpt-6-luna" {
		t.Fatalf("failure should still rank last: %+v, %v", r, err)
	}
	// An exact id still wins.
	r, err = ResolveModelRanked("openai/gpt-6.1-luna", models, usage, nil, failed, ok)
	if err != nil || r.Model != "openai/gpt-6.1-luna" || len(r.Demoted) != 0 {
		t.Fatalf("exact: %+v, %v", r, err)
	}
}

// A recent success still ranks first when the other match is on quota pace and
// the successful model's provider is ahead of its reset clock.
func TestResolveModelRankedQuotaKeepsSuccessFirst(t *testing.T) {
	models := []string{"codex/gpt-6-luna", "openai/gpt-6.1"}
	q := &Quotas{Providers: []QuotaProvider{
		quotaAt("codex", window("weekly", "weekly", 40, QuotaPaceAhead)),
		quotaAt("openai", window("weekly", "weekly", 20, QuotaPaceOnPace)),
	}}
	got, err := ResolveModelRankedQuota("gpt 6", models, nil, nil, nil, nil, q)
	if err != nil || got.Model != "openai/gpt-6.1" {
		t.Fatalf("on pace should win without a success: %+v, %v", got, err)
	}
	got, err = ResolveModelRankedQuota("gpt 6", models, nil, nil, nil, map[string]bool{"codex/gpt-6-luna": true}, q)
	if err != nil || got.Model != "codex/gpt-6-luna" {
		t.Fatalf("a recent success should beat the pace warning: %+v, %v", got, err)
	}
}

// A :thinking suffix the caller gives is kept, and the base id is resolved.
func TestResolveModelKeepsThinkingSuffix(t *testing.T) {
	models := []string{"openai/gpt-5", "openai/gpt-5-mini"}
	for _, c := range []struct{ query, want string }{
		{"gpt 5:high", "openai/gpt-5:high"},
		{"openai/gpt-5:low", "openai/gpt-5:low"},
		{"gpt 5 mini:medium", "openai/gpt-5-mini:medium"},
	} {
		got, err := ResolveModel(c.query, models, nil, nil)
		if err != nil || got != c.want {
			t.Errorf("%q: got %q, %v; want %q", c.query, got, err, c.want)
		}
	}
	// A numeric suffix is a dated snapshot, not a thinking level.
	if got, _ := ResolveModel("gpt 5:0813", []string{"a/gpt-5:0813"}, nil, nil); got != "a/gpt-5:0813" {
		t.Errorf("dated snapshot: %q", got)
	}
}

// A name that matches nothing names the closest models, at most five.
func TestResolveModelNoMatchListsClosest(t *testing.T) {
	models := []string{"openai/gpt-6-luna", "openai/gpt-6.1-luna", "openai/gpt-6.1", "openai/gpt-7-sol", "openai/gpt-6.1-mini"}
	_, err := ResolveModel("luna 6.2", models, nil, nil)
	if err == nil {
		t.Fatal("want an error")
	}
	var nm *NoModelMatchError
	if !errors.As(err, &nm) {
		t.Fatalf("error type: %T %v", err, err)
	}
	if len(nm.Closest) == 0 || len(nm.Closest) > 5 || nm.Closest[0] != "openai/gpt-6-luna" {
		t.Fatalf("closest: %v", nm.Closest)
	}
	if !strings.Contains(err.Error(), "no model in the list matches") || !strings.Contains(err.Error(), "closest:") {
		t.Errorf("error: %v", err)
	}
	// Unrelated names give no closest list.
	_, err = ResolveModel("zzz 99", models, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "no model in the list matches") {
		t.Fatalf("error: %v", err)
	}
	var nm2 *NoModelMatchError
	if !errors.As(err, &nm2) || len(nm2.Closest) != 0 {
		t.Errorf("closest for zzz: %+v", nm2)
	}
}

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
	if !strings.HasPrefix(last, "If you learned something") || !strings.Contains(last, "agent_learn") {
		t.Errorf("last line: %q", last)
	}
	if !strings.Contains(body, ChecklistMarker+" Before you tell your user it is done: read the diff in /wt/w_1, run the project's tests there") ||
		!strings.Contains(body, "Merge only if your user asked.") {
		t.Errorf("checklist: %q", body)
	}
	if strings.Index(body, ReportMarker) > strings.Index(body, ChecklistMarker) {
		t.Error("the checklist is inside the worker's report")
	}
	// Without a worktree, and without a report, the checklist is still there
	// and there is no learning line to act on.
	n.Worktree = ""
	body = FormatWorkerNotice(n, "")
	if !strings.Contains(body, "read the changes in the project folder") || strings.Contains(body, ReportMarker) ||
		strings.Contains(body, "agent_learn") {
		t.Errorf("no-worktree notice:\n%s", body)
	}
}
