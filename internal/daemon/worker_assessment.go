package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/difficulty"
	"github.com/niparis/agent-net/internal/protocol"
)

// workerJudgeTimeout bounds the judge on the start path. A start must never
// wait longer than this for a level; the fallback is normal. Real judge runs
// took 9 to 29 s (2026-11-03, 40 starts) and 15% hit an earlier 30 s limit;
// the start call itself allows 2 minutes.
const workerJudgeTimeout = 60 * time.Second

// assessWorkerStart runs the judge on the brief before the gateway chooses a
// model. It always returns a result: a missing, failed, timed out or low
// confidence judge gives normal with the reason in Error. The caller's
// difficulty and touches are not read.
func (d *Daemon) assessWorkerStart(ctx context.Context, project, task string) *protocol.WorkerAssessment {
	started := time.Now()
	a := &protocol.WorkerAssessment{RunID: protocol.NewID("k"), StartedAt: started.UnixNano(), Label: "normal", PickerLabel: "normal"}
	if d.cfg.testNoDifficulty {
		a.Error = "the difficulty judge is off in this test daemon"
		a.AssessedAt = time.Now().Unix()
		return a
	}
	cfg := d.cfg
	cfg.DifficultyAnalyzer = false
	cfg.DifficultyTimeout = workerJudgeTimeout
	jctx, cancel := context.WithTimeout(ctx, workerJudgeTimeout)
	defer cancel()
	result, err := NewDifficultyAssessor(cfg, DifficultySources{Binary: d.harnessBinary, Logf: d.safeLogf}).Assess(jctx, project, task)
	harness, model, _ := strings.Cut(result.JudgeModel, ":")
	a.Reasoning, a.Scope, a.Coupling = result.Scores.Reasoning, result.Scores.Scope, result.Scores.Coupling
	a.Uncertainty, a.Verification, a.Risk = result.Scores.Uncertainty, result.Scores.Verification, result.Scores.Risk
	a.D, a.Label, a.Bottleneck = result.D, result.Label, result.Bottleneck
	a.Confidence, a.RulesVersion = result.Confidence, difficulty.RulesVersion
	a.JudgeHarness, a.JudgeModel, a.Reasons = harness, model, result.Scores.Reasons
	if result.Notes != "" && result.Scores.Reasoning != 0 {
		if a.Reasons == nil {
			a.Reasons = make(map[string]string)
		}
		a.Reasons["judge_note"] = result.Notes
	}
	if err != nil || result.Scores.Reasoning == 0 || result.Label == "" {
		a.Label, a.PickerLabel = "normal", "normal"
		a.Error = assessmentFailure(result, err)
	} else if a.Confidence == "low" {
		// The scores and label are kept for the record; the picker uses normal.
		a.PickerLabel = "normal"
	} else {
		a.PickerLabel = a.Label
	}
	a.AssessedAt = time.Now().Unix()
	return a
}

// judgeDir picks the folder the judge sees. The judge-only path does not read
// the repository, so a caller that does not hold the project (a remote start
// or a CTO start from another machine) still gets a result from the brief.
func (d *Daemon) judgeDir(callerPath, project string) string {
	for _, dir := range []string{callerPath, project} {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return dir
		}
	}
	return d.cfg.StateDir
}

// assessmentFailure turns the assessor's fallback note into the short reason
// the start note and the stored row show.
func assessmentFailure(result difficulty.Assessment, err error) string {
	note := strings.TrimSuffix(strings.TrimPrefix(result.Notes, "difficulty: "), "; started at normal")
	if note == "" && err != nil {
		note = err.Error()
	}
	if note == "" {
		note = "judge returned no scores"
	}
	return note
}

// recordWorkerAssessment stores a judge result made before the start, under
// the new session. The worker record then shows the same scores as the start
// note. A result that arrives later (a resumed run) uses the same path.
func (d *Daemon) recordWorkerAssessment(w *worker, a *protocol.WorkerAssessment) {
	if a == nil {
		return
	}
	d.wmu.Lock()
	sid := w.sessionID
	d.wmu.Unlock()
	if sid == "" {
		return
	}
	copied := *a
	copied.SessionID = sid
	if copied.AssessedAt == 0 {
		copied.AssessedAt = time.Now().Unix()
	}
	d.goBackground(func() {
		if err := d.st.enqueueWorkerAssessment(copied); err != nil {
			d.Logf("worker %s: could not queue difficulty assessment: %v", sid, err)
			return
		}
		ctx, cancel := context.WithTimeout(d.ctoCtx, 10*time.Second)
		defer cancel()
		d.flushWorkerAssessments(ctx)
	})
}

// startWorkerAssessment assesses a run that begins without a start request,
// such as a resumed session. It never waits for the judge, gateway or notice.
func (d *Daemon) startWorkerAssessment(w *worker, project, task string) {
	if d.cfg.testNoDifficulty {
		return
	}
	d.wmu.Lock()
	sid := w.sessionID
	d.wmu.Unlock()
	d.goBackground(func() {
		a := d.assessWorkerStart(d.ctoCtx, project, task)
		a.SessionID = sid
		if err := d.st.enqueueWorkerAssessment(*a); err != nil {
			d.Logf("worker %s: could not queue difficulty assessment: %v", sid, err)
			return
		}
		ctx, cancel := context.WithTimeout(d.ctoCtx, 10*time.Second)
		defer cancel()
		d.flushWorkerAssessments(ctx)
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
