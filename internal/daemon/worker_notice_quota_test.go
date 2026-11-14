package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func agyQuota(used float64) *protocol.Quotas {
	return &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "agy", Plan: "google", Windows: []protocol.QuotaWindow{
		{ID: "gemini_argon_weekly", Label: "Gemini Argon weekly", Kind: "weekly", PercentUsed: used},
		{ID: "gemini_5h", Label: "Gemini 5-hour", Kind: "session", PercentUsed: 0},
	}}}}
}

// The notice records the cached provider quota the picker's floor reads, at
// the worker's start and at its end, without asking quota-axi again.
func TestWorkerNoticeRecordsQuotaAtStartAndEnd(t *testing.T) {
	d := struggleTestDaemon(t, "agy", agyLauncher{})
	d.quota.set(agyQuota(0))
	w := struggleTestWorker("agy", "native")
	w.model = "gemini-4-argon-high"
	w.quotaStart = d.quotaWorkerReading(w.harness, w.model)
	if w.quotaStart.name != "agy Gemini Argon weekly" || w.quotaStart.percent != 0 || !w.quotaStart.ok {
		t.Fatalf("start reading = %+v", w.quotaStart)
	}
	d.quota.set(agyQuota(12))
	n := d.notice(w, protocol.StatusDone, "", workerStats{})
	text := protocol.FormatWorkerNotice(n, "")
	if want := "quota: agy Gemini Argon weekly 0% at start, 12% at end\n"; !strings.Contains(text, want) {
		t.Fatalf("notice lacks %q:\n%s", want, text)
	}
}

// A provider with no usable reading still gets the line, so the owner can see
// that the run's quota was not recorded. A harness with no quota provider gets
// no line at all.
func TestWorkerNoticeQuotaSaysNoReading(t *testing.T) {
	d := struggleTestDaemon(t, "agy", agyLauncher{})
	w := struggleTestWorker("agy", "native")
	w.model = "gemini-4-argon-high"
	n := d.notice(w, protocol.StatusDone, "", workerStats{})
	if text := protocol.FormatWorkerNotice(n, ""); !strings.Contains(text, "quota: no reading at start, no reading at end\n") {
		t.Fatalf("notice lacks the no-reading line:\n%s", text)
	}

	d = struggleTestDaemon(t, "pi", piLauncher{})
	w = struggleTestWorker("pi", "native")
	w.model = "gpt-6"
	n = d.notice(w, protocol.StatusDone, "", workerStats{})
	if text := protocol.FormatWorkerNotice(n, ""); strings.Contains(text, "quota:") {
		t.Fatalf("pi notice has a quota line:\n%s", text)
	}
}

// A started worker takes its start reading from the daemon's cached quota copy
// and its end reading when the hand-back notice is made.
func TestAgyWorkerNoticeRecordsTheQuotaAtItsStart(t *testing.T) {
	t.Parallel()
	w, dir, _ := startAgyNode(t, shortSet)
	w.d.quota.set(&protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "agy", Windows: []protocol.QuotaWindow{
		{ID: "gemini_weekly", Label: "Gemini weekly", Kind: "weekly", PercentUsed: 3},
	}}}})
	// Hold the result event so the end reading can change after the start.
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:handback FAKE:result-gate", Isolation: protocol.IsolationWorktree})
	w.d.quota.set(&protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "agy", Windows: []protocol.QuotaWindow{
		{ID: "gemini_weekly", Label: "Gemini weekly", Kind: "weekly", PercentUsed: 9},
	}}}})
	if err := os.WriteFile(filepath.Join(dir, "result-release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	n := w.waitNotice(t, 1)[0]
	if want := "quota: agy Gemini weekly 3% at start, 9% at end\n"; !strings.Contains(n.Body, want) {
		t.Fatalf("notice lacks %q:\n%s", want, n.Body)
	}
}
