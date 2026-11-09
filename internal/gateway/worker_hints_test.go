package gateway

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestLooksLikeFixTask(t *testing.T) {
	yes := []string{
		"Fix the login bug",
		"fixes the feed sync",
		"Investigate the regression in delivery",
		"the page is broken",
		"two defects in the parser",
		"BUG: quota display",
	}
	for _, task := range yes {
		if !looksLikeFixTask(task) {
			t.Errorf("%q should look like a fix", task)
		}
	}
	no := []string{
		"Add a new settings page",
		"prefix the names",
		"document the delivery steps",
		"review the parser",
	}
	for _, task := range no {
		if looksLikeFixTask(task) {
			t.Errorf("%q should not look like a fix", task)
		}
	}
}

func TestWorkerStartHintNamesRecentChanges(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	other := ratingWorker(t, s, node, caller, "m", "normal")
	insertMergedChange(t, s, "ch_recent", "shop", other, "Fix the feed parser", c.now().Add(-24*time.Hour).Unix(), "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0")

	hint := s.WorkerStartHint("shop", "Fix the quota display", "", "")
	for _, want := range []string{"If this fixes earlier work", "fixes=<change id>", "Recent changes in shop:", "ch_recent", "Fix the feed parser", " by "} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint %q does not contain %q", hint, want)
		}
	}

	if got := s.WorkerStartHint("shop", "Fix the quota display", "", ""); got == "" {
		t.Fatal("the same start should get a hint again")
	}
	if got := s.WorkerStartHint("shop", "Fix the quota display", "", "ch_recent"); got != "" {
		t.Errorf("a start with fixes should get no hint: %q", got)
	}
	if got := s.WorkerStartHint("shop", "Add a settings page", "", ""); got != "" {
		t.Errorf("a task that does not sound like a fix should get no hint: %q", got)
	}
	if got := s.WorkerStartHint("other", "Fix the quota display", "", ""); got != "" {
		t.Errorf("another project should get no hint: %q", got)
	}
}

func TestWorkerStartHintLeavesOutOldChanges(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	other := ratingWorker(t, s, node, caller, "m", "normal")
	insertMergedChange(t, s, "ch_old", "shop", other, "Old work", c.now().Add(-8*24*time.Hour).Unix(), "b1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0")

	if got := s.WorkerStartHint("shop", "Fix the parser", "", ""); got != "" {
		t.Errorf("a change merged eight days ago should not be recent: %q", got)
	}
}

func TestBriefDefectChargedToCallerNotWorker(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	worker := ratingRun(t, s, node, caller, "p/m:high", "hard")
	before := s.QualityAnalysis()

	out, err := s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{
		Worker: worker, Kind: protocol.RateKindBriefDefect, Severity: 2, Note: "the brief named the wrong branch",
	}, Rater: caller})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Accepted || out.Kind != protocol.RateKindBriefDefect || out.Severity != 2 {
		t.Fatalf("brief defect result: %+v", out)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM worker_brief_defects WHERE worker_session_id=? AND caller=? AND severity=2`, worker, caller).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("brief defects stored %d times, want 1", n)
	}
	var wantRun, gotRun int64
	if err := s.db.QueryRow(`SELECT id FROM worker_runs WHERE session_id=? ORDER BY id DESC LIMIT 1`, worker).Scan(&wantRun); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT run_id FROM worker_brief_defects WHERE worker_session_id=?`, worker).Scan(&gotRun); err != nil {
		t.Fatal(err)
	}
	if gotRun != wantRun {
		t.Fatalf("brief defect run = %d, want the worker run %d", gotRun, wantRun)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM worker_defects WHERE session_id=?`, worker).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("the brief defect also counted against the worker: %d worker defects", n)
	}
	if after := s.QualityAnalysis(); !reflect.DeepEqual(before, after) {
		t.Fatalf("the picker quality changed after a brief defect:\nbefore %+v\nafter  %+v", before, after)
	}
	cal, err := s.Calibration()
	if err != nil {
		t.Fatal(err)
	}
	if cal.Defects != 0 {
		t.Fatalf("calibration defects = %d, want 0", cal.Defects)
	}
	found := false
	for _, c := range cal.Callers {
		if c.Caller == caller {
			found = true
			if c.BriefDefects != 1 {
				t.Fatalf("caller brief defects = %d, want 1", c.BriefDefects)
			}
		}
	}
	if !found {
		t.Fatalf("the caller is not in the calibration: %+v", cal.Callers)
	}
}

func TestRecentWorkerChangesExcludesTheWorker(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	me := ratingWorker(t, s, node, caller, "m", "normal")
	other := ratingWorker(t, s, node, caller, "m", "normal")
	insertMergedChange(t, s, "ch_own", "shop", me, "My earlier work", c.now().Add(-time.Hour).Unix(), "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0")
	insertMergedChange(t, s, "ch_other", "shop", other, "Another worker", c.now().Add(-2*time.Hour).Unix(), "b1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0")
	changes, err := s.RecentWorkerChanges("shop", me, "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].ChangeID != "ch_other" || changes[0].WorkerSession != other {
		t.Fatalf("recent changes = %+v, want only ch_other by the other worker", changes)
	}
	if changes, err := s.RecentWorkerChanges("shop", me, "ch_other", 5); err != nil || len(changes) != 0 {
		t.Fatalf("excluding its own change: %+v, %v", changes, err)
	}
}

// A team lead may file a brief defect about a member's brief. It is charged to
// the member, the briefing session, not to the lead or the worker. A peer
// outside the tree is refused.
func TestBriefDefectFromLeadChargedToMember(t *testing.T) {
	r := newPlaceRig(t)
	var node string
	if err := r.s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, r.member).Scan(&node); err != nil {
		t.Fatal(err)
	}
	worker := ratingRun(t, r.s, node, r.member, "p/m", "hard")
	out, err := r.s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{
		Worker: worker, Kind: protocol.RateKindBriefDefect, Severity: 2, Note: "the member's brief named the wrong branch",
	}, Rater: r.lead})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Accepted {
		t.Fatalf("a team lead was refused: %+v", out)
	}
	var caller string
	if err := r.s.db.QueryRow(`SELECT caller FROM worker_brief_defects WHERE worker_session_id=?`, worker).Scan(&caller); err != nil {
		t.Fatal(err)
	}
	if caller != r.member {
		t.Fatalf("brief defect charged to %q, want the briefing session %q", caller, r.member)
	}
	if out, err := r.s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{
		Worker: worker, Kind: protocol.RateKindBriefDefect, Severity: 2, Note: "a peer's guess",
	}, Rater: r.outside}); err != nil || out.Accepted {
		t.Fatalf("a peer outside the tree filed a brief defect: %+v, %v", out, err)
	}
}

// insertMergedChange stores one merged change of worker in project.
func insertMergedChange(t *testing.T, s *Store, id, project, worker, title string, mergedAt int64, commit string) {
	t.Helper()
	origin := `{"kind":"worker","worker_id":"w_test","session_id":"` + worker + `"}`
	if _, err := s.db.Exec(`INSERT INTO delivery_changes(id,project,branch,head_commit,revision,origin,title,state,merged_commit,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, project, "agent-net/w1", commit, 1, origin, title, protocol.ChangeMerged, commit, mergedAt, mergedAt); err != nil {
		t.Fatal(err)
	}
}
