package protocol

import (
	"strings"
	"testing"
)

// completeBrief passes every deterministic brief check.
const completeBrief = `Goal: The owner asked: "fix the parser crash" (message m_86b8d8cd77f796f0).
Files or areas: internal/gateway/brief.go and docs/reference.md.
Limits: tests use temp dirs and fakes only, never the real setup.
Definition of done: the new test fails first and passes after the fix.
How you are graded: the caller reruns the test and reads the diff.
Evidence:
- the owner's words
- the failing test
- the diff
`

func TestBriefChecksPassACompleteBrief(t *testing.T) {
	if got := CheckWorkerBrief(completeBrief, "normal", "", nil); len(got) != 0 {
		t.Fatalf("complete brief failed: %v", got)
	}
}

func TestBriefChecksNameEachMissingItem(t *testing.T) {
	cases := []struct {
		name string
		task string
		want string
	}{
		{"goal", "Fix the parser crash.\nFiles or areas: internal/gateway/brief.go\nLimits: tests use temp dirs, never the real setup.\nDefinition of done: the test passes.\nHow you are graded: the caller runs it.\nEvidence:\n- one\n- two\n- three\n", "Goal:"},
		{"files", "Goal: The owner asked: \"fix it\".\nLimits: tests use temp dirs, never the real setup.\nDefinition of done: the test passes.\nHow you are graded: the caller runs it.\nEvidence:\n- one\n- two\n- three\n", "Files or areas:"},
		{"limits", "Goal: The owner asked: \"fix it\".\nFiles or areas: internal/gateway/brief.go\nDefinition of done: the test passes.\nHow you are graded: the caller runs it.\nEvidence:\n- one\n- two\n- three\n", "Limits:"},
		{"definition", "Goal: The owner asked: \"fix it\".\nFiles or areas: internal/gateway/brief.go\nLimits: tests use temp dirs, never the real setup.\nHow you are graded: the caller runs it.\nEvidence:\n- one\n- two\n- three\n", "Definition of done:"},
		{"graded", "Goal: The owner asked: \"fix it\".\nFiles or areas: internal/gateway/brief.go\nLimits: tests use temp dirs, never the real setup.\nDefinition of done: the test passes.\nEvidence:\n- one\n- two\n- three\n", "How you are graded:"},
		{"evidence", "Goal: The owner asked: \"fix it\".\nFiles or areas: internal/gateway/brief.go\nLimits: tests use temp dirs, never the real setup.\nDefinition of done: the test passes.\nHow you are graded: the caller runs it.\n", "Evidence:"},
		{"evidence lines", "Goal: The owner asked: \"fix it\".\nFiles or areas: internal/gateway/brief.go\nLimits: tests use temp dirs, never the real setup.\nDefinition of done: the test passes.\nHow you are graded: the caller runs it.\nEvidence:\n- one\n- two\n", "Evidence:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			failures := CheckWorkerBrief(tc.task, "normal", "", nil)
			joined := strings.Join(failures, "\n")
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("failures %q do not name %q", joined, tc.want)
			}
		})
	}
}

// Markdown punctuation around the labels does not hide them.
func TestBriefChecksSeeMarkdownLabels(t *testing.T) {
	task := "## **Goal:** the owner asked \"fix it\"\n" +
		"* **Files or areas:** `internal/gateway/brief.go`\n" +
		"> **Limits:** tests use temp dirs and never the real setup\n" +
		"- **Definition of done:** the test passes\n" +
		"- **How you are graded:** the caller runs it\n" +
		"### Evidence\n- one\n- two\n- three\n"
	if got := CheckWorkerBrief(task, "normal", "", nil); len(got) != 0 {
		t.Fatalf("markdown brief failed: %v", got)
	}
	numbered := "1. **Goal:** the owner asked \"fix it\"\n" +
		"2. **Files or areas:** `internal/gateway/brief.go`\n" +
		"3. **Limits:** tests use temp dirs and never the real setup\n" +
		"4. **Definition of done:** the test passes\n" +
		"5. **How you are graded:** the caller runs it\n" +
		"6. **Evidence:**\n- one\n- two\n- three\n"
	if got := CheckWorkerBrief(numbered, "normal", "", nil); len(got) != 0 {
		t.Fatalf("numbered markdown brief failed: %v", got)
	}
}

func TestBriefModelRuleForSimpleTasks(t *testing.T) {
	failures := CheckWorkerBrief(completeBrief, "simple", "", nil)
	if len(failures) != 1 || failures[0] != BriefFailModel {
		t.Fatalf("simple task with no model: %v", failures)
	}
	if got := CheckWorkerBrief(completeBrief, "simple", "opencode-go/deepseek-v4.1-flash", nil); len(got) != 0 {
		t.Fatalf("simple task with a model failed: %v", got)
	}
	if got := CheckWorkerBrief(completeBrief, "simple", "", []string{"anthropic"}); len(got) != 0 {
		t.Fatalf("simple task with not_family failed: %v", got)
	}
	// A normal task needs neither.
	if got := CheckWorkerBrief(completeBrief, "normal", "", nil); len(got) != 0 {
		t.Fatalf("normal task with no model failed: %v", got)
	}
}

func TestBriefPaths(t *testing.T) {
	got := BriefPaths("Edit internal/gateway/brief.go and docs/reference.md, then check /tmp/x. See https://example.com/not/a/path.")
	want := []string{"internal/gateway/brief.go", "docs/reference.md", "tmp/x"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("paths = %v, want %v", got, want)
	}
}

func TestBriefOverlapAllowed(t *testing.T) {
	if _, ok := BriefOverlapAllowed("Files: a.go"); ok {
		t.Fatal("a brief with no statement allowed overlap")
	}
	if reason, ok := BriefOverlapAllowed("Overlap OK: the other worker finishes first"); !ok || reason != "the other worker finishes first" {
		t.Fatalf("reason = %q ok = %v", reason, ok)
	}
	if _, ok := BriefOverlapAllowed("overlap ok:"); ok {
		t.Fatal("an empty reason allowed overlap")
	}
}

func TestBriefTaskKeyUsesTitleThenGoal(t *testing.T) {
	a := BriefTaskKey("s_caller", "shop", "Fix Parser", "Goal: owner asked\n")
	b := BriefTaskKey("s_caller", "shop", "  fix   parser ", "another task")
	if a != b {
		t.Fatalf("titles did not normalize: %q / %q", a, b)
	}
	c := BriefTaskKey("s_caller", "shop", "", "Goal: the owner asked \"fix the parser\"\nmore")
	d := BriefTaskKey("s_caller", "shop", "", "goal: THE OWNER ASKED \"FIX THE PARSER\"")
	if c != d {
		t.Fatalf("goals did not normalize: %q / %q", c, d)
	}
}
