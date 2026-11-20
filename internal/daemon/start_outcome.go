package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A start that leaves the caller's hands ends in a notice to the caller: the
// worker started, waits in the queue, or did not start. The machine tells the
// gateway, and the gateway makes the notice. These reports used to live in the
// memory of the daemon, so a restart that came while a start was launching
// lost them: the caller heard nothing.
//
// Each report is kept in daemon.db (table start_outcome_outbox) until the
// gateway has taken it or refused it. A start that is launching holds a
// not-started report that is not ready yet. The launch replaces it with the
// real outcome. If the daemon stops first, the held report is made ready when
// the next daemon starts, and goes out after the next connect.

const (
	outcomeNotStarted  = "not_started"
	outcomeStartReport = "start_report"
)

// restartedStartNote is the reason of a held report that a restart made ready.
const restartedStartNote = "the daemon restarted while this start was launching, so it may not have started. Look at the workers list before you start it again"

func notStartedOutcomeKey(ref string) string { return "ns:" + ref }
func startReportOutcomeKey(id string) string { return "sr:" + id }

// putStartOutcome keeps a report, replacing any report of the same key.
func (s *state) putStartOutcome(key, kind string, payload any, ready bool) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	r := 0
	if ready {
		r = 1
	}
	_, err = s.db.Exec(`INSERT OR REPLACE INTO start_outcome_outbox(key,kind,payload,ready,at) VALUES (?,?,?,?,?)`,
		key, kind, string(raw), r, time.Now().Unix())
	return err
}

func (s *state) dropStartOutcome(key string) {
	s.db.Exec(`DELETE FROM start_outcome_outbox WHERE key=?`, key)
}

// readyStartOutcomes lists the reports that wait to be sent.
func (s *state) readyStartOutcomes() ([]startOutcomeRow, error) {
	rows, err := s.db.Query(`SELECT key,kind,payload FROM start_outcome_outbox WHERE ready=1 ORDER BY at, key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []startOutcomeRow
	for rows.Next() {
		var r startOutcomeRow
		if err := rows.Scan(&r.Key, &r.Kind, &r.Payload); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// readyHeldStartOutcomes makes every held report ready. It runs once, when the
// daemon starts: a held report belongs to a launch that the last daemon never
// finished.
func (s *state) readyHeldStartOutcomes() (int64, error) {
	res, err := s.db.Exec(`UPDATE start_outcome_outbox SET ready=1 WHERE ready=0`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

type startOutcomeRow struct {
	Key, Kind, Payload string
}

// holdStartOutcome records, before a launch begins, the report that the caller
// gets if the daemon stops before the launch ends.
func (d *Daemon) holdStartOutcome(key, kind string, payload any) {
	if err := d.st.putStartOutcome(key, kind, payload, false); err != nil {
		d.Logf("start outcome %s: could not keep it: %v", key, err)
	}
}

// holdQueuedStart is holdStartOutcome for a start that left the queue.
func (d *Daemon) holdQueuedStart(e queuedStart) {
	d.holdStartOutcome(notStartedOutcomeKey(e.ID), outcomeNotStarted,
		protocol.WorkerNotStarted{Ref: e.ID, Owner: e.Owner, Project: e.Project, Harness: e.Harness, Reason: restartedStartNote, Permissions: e.Permissions})
}

// holdAcceptedStart is holdStartOutcome for a start that was answered
// "accepted" and goes on in the background.
func (d *Daemon) holdAcceptedStart(id, caller string, p protocol.WorkerStartRequest) {
	d.holdStartOutcome(startReportOutcomeKey(id), outcomeStartReport,
		protocol.WorkerStartReport{ID: id, Owner: caller, Project: p.Project, Harness: p.Harness, Status: protocol.StartReportNotStarted, Note: restartedStartNote})
}

// reportStartOutcome keeps a report and sends it. The send is tried a few
// times with a growing pause while the gateway does not answer or the link is
// down, and stops when the gateway accepts or refuses it. A report that was not
// taken stays on disk for the next connect.
func (d *Daemon) reportStartOutcome(ctx context.Context, key, kind string, payload any) {
	if err := d.st.putStartOutcome(key, kind, payload, true); err != nil {
		d.Logf("start outcome %s: could not keep it: %v", key, err)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	d.sendStartOutcome(ctx, startOutcomeRow{Key: key, Kind: kind, Payload: string(raw)}, notStartedTries)
}

// flushStartOutcomes sends the reports that an earlier run or an earlier link
// could not. Each is tried once: the next connect or the queue tick tries again.
func (d *Daemon) flushStartOutcomes(ctx context.Context) {
	rows, err := d.st.readyStartOutcomes()
	if err != nil {
		d.Logf("start outcome outbox: %v", err)
		return
	}
	for _, r := range rows {
		if ctx.Err() != nil {
			return
		}
		d.sendStartOutcome(ctx, r, 1)
	}
}

// sendStartOutcome makes the call for a report, tries times, and drops the
// report once the gateway has accepted or refused it.
func (d *Daemon) sendStartOutcome(ctx context.Context, r startOutcomeRow, tries int) {
	if !d.claimOutcome(r.Key) {
		return // another send of the same report is under way
	}
	defer d.releaseOutcome(r.Key)
	frame := "worker.not_started"
	if r.Kind == outcomeStartReport {
		frame = "worker.start_report"
	}
	var payload any
	switch r.Kind {
	case outcomeStartReport:
		var p protocol.WorkerStartReport
		if json.Unmarshal([]byte(r.Payload), &p) != nil {
			d.st.dropStartOutcome(r.Key)
			return
		}
		payload = p
	default:
		var p protocol.WorkerNotStarted
		if json.Unmarshal([]byte(r.Payload), &p) != nil {
			d.st.dropStartOutcome(r.Key)
			return
		}
		payload = p
	}
	pause := d.cfg.BackoffMin
	for attempt := 1; attempt <= tries; attempt++ {
		rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := d.call(rctx, frame, payload)
		cancel()
		var refused RemoteError
		// A refusal is final. A fault of the gateway's own, marked by the
		// gateway, and a call that did not get through are tried again.
		if err == nil || (errors.As(err, &refused) && !errors.Is(err, errGatewayRetry)) {
			if err != nil {
				d.Logf("start outcome %s: report refused: %v", r.Key, err)
			}
			d.st.dropStartOutcome(r.Key)
			return
		}
		d.Logf("start outcome %s: report: %v (attempt %d of %d)", r.Key, err, attempt, tries)
		if attempt == tries {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(pause):
		}
		pause *= 2
	}
}

func (d *Daemon) claimOutcome(key string) bool {
	d.outcomeMu.Lock()
	defer d.outcomeMu.Unlock()
	if d.outcomeBusy[key] {
		return false
	}
	if d.outcomeBusy == nil {
		d.outcomeBusy = map[string]bool{}
	}
	d.outcomeBusy[key] = true
	return true
}

func (d *Daemon) releaseOutcome(key string) {
	d.outcomeMu.Lock()
	delete(d.outcomeBusy, key)
	d.outcomeMu.Unlock()
}
