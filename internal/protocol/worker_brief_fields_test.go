package protocol

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/settings"
)

func TestWorkerBriefRendersFieldsInFixedOrder(t *testing.T) {
	brief := WorkerBrief{
		Problem:    "The parser drops the last line. The owner asked.",
		Evidence:   []string{"test A fails", "test B passes"},
		Done:       []string{"the failing test passes", "no new warnings"},
		Hypothesis: "the reader stops at EOF",
		Scope:      "do not touch the writer",
		Notes:      "branch from main",
	}
	text := brief.Text()
	order := []string{
		"Problem:\nThe parser drops the last line. The owner asked.",
		"Evidence:\n- test A fails\n- test B passes",
		"Done:\n- the failing test passes\n- no new warnings",
		"Hypothesis (yours to drop if wrong):\nthe reader stops at EOF",
		"Scope:\ndo not touch the writer",
		"Notes:\nbranch from main",
	}
	at := -1
	for _, want := range order {
		i := strings.Index(text, want)
		if i <= at {
			t.Fatalf("section %q is missing or out of order:\n%s", want, text)
		}
		at = i
	}
}

func TestWorkerBriefKeepsTheOldTaskText(t *testing.T) {
	if got := (WorkerBrief{Notes: "  fix the login  "}).Text(); got != "fix the login" {
		t.Fatalf("old task changed: %q", got)
	}
	p := WorkerStartRequest{Task: "fix the login"}
	if p.BriefText() != "fix the login" {
		t.Fatalf("legacy task changed: %q", p.BriefText())
	}
}

func TestWorkerTaskBodyShowsTheStructuredBrief(t *testing.T) {
	brief := WorkerBrief{Problem: "The parser drops the last line.", Done: []string{"the failing test passes"}}
	body := WorkerTaskBodyBrief("opencode", brief, "", "", "", "", WorkersSandboxed, "", settings.Snapshot{}, "")
	if !strings.HasSuffix(body, "\n\nTask:\nProblem:\nThe parser drops the last line.\n\nDone:\n- the failing test passes") {
		t.Fatalf("body does not end with the structured brief:\n%s", body)
	}
}
