package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWorkerHandbackFilesRoundTrip(t *testing.T) {
	want := WorkerHandbackRequest{Report: "done", Files: []string{"/work/report.txt", "/work/image.png"}}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got WorkerHandbackRequest
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
	if !strings.Contains(string(b), `"files":[`) {
		t.Fatalf("files missing from JSON: %s", b)
	}
}

func TestHandbackCompleteDefaultsToTrue(t *testing.T) {
	if !(WorkerHandbackRequest{Report: "done"}).IsComplete() {
		t.Error("a hand-back with no complete field must count as complete")
	}
	partial := false
	if (WorkerHandbackRequest{Report: "part", Complete: &partial}).IsComplete() {
		t.Error("complete false was ignored")
	}
	done := true
	if !(WorkerHandbackRequest{Report: "done", Complete: &done}).IsComplete() {
		t.Error("complete true was ignored")
	}
}

func TestTrackRecordLine(t *testing.T) {
	first := TrackRecord{Harness: "pi", Model: "terra", Runs: 4, Complete: 1, Partial: 3,
		MedianRunSeconds: 9 * 60, Resumable: false}
	got := first.Line()
	want := "terra · pi: 4 runs: 1 complete, 3 partial (median 9 min) · cannot resume"
	if got != want {
		t.Errorf("line:\n got %q\nwant %q", got, want)
	}
	second := TrackRecord{Harness: "opencode", Model: "deepseek-v4.1-flash", Runs: 14, Complete: 13,
		MedianRunSeconds: 26 * 60, Resumable: true}
	got = second.Line()
	want = "deepseek-v4.1-flash · opencode: 14 runs: 13 complete (median 26 min) · resumes"
	if got != want {
		t.Errorf("line:\n got %q\nwant %q", got, want)
	}
	// A model name that was not resolved is still readable.
	third := TrackRecord{Harness: "codex", Runs: 1, Failed: 1, MedianRunSeconds: 45, Resumable: true}
	got = third.Line()
	want = "default · codex: 1 run: 1 failed (median 45 s) · resumes"
	if got != want {
		t.Errorf("line:\n got %q\nwant %q", got, want)
	}
	// A stopped run is shown but does not read as a failure.
	fourth := TrackRecord{Harness: "opencode", Model: "deepseek-v4.1-flash", Runs: 3, Complete: 2, Stopped: 1, Resumable: true}
	got = fourth.Line()
	want = "deepseek-v4.1-flash · opencode: 3 runs: 2 complete, 1 stopped · resumes"
	if got != want {
		t.Errorf("line:\n got %q\nwant %q", got, want)
	}
	if got := (*TrackRecord)(nil).Line(); got != "" {
		t.Errorf("a nil track record gave %q", got)
	}
	if got := (&TrackRecord{Harness: "pi"}).Line(); !strings.Contains(got, "no runs yet") || !strings.Contains(got, "cannot resume") {
		t.Errorf("no runs line: %q", got)
	}
}

func TestWorkerOutcome(t *testing.T) {
	yes, no := true, false
	for _, c := range []struct {
		reason, status string
		complete       *bool
		want           string
	}{
		{StopHandback, StatusDone, nil, OutcomeComplete},
		{StopHandback, StatusDone, &yes, OutcomeComplete},
		{StopHandback, StatusDone, &no, OutcomePartial},
		{StopHandback, StatusBlocked, nil, OutcomeFailed},
		{StopHandback, StatusFailed, nil, OutcomeFailed},
		{StopHandback, "", nil, OutcomeComplete}, // an older daemon sends no status
		{StopIdle, "", nil, OutcomeTimedOut},
		{StopIdle, StatusTimedOut, nil, OutcomeTimedOut},
		{StopMaxAge, "", nil, OutcomeTimedOut},
		{StopMaxAge, StatusTimedOut, nil, OutcomeTimedOut},
		// A stop is not the model's fault: a stop on request, a worker lost
		// at a daemon restart, an owner that went away, and a start that
		// never ran the model are neither success nor failure.
		{StopRequested, "", nil, OutcomeStopped},
		{StopRequested, StatusStopped, nil, OutcomeStopped},
		{StopLost, "", nil, OutcomeInfra},
		// The daemon's notice for a lost run says failed to its owner. That
		// is the run's fate, not the model's: it is still neutral.
		{StopLost, StatusFailed, nil, OutcomeInfra},
		{StopOwnerGone, "", nil, OutcomeStopped},
		{StopStartFails, "", nil, OutcomeInfra},
		{StopExited, "", nil, OutcomeFailed},
		{StopRunFailed, "", nil, OutcomeFailed},
		{StopModelFails, "", nil, OutcomeFailed},
	} {
		if got := WorkerOutcome(c.reason, c.status, c.complete); got != c.want {
			t.Errorf("%s/%s/%v: %q, want %q", c.reason, c.status, c.complete, got, c.want)
		}
	}
}

func TestHarnessCanResume(t *testing.T) {
	for _, h := range []string{"opencode", "claude", "codex"} {
		if !HarnessCanResume(h) {
			t.Errorf("%s cannot resume", h)
		}
	}
	if HarnessCanResume("pi") {
		t.Error("Pi cannot open an existing session")
	}
}

func TestWorkerBriefsTellWorkersToSetComplete(t *testing.T) {
	for name, brief := range map[string]string{"worker": WorkerPreamble, "codex": WorkerPreambleCodex} {
		low := strings.ToLower(brief)
		if !strings.Contains(low, "complete") {
			t.Errorf("the %s brief does not mention complete", name)
		}
	}
	if !strings.Contains(WorkerPreamble, "complete: false") {
		t.Error("the worker brief does not say complete: false for partial work")
	}
	if !strings.Contains(WorkerPreambleCodex, "complete: yes") || !strings.Contains(WorkerPreambleCodex, "complete: no") {
		t.Error("the Codex brief does not ask for complete: yes or complete: no")
	}
}

func TestPlaybookNoteIsBoundedAndClean(t *testing.T) {
	model, harness, note, err := CleanPlaybook(" deepseek-v4.1-flash ", " opencode ", "prefers a checklist\nsecond line\x00")
	if err != nil {
		t.Fatal(err)
	}
	if model != "deepseek-v4.1-flash" || harness != "opencode" || strings.ContainsRune(note, 0) || !strings.Contains(note, "\n") {
		t.Errorf("cleaned: %q %q %q", model, harness, note)
	}
	if _, _, _, err := CleanPlaybook("m", "", strings.Repeat("x", PlaybookNoteMaxBytes+1)); err == nil {
		t.Error("an oversized note was accepted")
	}
	if _, _, _, err := CleanPlaybook("", "", "text"); err == nil {
		t.Error("an empty model was accepted")
	}
}

func TestTrackMedianAndShortDuration(t *testing.T) {
	if got := shortDuration(0); got != "0 s" {
		t.Errorf("zero: %q", got)
	}
	if got := shortDuration(90 * time.Second); got != "2 min" {
		t.Errorf("90s: %q", got)
	}
}

func TestWorkerOutcomeInfrastructure(t *testing.T) {
	for _, tc := range []struct{ reason, detail, want string }{
		{StopModelFails, "HTTP 429", "infra"}, {StopModelFails, "RESOURCE_EXHAUSTED", "infra"},
		{StopModelFails, "Individual quota reached", "infra"}, {StopModelFails, "rate limit exceeded", "infra"}, {StopModelFails, "rate_limit", "infra"}, {StopModelFails, "too many requests", "infra"}, {StopModelFails, "status 429", "infra"}, {StopModelFails, "not logged in", "infra"},
		{StopModelFails, "The Codex turn failed: Selected model is at capacity. Please try a different model.", "infra"},
		{StopModelFails, "Anthropic is overloaded", "infra"},
		{StopLost, "", "infra"}, {StopStartFails, "missing Go toolchain", "infra"},
		{StopModelFails, "model crashed", OutcomeFailed}, {StopExited, "", OutcomeFailed},
	} {
		if got := WorkerOutcomeDetail(tc.reason, "", nil, tc.detail); got != tc.want {
			t.Errorf("%s %q: got %s want %s", tc.reason, tc.detail, got, tc.want)
		}
	}
	if poorTrack(TrackRecord{Complete: 3, Infra: 10}) {
		t.Error("infra made track poor")
	}
	if !poorTrack(TrackRecord{Complete: 2, Failed: 3}) {
		t.Error("real failures did not make track poor")
	}
}
