package phrasing

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestSkillSummaryUsesWorkerAndCaches(t *testing.T) {
	w := &summaryTestWorker{}
	c := New(w)
	first, err := c.Summarize(context.Background(), "one", "two", "first text", "second text")
	if err != nil || first != "a adds tests; b uses the old install command" {
		t.Fatalf("summary %q: %v", first, err)
	}
	second, err := c.Summarize(context.Background(), "one", "two", "first text", "second text")
	if err != nil || second != first || w.calls != 1 {
		t.Fatalf("cache: %q %v calls %d", second, err, w.calls)
	}
}

type summaryTestWorker struct{ calls int }

func (w *summaryTestWorker) Run(_ context.Context, req protocol.WorkerStartRequest) (string, error) {
	w.calls++
	if req.Difficulty != "trivial" || req.Model != "" || req.Harness != "" || req.Isolation != protocol.IsolationNone || !strings.Contains(req.Task, "first text") || !strings.Contains(req.Task, "second text") {
		return "", fmt.Errorf("bad request")
	}
	return "a adds tests; b uses the old install command", nil
}
