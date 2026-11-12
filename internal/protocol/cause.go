package protocol

import "strings"

// Causes of rework (decision 0145). When a worker run fails, quits early, or
// needs a fix round, and when a defect is filed, the gateway stores what it
// already knows about the cause, so the next analysis can tell a bad brief
// from the model or the machine. The cause is never asked from a caller and
// never gates anything.
const (
	// CauseUnknown means the record holds no known cause. It is the stored
	// value for rows written before causes existed and for a run that ended
	// without a signal.
	CauseUnknown = "unknown"
	// CauseQuotaAuth is a provider quota or sign-in refusal (429, quota
	// reached, a 401 or 403, an expired login).
	CauseQuotaAuth = "quota_auth"
	// CauseSandbox is a write or command refused by the worker's sandbox.
	CauseSandbox = "sandbox"
	// CauseHarness is a harness crash, a start that never ran, a lost run, or
	// a run stopped by the idle or longest-run limit.
	CauseHarness = "harness"
	// CauseTestFailure is a failed test step in the merger's suite.
	CauseTestFailure = "test_failure"
	// CauseAcceptFinding is a failed accept step: the caller checked the work and
	// found a fault.
	CauseAcceptFinding = "accept_finding"
	// CauseBriefDefect is the caller's own rating that its brief caused the
	// rework.
	CauseBriefDefect = "brief_defect"
)

// How a defect found later is classified. A defect is caught before release
// when it is severity 1 or the test step found it; otherwise it escaped.
const (
	DefectCaught  = "caught"
	DefectEscaped = "escaped"
)

// ValidWorkerCause reports whether c is one of the stored causes.
func ValidWorkerCause(c string) bool {
	switch c {
	case "", CauseUnknown, CauseQuotaAuth, CauseSandbox, CauseHarness,
		CauseTestFailure, CauseAcceptFinding, CauseBriefDefect:
		return true
	}
	return false
}

// SandboxDenial recognizes a write or command refused by a sandbox. It reads
// the provider or harness error text, not a tool transcript.
func SandboxDenial(detail string) bool {
	detail = strings.ToLower(detail)
	for _, marker := range []string{"sandbox", "operation not permitted", "read-only file system"} {
		if strings.Contains(detail, marker) {
			return true
		}
	}
	return false
}

// WorkerRunCause classifies why a run ended the way it did, from the stop
// reason and the failure text the machine reported. It returns CauseUnknown
// for a run that ended normally or with a signal agent-net does not read.
func WorkerRunCause(reason, detail string) string {
	switch {
	case ProviderQuotaRefusal(detail) || ModelAuthFailure(detail) || AccountQuotaBlocked(detail):
		return CauseQuotaAuth
	case SandboxDenial(detail):
		return CauseSandbox
	}
	switch reason {
	case StopIdle, StopMaxAge, StopModelFails, StopRunFailed, StopStartFails, StopLost, StopExited:
		return CauseHarness
	}
	return CauseUnknown
}

// AccountQuotaBlocked recognizes a provider account quota message such as
// "Individual quota reached ... Resets in 160h24m5s", which is not a 429.
func AccountQuotaBlocked(detail string) bool {
	lower := strings.ToLower(detail)
	return strings.Contains(lower, "quota reached") || strings.Contains(lower, "quota exceeded")
}

// DefectPhase tells whether a defect found later was caught before release or
// escaped. severity 1 and the test step count as caught.
func DefectPhase(step string, severity int) string {
	if step == StepTest || severity <= 1 {
		return DefectCaught
	}
	return DefectEscaped
}
