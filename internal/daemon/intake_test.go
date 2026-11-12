package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"sync"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A failed harness update goes to the CTO intake queue, and a later update
// that works resolves it (decision 0152).
func TestHarnessUpdateReportsToCTOIntake(t *testing.T) {
	var mu sync.Mutex
	var got []protocol.IntakeReport
	d := &Daemon{log: log.New(io.Discard, "", 0)}
	d.installed = []protocol.HarnessInfo{{Name: "codex", Version: "1.0"}}
	d.cfg.testGatewayCall = func(_ context.Context, typ string, params any) (json.RawMessage, bool, error) {
		if typ != "intake.report" {
			return json.RawMessage(`{}`), true, nil
		}
		mu.Lock()
		got = append(got, params.(protocol.IntakeReport))
		mu.Unlock()
		return json.RawMessage(`{"ok":true}`), true, nil
	}
	reports := func() []protocol.IntakeReport {
		mu.Lock()
		defer mu.Unlock()
		return append([]protocol.IntakeReport(nil), got...)
	}
	d.setHarnessUpdate(context.Background(), "codex", &protocol.HarnessUpdate{Result: "failed", Detail: "upgrade failed: no network", Before: "1.0"})
	eventually(t, "failure report", func() bool { return len(reports()) == 1 })
	r := reports()[0]
	if r.Kind != protocol.IntakeHarnessUpdate || r.Subject != "codex" || r.Resolved || r.Text != "codex update failed: upgrade failed: no network" {
		t.Fatalf("failure report = %+v", r)
	}
	// A probe that will retry says nothing yet.
	d.setHarnessUpdate(context.Background(), "codex", &protocol.HarnessUpdate{Result: "timed out, retrying"})
	d.setHarnessUpdate(context.Background(), "codex", &protocol.HarnessUpdate{Result: "updated", Before: "1.0", After: "1.1"})
	eventually(t, "resolve report", func() bool { return len(reports()) == 2 })
	if r := reports()[1]; r.Kind != protocol.IntakeHarnessUpdate || !r.Resolved {
		t.Fatalf("resolve report = %+v", r)
	}
}
