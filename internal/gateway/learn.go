package gateway

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Findings and the automatic playbook (decision 0022). A finding is quiet: it
// is stored, and it may promote a playbook note. It is never a message and it
// never reaches Needs you.

// AddFinding stores one finding and lets the gateway promote a note from it.
// A session records at most FindingsPerSessionDay findings in 24 hours.
func (s *Store) AddFinding(f protocol.Finding) (protocol.LearnResult, error) {
	clean, err := protocol.CleanFinding(f.SubjectKind, f.Subject, f.Finding, f.Evidence)
	if err != nil {
		return protocol.LearnResult{}, reject("%v", err)
	}
	if strings.TrimSpace(f.Reporter) == "" {
		return protocol.LearnResult{}, reject("a finding needs the session that reports it")
	}
	now := s.now()
	var today int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM findings WHERE reporter = ? AND at >= ?`,
		f.Reporter, now.Add(-24*time.Hour).Unix()).Scan(&today); err != nil {
		return protocol.LearnResult{}, err
	}
	if today >= protocol.FindingsPerSessionDay {
		return protocol.LearnResult{Accepted: false, Remaining: 0,
			Reason: fmt.Sprintf("at most %d findings per session per day", protocol.FindingsPerSessionDay)}, nil
	}
	evidence, _ := json.Marshal(clean.Evidence)
	res, err := s.db.Exec(`INSERT INTO findings (subject_kind, subject, finding, evidence, reporter, node_id, at, status)
		VALUES (?,?,?,?,?,?,?,?)`, clean.SubjectKind, clean.Subject, clean.Finding, string(evidence),
		f.Reporter, f.NodeID, now.Unix(), protocol.FindingNew)
	if err != nil {
		return protocol.LearnResult{}, err
	}
	id, _ := res.LastInsertId()
	_, _, harness := noteKey(clean.SubjectKind, clean.Subject)
	if err := s.evaluatePlaybook(clean.SubjectKind, clean.Subject, harness); err != nil {
		return protocol.LearnResult{}, err
	}
	var status string
	if err := s.db.QueryRow(`SELECT status FROM findings WHERE id = ?`, id).Scan(&status); err != nil {
		return protocol.LearnResult{}, err
	}
	return protocol.LearnResult{Accepted: true, ID: id, Status: status,
		Remaining: protocol.FindingsPerSessionDay - today - 1}, nil
}

// Findings lists the findings of one subject, newest first. An empty subject
// lists every finding.
func (s *Store) Findings(kind, subject string, limit int) ([]protocol.Finding, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	query := `SELECT id, subject_kind, subject, finding, evidence, reporter, node_id, at, status FROM findings`
	var args []any
	var where []string
	if kind != "" {
		where = append(where, "subject_kind = ?")
		args = append(args, kind)
	}
	if subject != "" {
		where = append(where, "subject = ?")
		args = append(args, subject)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY at DESC, id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.Finding{}
	for rows.Next() {
		var f protocol.Finding
		var evidence string
		if err := rows.Scan(&f.ID, &f.SubjectKind, &f.Subject, &f.Finding, &evidence,
			&f.Reporter, &f.NodeID, &f.At, &f.Status); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(evidence), &f.Evidence)
		out = append(out, f)
	}
	return out, rows.Err()
}

// noteKey turns a finding subject into its playbook key.
func noteKey(kind, subject string) (keyKind, keySubject, harness string) {
	if kind == protocol.SubjectModelHarness {
		model, h, ok := protocol.ParseModelHarness(subject)
		if !ok {
			return kind, subject, ""
		}
		return kind, model, h
	}
	return kind, subject, ""
}

// evaluatePlaybook promotes a note when the findings on a subject have enough
// support, or when its track record is clearly bad. A note written by the
// owner, or pinned, is never changed.
func (s *Store) evaluatePlaybook(kind, subject, harness string) error {
	if strings.TrimSpace(subject) == "" {
		return nil
	}
	keyKind, keySubject, keyHarness := noteKey(kind, subject)
	if keySubject == "" {
		return nil
	}
	now := s.now()
	cut := now.Add(-protocol.FindingWindowDays * 24 * time.Hour).Unix()
	rows, err := s.db.Query(`SELECT reporter, finding FROM findings
		WHERE subject_kind = ? AND subject = ? AND at >= ? AND status IN (?, ?)
		ORDER BY at DESC, id DESC`, kind, subject, cut, protocol.FindingNew, protocol.FindingPromoted)
	if err != nil {
		return err
	}
	count := 0
	reporters := map[string]bool{}
	latest := ""
	for rows.Next() {
		var reporter, text string
		if err := rows.Scan(&reporter, &text); err != nil {
			rows.Close()
			return err
		}
		if count == 0 {
			latest = text
		}
		count++
		reporters[reporter] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	enough := count >= 3 && len(reporters) >= 2
	var track *protocol.TrackRecord
	trackSubject := ""
	switch keyKind {
	case protocol.SubjectModel:
		trackSubject = keySubject
	case protocol.SubjectHarness:
		trackSubject = keySubject
	case protocol.SubjectModelHarness:
		trackSubject = keySubject + "@" + keyHarness
	}
	if trackSubject != "" {
		if t, ok, err := s.TrackSubject(keyKind, trackSubject, protocol.TrackRecordDays*24*time.Hour); err != nil {
			return err
		} else if ok {
			track = t
		}
	}
	bad := track != nil && track.Runs >= 4 && track.Complete*2 < track.Runs
	if !enough && !bad {
		return nil
	}
	var source string
	var pinned int
	var createdAt int64
	err = s.db.QueryRow(`SELECT source, pinned, created_at FROM playbook WHERE subject_kind=? AND subject=? AND harness=?`,
		keyKind, keySubject, keyHarness).Scan(&source, &pinned, &createdAt)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil && (pinned == 1 || source != protocol.PlaybookSourceAuto) {
		return nil
	}
	text := latest
	if text == "" {
		text = "The track record is poor."
	}
	text += fmt.Sprintf("\n%d findings from %d sessions", count, len(reporters))
	if track != nil {
		text += "\n" + track.Line()
	}
	text = protocol.CleanPlaybookText(text, protocol.PlaybookAutoMaxBytes)
	if createdAt == 0 {
		createdAt = now.Unix()
	}
	if _, err := s.db.Exec(`INSERT INTO playbook (subject_kind, subject, harness, note, source, support, track_line, pinned, stale, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,0,0,?,?)
		ON CONFLICT (subject_kind, subject, harness) DO UPDATE SET note=excluded.note, source='auto',
		support=excluded.support, track_line=excluded.track_line, stale=0, updated_at=excluded.updated_at`,
		keyKind, keySubject, keyHarness, text, protocol.PlaybookSourceAuto, count, trackLine(track),
		createdAt, now.Unix()); err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE findings SET status = ? WHERE subject_kind = ? AND subject = ? AND status = ?`,
		protocol.FindingPromoted, kind, subject, protocol.FindingNew)
	return err
}

func trackLine(t *protocol.TrackRecord) string {
	if t == nil {
		return ""
	}
	return t.Line()
}
