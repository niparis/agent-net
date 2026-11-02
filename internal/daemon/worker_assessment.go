package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/difficulty"
	"github.com/niparis/agent-net/internal/protocol"
)

// startWorkerAssessment never waits for the judge, gateway, or notice. Each
// actual start gets a distinct run ID, including a resumed session.
func (d *Daemon) startWorkerAssessment(w *worker, project, task string) {
	d.wmu.Lock()
	sid, owner, used := w.sessionID, w.owner, w.difficultyUsed
	d.wmu.Unlock()
	if used == "" {
		used = "normal"
	}
	runID, started := protocol.NewID("k"), time.Now().UnixNano()
	d.goBackground(func() {
		ctx, cancel := context.WithTimeout(d.ctoCtx, 60*time.Second)
		cfg := d.cfg
		cfg.DifficultyAnalyzer = false
		cfg.DifficultyTimeout = 60 * time.Second
		judge := NewDifficultyAssessor(cfg, DifficultySources{Binary: d.harnessBinary})
		result, err := judge.Assess(ctx, project, task)
		cancel()
		harness, model, _ := strings.Cut(result.JudgeModel, ":")
		a := protocol.WorkerAssessment{RunID: runID, SessionID: sid, StartedAt: started, AssessedAt: time.Now().Unix(),
			Reasoning: result.Scores.Reasoning, Scope: result.Scores.Scope, Coupling: result.Scores.Coupling,
			Uncertainty: result.Scores.Uncertainty, Verification: result.Scores.Verification, Risk: result.Scores.Risk,
			D: result.D, Label: result.Label, Bottleneck: result.Bottleneck, Confidence: result.Confidence,
			RulesVersion: difficulty.RulesVersion, JudgeHarness: harness, JudgeModel: model, Reasons: result.Scores.Reasons, PickerLabel: used}
		if err != nil || result.Scores.Reasoning == 0 {
			a.Label = ""
			a.Error = strings.TrimSuffix(strings.TrimPrefix(result.Notes, "difficulty: "), "; started at normal")
			if a.Error == "" && err != nil {
				a.Error = err.Error()
			}
			if a.Error == "" {
				a.Error = "judge returned no scores"
			}
		}
		// A separate deadline lets a timed-out judge report its failure.
		reportCtx, reportCancel := context.WithTimeout(d.ctoCtx, 10*time.Second)
		defer reportCancel()
		if err := d.st.enqueueWorkerAssessment(a); err != nil {
			d.Logf("worker %s: could not queue difficulty assessment: %v", sid, err)
		} else {
			d.flushWorkerAssessments(reportCtx)
		}
		// A notification (not a reply) cannot complete the caller's pending wait.
		_, err = d.send(reportCtx, protocol.MsgSend{Sender: sid, Recipient: owner, Type: protocol.TypeNotification,
			Body: protocol.WorkerAssessmentNotice(sid, a), IdempotencyKey: runID})
		if err != nil {
			d.Logf("worker %s: difficulty notice: %v", sid, err)
		}
	})
}

// Keep results until the gateway has accepted them, including across reconnects.
func (s *state) enqueueWorkerAssessment(a protocol.WorkerAssessment) error {
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT OR IGNORE INTO worker_assessment_outbox(run_id,payload) VALUES (?,?)`, a.RunID, string(raw))
	return err
}

func (s *state) pendingWorkerAssessments() ([]protocol.WorkerAssessment, error) {
	rows, err := s.db.Query(`SELECT payload FROM worker_assessment_outbox ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.WorkerAssessment
	for rows.Next() {
		var raw string
		var a protocol.WorkerAssessment
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (d *Daemon) flushWorkerAssessments(ctx context.Context) {
	d.st.assessmentMu.Lock()
	defer d.st.assessmentMu.Unlock()
	pending, err := d.st.pendingWorkerAssessments()
	if err != nil {
		d.Logf("difficulty assessment outbox: %v", err)
		return
	}
	for _, a := range pending {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := d.call(cctx, "worker.assessment", a)
		cancel()
		var remote RemoteError
		if err != nil && !errors.As(err, &remote) {
			return
		}
		if err != nil {
			d.Logf("worker %s: difficulty assessment refused: %v", a.SessionID, err)
		}
		if _, err := d.st.db.Exec(`DELETE FROM worker_assessment_outbox WHERE run_id=?`, a.RunID); err != nil {
			d.Logf("difficulty assessment outbox: %v", err)
			return
		}
	}
}
