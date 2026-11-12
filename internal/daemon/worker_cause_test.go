package daemon

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The exact text a failed Codex turn reports. It reaches the daemon as the
// hand-back report, so the run's failure text and the gateway's cause come
// from here (decision 0145).
const codexUsageLimitReport = "The Codex turn failed: You've hit your usage limit. Upgrade to Pro ... or try again at 3:39 AM.\n\nLast message:\nI could not continue."

func TestHandbackFailureReadsTheProviderText(t *testing.T) {
	got := handbackFailure(protocol.StatusFailed, codexUsageLimitReport)
	if !strings.Contains(got, "You've hit your usage limit") {
		t.Fatalf("failure text = %q, want the provider sentence", got)
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("failure text was not cleaned to one line: %q", got)
	}
	if got := handbackFailure(protocol.StatusDone, "all good"); got != "" {
		t.Fatalf("a done hand-back left failure text %q", got)
	}
	if got := handbackFailure(protocol.StatusBlocked, "BLOCKED: needs approval"); got != "" {
		t.Fatalf("a blocked hand-back left failure text %q", got)
	}
}
