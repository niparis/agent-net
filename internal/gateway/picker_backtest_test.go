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

// TestPickerBacktest reads only the explicitly supplied copied database.
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
	beforeTasks, err := s.pickerTasksBeforeChains()
	if err != nil {
		t.Fatal(err)
	}
	before := protocol.BuildPickerTable(beforeTasks, s.pickerTable().Settings())
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
	fmt.Println("SCORES profile bucket before(n) after(n)")
	for _, m := range []string{"gpt-6.1-sol:low", "gpt-6.1-sol:medium", "gpt-6.1-sol:high", "gpt-6.1-terra:low", "gpt-6.1-terra:medium", "gpt-6.1-terra:high", "gpt-6.1-terra:max"} {
		for bucket := 0; bucket <= 9; bucket++ {
			lo, _ := after.BucketRange(bucket)
			a, b := before.Cell(m, bucket), after.Cell(m, bucket)
			fmt.Printf("SCORE %s %.2f %.3f(%d) %.3f(%d)\n", m, lo, a.Score, a.N, b.Score, b.N)
		}
	}
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
	fmt.Println("RUN n D level actual | current best, allowed | B' first pick note | simulated share of 1000 picks (no quota)")
	for i, r := range runs {
		if r.d <= 0 {
			fmt.Printf("RUN %d no-assessment %s %s\n", i+1, r.level, r.model)
			continue
		}
		level := r.level
		if level == "" {
			level = "normal"
		}
		bucket := after.Bucket(r.d)
		lo, _ := after.BucketRange(bucket)
		oldFacts := make([]protocol.PickerCandidateFacts, len(candidates))
		for j, c := range candidates {
			score, _, ceil, ok := protocol.WorkerBenchmarkCeiling(c, scores, minimums)
			key := protocol.PickerModelKey(c.Harness, c.Model)
			oldFacts[j] = protocol.PickerCandidateFacts{Key: key, Cell: before.Cell(key, bucket), Scored: ok, Benchmark: score, Ceiling: ceil}
		}
		oldVerdicts, oldIdx := protocol.PickerBucketVerdicts(oldFacts, lo, before.BucketMean(bucket), bucket, before.Settings())
		oldBest, oldName := before.BucketMean(bucket), "mean"
		if oldIdx >= 0 {
			oldBest, oldName = oldFacts[oldIdx].Cell.Score, candidates[oldIdx].Name
		}
		var oldAllowed []string
		for j, v := range oldVerdicts {
			if v.Allowed {
				oldAllowed = append(oldAllowed, fmt.Sprintf("%s %.2f n%d", candidates[j].Name, oldFacts[j].Cell.Score, oldFacts[j].Cell.N))
			}
		}
		// The real picker, on the corrected table, many times from a fresh
		// rotation: the shares are what the weights and the explore share give.
		in := &protocol.PickerInput{D: r.d, Table: after}
		rotation := &protocol.WorkerRotation{}
		shares := map[string]int{}
		firstNote := ""
		const picks = 1000
		for k := 0; k < picks; k++ {
			choice, err := protocol.ChooseWorkerModel(candidates, level, scores, minimums, nil, nil, rotation, in)
			if err != nil {
				t.Fatal(err)
			}
			if k == 0 {
				firstNote = choice.PickNote
			}
			shares[choice.Name]++
		}
		var names []string
		for name := range shares {
			names = append(names, name)
		}
		sort.Slice(names, func(a, b int) bool { return shares[names[a]] > shares[names[b]] })
		var share []string
		for _, name := range names {
			share = append(share, fmt.Sprintf("%s %.1f%%", name, 100*float64(shares[name])/picks))
		}
		fmt.Printf("RUN %d %.2f %s %s | best=%s %.2f; allowed=%s | %s | %s\n", i+1, r.d, level, r.model, oldName, oldBest, strings.Join(oldAllowed, ", "), firstNote, strings.Join(share, ", "))
	}
}
