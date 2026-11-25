package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// pickQuotaMigration keeps what each recorded pick read of the quota windows
// (decision 0191): one row per pick, keyed by the same run_id as its
// pick_candidates rows. read is the protocol.PickQuota JSON written with the
// rows; run_end is the protocol.PickRunEnd JSON written when the chosen run
// ends. Rows are kept as long as the pick's rows: retention prunes neither.
// Appended at the end of the migration list and never edited once it ships.
const pickQuotaMigration = `
CREATE TABLE IF NOT EXISTS pick_quota (
 run_id TEXT PRIMARY KEY,
 read TEXT NOT NULL,
 run_end TEXT NOT NULL DEFAULT '');
`

// insertPickQuota stores what a pick read, in the transaction that stores its
// rows. A pick without rows has nothing to explain, and a repeat write of the
// same pick is a no-op.
func insertPickQuota(tx *sql.Tx, runID string, rows []protocol.PickCandidate, q *protocol.PickQuota) error {
	if q == nil || len(rows) == 0 || runID == "" {
		return nil
	}
	raw, err := json.Marshal(q)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT OR IGNORE INTO pick_quota(run_id, read) VALUES (?, ?)`, runID, string(raw))
	return err
}

// recordPickRunEnd adds the end of a run to the pick that chose its model: the
// run's tokens by kind, as the run row holds them, and its provider's windows
// as read now. The pick is the session's latest one that has no end yet, so a
// later run of the same session (a resume) does not overwrite the first.
func (g *Gateway) recordPickRunEnd(nodeID string, run protocol.WorkerRun) {
	if run.SessionID == "" {
		return
	}
	end := protocol.PickRunEnd{EndedAt: run.EndedAt, Provider: protocol.QuotaProviderForStart(run.Harness, run.Model)}
	if end.Provider != "" {
		if p, ok := g.ownerQuotas(nodeID).Provider(end.Provider); ok {
			after := protocol.PickQuotaProviderOf(p)
			end.After = &after
		}
	}
	if err := g.store.recordPickRunEnd(run.SessionID, end); err != nil {
		g.logLine("error", "where", "pick run end", "session", run.SessionID, "err", err)
	}
}

func (s *Store) recordPickRunEnd(sessionID string, end protocol.PickRunEnd) error {
	var runID string
	err := s.db.QueryRow(`SELECT pq.run_id FROM pick_quota pq JOIN worker_assessments wa ON wa.run_id=pq.run_id
		WHERE wa.session_id=? AND pq.run_end='' ORDER BY wa.started_at DESC LIMIT 1`, sessionID).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.db.QueryRow(`SELECT usage_in, usage_out, usage_reasoning, usage_cache_read, usage_cache_write FROM worker_runs
		WHERE session_id=? ORDER BY id DESC LIMIT 1`, sessionID).Scan(&end.Tokens.In, &end.Tokens.Out, &end.Tokens.Reasoning, &end.Tokens.CacheRead, &end.Tokens.CacheWrite); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	raw, err := json.Marshal(end)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE pick_quota SET run_end=? WHERE run_id=? AND run_end=''`, string(raw), runID)
	return err
}

// PickQuotaViews reads, for each worker session, what its latest recorded pick
// read and what its run used, in plain words. Sessions without a recorded
// pick are left out. One statement for the read, one for the rows' names.
func (s *Store) PickQuotaViews(sessions []string) (map[string]protocol.PickQuotaView, error) {
	out := map[string]protocol.PickQuotaView{}
	if len(sessions) == 0 {
		return out, nil
	}
	args := make([]any, len(sessions))
	for i, id := range sessions {
		args[i] = id
	}
	in := `(?` + strings.Repeat(",?", len(sessions)-1) + `)`
	rows, err := s.db.Query(`SELECT wa.session_id, pq.run_id, pq.read, pq.run_end FROM pick_quota pq
		JOIN worker_assessments wa ON wa.run_id=pq.run_id
		WHERE wa.session_id IN `+in+` ORDER BY wa.session_id, wa.started_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	type found struct {
		session, runID string
		read           protocol.PickQuota
		end            *protocol.PickRunEnd
	}
	var picks []found
	seen := map[string]bool{}
	for rows.Next() {
		var f found
		var read, runEnd string
		if err := rows.Scan(&f.session, &f.runID, &read, &runEnd); err != nil {
			rows.Close()
			return nil, err
		}
		if seen[f.session] {
			continue
		}
		seen[f.session] = true
		if err := json.Unmarshal([]byte(read), &f.read); err != nil {
			continue
		}
		if runEnd != "" {
			var e protocol.PickRunEnd
			if json.Unmarshal([]byte(runEnd), &e) == nil {
				f.end = &e
			}
		}
		picks = append(picks, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, f := range picks {
		pickRows, err := s.pickRowsForQuota(f.runID)
		if err != nil {
			return nil, err
		}
		read := f.read
		out[f.session] = protocol.PickQuotaView{RunID: f.runID, Lines: protocol.PickQuotaLines(&read, f.end, pickRows), Read: &read, RunEnd: f.end}
	}
	return out, nil
}

// pickRowsForQuota reads the row fields the plain words name: the name, the
// pace room and factor, and the gate's or the ranker's reason, by position.
func (s *Store) pickRowsForQuota(runID string) ([]protocol.PickCandidate, error) {
	rows, err := s.db.Query(`SELECT pos, name, pace_room, pace_factor, skipped_reason, left_out_reason FROM pick_candidates WHERE run_id=? ORDER BY pos`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.PickCandidate
	for rows.Next() {
		var pos int
		var r protocol.PickCandidate
		var room sql.NullFloat64
		if err := rows.Scan(&pos, &r.Name, &room, &r.PaceFactor, &r.SkippedReason, &r.LeftOutReason); err != nil {
			return nil, err
		}
		if room.Valid {
			v := room.Float64
			r.PaceRoom = &v
		}
		for len(out) < pos {
			out = append(out, protocol.PickCandidate{})
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
