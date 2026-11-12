package daemon

import (
	"context"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// reportIntake sends one CTO intake report (decision 0152) in the background.
// A lost report is not retried: the failure repeats and is reported again.
func (d *Daemon) reportIntake(r protocol.IntakeReport) {
	d.goBackground(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := d.call(ctx, "intake.report", r); err != nil {
			d.Logf("CTO intake %s %s: %v", r.Kind, r.Subject, err)
		}
	})
}

// harnessUpdateIntake is the report of one update attempt: a failure is an
// alert, a success resolves it. A probe that will retry says nothing yet.
func harnessUpdateIntake(name string, u *protocol.HarnessUpdate) (protocol.IntakeReport, bool) {
	r := protocol.IntakeReport{Kind: protocol.IntakeHarnessUpdate, Subject: name}
	switch u.Result {
	case "failed":
		r.Text, r.Detail = name+" update failed"+updateLogDetail(u.Detail), "Version before: "+u.Before
		return r, true
	case "updated", "already current":
		r.Resolved = true
		return r, true
	}
	return r, false
}
