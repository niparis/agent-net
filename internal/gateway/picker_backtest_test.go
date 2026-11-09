package gateway

import (
	"fmt"
	"github.com/niparis/agent-net/internal/protocol"
	"os"
	"sort"
	"strings"
	"testing"
)

func (s *Store) pickerTasksBeforeChains() ([]protocol.PickerTask, error) {
	snap, err := s.runsSnapshot()
	if err != nil {
		return nil, err
	}
	// The latest valid judge result of each session. The rows come oldest first.
	judged := map[string]float64{}
	rows, err := s.db.Query(`SELECT session_id, d FROM worker_assessments WHERE d > 0 ORDER BY started_at, assessed_at`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var session string
		var d float64
		if err := rows.Scan(&session, &d); err != nil {
			rows.Close()
			return nil, err
		}
		judged[session] = d
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}

	defects := map[string][2]int{}
	rows, err = s.db.Query(`SELECT session_id, COUNT(*), COALESCE(SUM(severity), 0) FROM worker_defects GROUP BY session_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var session string
		var count, severity int
		if err := rows.Scan(&session, &count, &severity); err != nil {
			rows.Close()
			return nil, err
		}
		defects[session] = [2]int{count, severity}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}

	// A failed accept, review or test step counts against the worker whose
	// session opened the change.
	failed := map[string]int{}
	rows, err = s.db.Query(`SELECT json_extract(c.origin, '$.session_id'), COUNT(*)
		FROM delivery_evidence e JOIN delivery_changes c ON c.id = e.change_id
		WHERE e.result = 'fail' AND e.step IN ('accept', 'review', 'test')
		GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var session *string
		var count int
		if err := rows.Scan(&session, &count); err != nil {
			rows.Close()
			return nil, err
		}
		if session != nil {
			failed[*session] = count
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}

	counts := func(outcome string) bool {
		return outcome != protocol.OutcomeStopped && outcome != protocol.OutcomeInfra
	}
	// The last run of each session that counts. The rows come oldest first.
	last := map[string]int{}
	for i, r := range snap.rows {
		if r.session != "" && counts(r.outcome) {
			last[r.session] = i
		}
	}
	out := make([]protocol.PickerTask, 0, len(snap.rows))
	for i, r := range snap.rows {
		d := judged[r.session]
		key := protocol.PickerModelKey(r.harness, r.model)
		if r.session == "" || d <= 0 || key == "" || !counts(r.outcome) {
			continue
		}
		facts := protocol.PickerTaskFacts{Complete: r.outcome == protocol.OutcomeComplete, FixRounds: r.fix}
		if last[r.session] == i {
			facts.FailedSteps = failed[r.session]
			facts.Defects, facts.DefectSeverity = defects[r.session][0], defects[r.session][1]
		}
		out = append(out, protocol.PickerTask{Key: key, D: d, Score: protocol.PickerTaskScore(facts)})
	}
	return out, nil
}

// backtestLevel is the judge label of a D, by the same cut points as
// protocol.LevelDRange. A column of the table has no label of its own, so the
// column backtest uses the label of the middle of the bucket.
func backtestLevel(d float64) string {
	switch {
	case d < 1.5:
		return "trivial"
	case d < 2.25:
		return "simple"
	case d < 3:
		return "normal"
	case d < 3.75:
		return "hard"
	}
	return "very_complex"
}

// backtestShares runs the real picker 1000 times from a fresh rotation, with
// no quota, and returns the share of each profile and the first pick's note.
func backtestShares(t *testing.T, candidates []protocol.WorkerModelCandidate, level string, d float64, table *protocol.PickerTable, scores []protocol.ModelScore, minimums map[string]float64) (string, string) {
	t.Helper()
	in := &protocol.PickerInput{D: d, Table: table}
	rotation := &protocol.WorkerRotation{}
	shares := map[string]int{}
	note := ""
	const picks = 1000
	for k := 0; k < picks; k++ {
		choice, err := protocol.ChooseWorkerModel(candidates, level, scores, minimums, nil, nil, rotation, in)
		if err != nil {
			t.Fatal(err)
		}
		if k == 0 {
			note = choice.PickNote
		}
		shares[choice.Name]++
	}
	var names []string
	for name := range shares {
		names = append(names, name)
	}
	sort.Slice(names, func(a, b int) bool {
		if shares[names[a]] != shares[names[b]] {
			return shares[names[a]] > shares[names[b]]
		}
		return names[a] < names[b]
	})
	var out []string
	for _, name := range names {
		out = append(out, fmt.Sprintf("%s=%.1f", name, 100*float64(shares[name])/picks))
	}
	return strings.Join(out, ";"), note
}

// TestPickerBacktest reads only the explicitly supplied copied database. It
// prints TASK lines for the last 20 tasks and COLUMN lines for each D column,
// both from the real picker with no quota, so the same test can run on two
// versions of the picker and the output can be compared.
func TestPickerBacktest(t *testing.T) {
	path := os.Getenv("AGENT_NET_PICKER_BACKTEST_DB")
	if path == "" {
		t.Skip("set AGENT_NET_PICKER_BACKTEST_DB to a copy")
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	after := s.pickerTable()
	profiles, err := s.WorkerProfiles()
	if err != nil {
		t.Fatal(err)
	}
	scores, err := s.scores()
	if err != nil {
		t.Fatal(err)
	}
	minimums, err := s.difficultyMinimums()
	if err != nil {
		t.Fatal(err)
	}
	var candidates []protocol.WorkerModelCandidate
	for _, p := range profiles {
		candidates = append(candidates, protocol.WorkerModelCandidate{Name: p.Name, Harness: p.Harness, Model: p.Selection().LegacyModel(), DefaultEffort: p.Effort})
	}
	var names []string
	for _, c := range candidates {
		names = append(names, c.Name)
	}
	fmt.Println("PROFILES " + strings.Join(names, ";"))
	// The last 20 tasks: distinct sessions by the end of their last run. D is
	// the first valid judge result, the one the table scores the task under.
	rows, err := s.db.Query(`SELECT r.session_id, r.model, r.difficulty, MAX(r.ended_at) e,
		COALESCE((SELECT a.d FROM worker_assessments a WHERE a.session_id=r.session_id AND a.d>0 ORDER BY a.started_at,a.assessed_at LIMIT 1),0)
		FROM worker_runs r WHERE r.session_id<>'' GROUP BY r.session_id ORDER BY e DESC LIMIT 20`)
	if err != nil {
		t.Fatal(err)
	}
	type run struct {
		session, model, level string
		ended                 int64
		d                     float64
	}
	var runs []run
	for rows.Next() {
		var r run
		if err := rows.Scan(&r.session, &r.model, &r.level, &r.ended, &r.d); err != nil {
			t.Fatal(err)
		}
		runs = append(runs, r)
	}
	rows.Close()
	for i, r := range runs {
		if r.d <= 0 {
			fmt.Printf("TASK\t%d\tno-assessment\t%s\t%s\t\t\n", i+1, r.level, r.model)
			continue
		}
		level := r.level
		if level == "" {
			level = "normal"
		}
		share, note := backtestShares(t, candidates, level, r.d, after, scores, minimums)
		fmt.Printf("TASK\t%d\t%.2f\t%s\t%s\t%s\t%s\n", i+1, r.d, level, r.model, share, note)
	}
	for bucket := 0; bucket <= 11; bucket++ {
		lo, hi := after.BucketRange(bucket)
		mid := (lo + hi) / 2
		level := backtestLevel(mid)
		share, note := backtestShares(t, candidates, level, mid, after, scores, minimums)
		fmt.Printf("COLUMN\t%.2f\t%s\t%s\t%s\n", lo, level, share, note)
	}
}
