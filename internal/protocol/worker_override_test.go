package protocol

import (
	"reflect"
	"strings"
	"testing"
)

func TestSelectionFields(t *testing.T) {
	cases := []struct {
		name string
		req  WorkerStartRequest
		want []string
	}{
		{"none", WorkerStartRequest{Project: "shop", Task: "x", Title: "t", Isolation: IsolationNone}, nil},
		{"auto model is the picker", WorkerStartRequest{Model: "auto"}, nil},
		{"model", WorkerStartRequest{Model: "gpt-6"}, []string{"model=gpt-6"}},
		{"all", WorkerStartRequest{Harness: "codex", Model: "gpt-6", Provider: "openai", Effort: "high", NotFamily: []string{"openai", "google"}, Machine: "nas"},
			[]string{"harness=codex", "model=gpt-6", "provider=openai", "effort=high", "not_family=openai,google", "machine=nas"}},
		{"blank values are not set", WorkerStartRequest{Harness: " ", Model: "", NotFamily: []string{" "}}, nil},
	}
	for _, tc := range cases {
		if got := tc.req.SelectionFields(); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: SelectionFields = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestWithoutSelection(t *testing.T) {
	req := WorkerStartRequest{Project: "shop", Task: "x", Harness: "codex", Model: "m", Provider: "p", Effort: "e", NotFamily: []string{"a"}, Machine: "nas", OwnerWords: "w", Reason: "r"}
	got := req.WithoutSelection()
	if got.Harness != "" || got.Model != "" || got.Provider != "" || got.Effort != "" || got.NotFamily != nil || got.Machine != "" || got.OwnerWords != "" || got.Reason != "" {
		t.Fatalf("WithoutSelection kept a field: %+v", got)
	}
	if got.Project != "shop" || got.Task != "x" {
		t.Fatalf("WithoutSelection dropped a plain field: %+v", got)
	}
}

func TestOwnerWordsMatch(t *testing.T) {
	prompts := []string{"Please use a  Codex\nworker for this task, thanks", "unrelated"}
	for _, tc := range []struct {
		quote string
		want  bool
	}{
		{"use a codex worker for this", true},
		{"  USE A CODEX WORKER  ", true},
		{"use a claude worker", false},
		{"codex", false}, // too short to prove anything
		{"", false},
		{"the owner said use codex", false},
	} {
		if got := OwnerWordsMatch(tc.quote, prompts); got != tc.want {
			t.Errorf("OwnerWordsMatch(%q) = %v, want %v", tc.quote, got, tc.want)
		}
	}
	if OwnerWordsMatch(strings.Repeat("x", MaxOwnerWordsRunes+1), []string{strings.Repeat("x", 2000)}) {
		t.Error("a quote over the limit matched")
	}
}

func TestStartOverrideText(t *testing.T) {
	got := StartOverrideText("build-bot", []string{"harness=codex", "model=gpt-6"}, "Fix login", "cheaper for this task")
	want := "build-bot wants harness=codex model=gpt-6 for 'Fix login': cheaper for this task. Confirm, or let the picker choose."
	if got != want {
		t.Fatalf("StartOverrideText = %q, want %q", got, want)
	}
	long := StartOverrideText("a", []string{"model=m"}, "t", strings.Repeat("r", 5000))
	if len([]rune(long)) > MaxOwnerAskChars {
		t.Fatalf("text has %d characters, limit %d", len([]rune(long)), MaxOwnerAskChars)
	}
}
