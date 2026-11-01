package phrasing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

type fakeWorker struct {
	calls  []protocol.WorkerStartRequest
	output string
	err    error
}

func (f *fakeWorker) Run(ctx context.Context, p protocol.WorkerStartRequest) (string, error) {
	f.calls = append(f.calls, p)
	return f.output, f.err
}
func TestPhraseBatchesAndUsesAutomaticChooser(t *testing.T) {
	f := &fakeWorker{output: `{"token tasks":"GitHub secret management","daily backups":"daily backup operations"}`}
	c := New(f)
	got := c.Clean(context.Background(), time.Unix(1, 0), []string{"token tasks", "daily backups", "token tasks"})
	if len(f.calls) != 1 || got["token tasks"] != "GitHub secret management" || got["daily backups"] != "daily backup operations" {
		t.Fatalf("calls=%+v output=%v", f.calls, got)
	}
	r := f.calls[0]
	if r.Model != "" || r.Harness != "" || r.Difficulty != "trivial" || r.Isolation != protocol.IsolationNone {
		t.Fatalf("not automatic: %+v", r)
	}
	if !strings.Contains(r.Task, "token tasks") || !strings.Contains(r.Task, "daily backups") {
		t.Fatal(r.Task)
	}
}
func TestPhraseFallbackAndCap(t *testing.T) {
	f := &fakeWorker{err: errors.New("no suitable provider")}
	c := New(f)
	now := time.Unix(10000, 0)
	for i := 0; i < 6; i++ {
		got := c.Clean(context.Background(), now.Add(time.Duration(i)*2*time.Minute), []string{"raw phrase"})
		if got["raw phrase"] != "raw phrase" {
			t.Fatal(got)
		}
	}
	got := c.Clean(context.Background(), now.Add(12*time.Minute), []string{"raw phrase"})
	if len(f.calls) != 6 || got["raw phrase"] != "raw phrase" {
		t.Fatalf("cap %d %+v", len(f.calls), got)
	}
}
func TestPhraseBadJSONAndTimeoutFallback(t *testing.T) {
	for _, output := range []string{"garbage", `{"raw phrase":"one"}`, `{"raw phrase":"word word word word word word word"}`, `{"other":"some words"}`, `{"raw phrase":"first; second"}`} {
		f := &fakeWorker{output: output}
		got := New(f).Clean(context.Background(), time.Now(), []string{"raw phrase"})
		if got["raw phrase"] != "raw phrase" {
			t.Fatalf("output=%q got=%v", output, got)
		}
	}
	f := &fakeWorker{err: context.DeadlineExceeded}
	if got := New(f).Clean(context.Background(), time.Now(), []string{"raw phrase"}); got["raw phrase"] != "raw phrase" {
		t.Fatal(got)
	}
}

func TestPhraseCancelledContextDoesNotStart(t *testing.T) {
	f := &fakeWorker{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := New(f).Clean(ctx, time.Now(), []string{"raw phrase"})
	if len(f.calls) != 0 || got["raw phrase"] != "raw phrase" {
		t.Fatalf("cancelled: %+v %v", f.calls, got)
	}
}

func TestPhraseAtMostOncePerWindow(t *testing.T) {
	f := &fakeWorker{err: errors.New("failed")}
	c := New(f)
	now := time.Unix(10000, 0)
	c.Clean(context.Background(), now, []string{"raw phrase"})
	c.Clean(context.Background(), now.Add(time.Minute), []string{"another phrase"})
	if len(f.calls) != 1 {
		t.Fatalf("calls=%d", len(f.calls))
	}
	c.Clean(context.Background(), now.Add(2*time.Minute), []string{"another phrase"})
	if len(f.calls) != 2 {
		t.Fatalf("next window calls=%d", len(f.calls))
	}
}

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
