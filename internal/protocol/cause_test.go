package protocol

import "testing"

// Every cause source agent-net can see is classified. Decision 0141.
func TestWorkerRunCause(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		detail string
		want   string
	}{
		{"provider rate limit", StopModelFails, "provider returned HTTP 429: too many requests", CauseQuotaAuth},
		{"provider sign-in", StopModelFails, "403 forbidden: not logged in", CauseQuotaAuth},
		{"account quota", StopModelFails, "Individual quota reached. Please upgrade. Resets in 160h24m5s.", CauseQuotaAuth},
		{"sandbox denial", StopRunFailed, "write internal/x.go: operation not permitted", CauseSandbox},
		{"idle timeout", StopIdle, "", CauseHarness},
		{"longest run", StopMaxAge, "", CauseHarness},
		{"harness crash", StopExited, "panic: index out of range", CauseHarness},
		{"model call failed", StopModelFails, "the model call failed", CauseHarness},
		{"lost run", StopLost, "", CauseHarness},
		{"normal hand-back", StopHandback, "", CauseUnknown},
		{"owner stopped it", StopRequested, "", CauseUnknown},
		{"no signal", "", "", CauseUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := WorkerRunCause(tc.reason, tc.detail); got != tc.want {
				t.Fatalf("cause = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDefectPhase(t *testing.T) {
	if got := DefectPhase(StepTest, 2); got != DefectCaught {
		t.Fatalf("a test step defect = %q, want caught", got)
	}
	if got := DefectPhase(StepVerify, 1); got != DefectCaught {
		t.Fatalf("a severity 1 defect = %q, want caught", got)
	}
	if got := DefectPhase(StepVerify, 3); got != DefectEscaped {
		t.Fatalf("a severity 3 verify defect = %q, want escaped", got)
	}
	if got := DefectPhase("", 2); got != DefectEscaped {
		t.Fatalf("a severity 2 hand-filed defect = %q, want escaped", got)
	}
}
