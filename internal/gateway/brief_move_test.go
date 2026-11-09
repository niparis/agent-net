package gateway

import (
	"reflect"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A caller that filed defect_found_later on its own worker by mistake can move
// that entry to a brief_defect. The worker stops being charged and both rows
// stay in the history.
func TestMoveDefectToBriefDefect(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	wid := ratingRun(t, s, node, caller, "p/m", "hard")
	before := s.QualityAnalysis()

	frame := defectFrame(wid, caller, "the brief named the wrong branch")
	frame.Severity = 2
	filed, err := s.AddRating(frame)
	if err != nil {
		t.Fatal(err)
	}
	if !filed.Accepted || filed.Defect == 0 {
		t.Fatalf("defect was not filed with an id: %+v", filed)
	}
	if tasks, median := defectTaskCalibration(t, s, caller); tasks != 1 || median != 2 {
		t.Fatalf("before the move: tasks %d median %v, want 1 and 2", tasks, median)
	}

	out, err := s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{
		Worker: wid, Kind: protocol.RateKindMoveToBriefDefect, Defect: filed.Defect,
	}, Rater: caller})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Accepted || out.Kind != protocol.RateKindMoveToBriefDefect || out.Defect != filed.Defect || out.BriefDefect == 0 {
		t.Fatalf("move result: %+v", out)
	}

	var movedAt int64
	var movedBy string
	var briefID int64
	var severity int
	var note string
	if err := s.db.QueryRow(`SELECT moved_at, moved_by, brief_defect_id, severity, note FROM worker_defects WHERE id = ?`,
		filed.Defect).Scan(&movedAt, &movedBy, &briefID, &severity, &note); err != nil {
		t.Fatal(err)
	}
	if movedAt == 0 || movedBy != caller || briefID != out.BriefDefect {
		t.Fatalf("the original row does not record the move: moved_at %d moved_by %q brief_defect_id %d", movedAt, movedBy, briefID)
	}
	if severity != 2 || note != "the brief named the wrong branch" {
		t.Fatalf("the original row was changed: severity %d note %q", severity, note)
	}
	var chargedTo, rater, briefNote string
	var briefSeverity int
	if err := s.db.QueryRow(`SELECT caller, rater, severity, note FROM worker_brief_defects WHERE id = ?`,
		out.BriefDefect).Scan(&chargedTo, &rater, &briefSeverity, &briefNote); err != nil {
		t.Fatal(err)
	}
	if chargedTo != caller || rater != caller || briefSeverity != 2 || briefNote != note {
		t.Fatalf("brief defect row: caller %q rater %q severity %d note %q", chargedTo, rater, briefSeverity, briefNote)
	}

	if after := s.QualityAnalysis(); !reflect.DeepEqual(before, after) {
		t.Fatalf("the worker is still charged after the move:\nbefore %+v\nafter  %+v", before, after)
	}
	cal, err := s.Calibration()
	if err != nil {
		t.Fatal(err)
	}
	if cal.Defects != 0 {
		t.Fatalf("calibration defects = %d after the move, want 0", cal.Defects)
	}
	for _, c := range cal.Callers {
		if c.Caller == caller && c.BriefDefects != 1 {
			t.Fatalf("caller brief defects = %d, want 1", c.BriefDefects)
		}
	}
	if tasks, median := defectTaskCalibration(t, s, caller); tasks != 1 || median != 0 {
		t.Fatalf("after the move: tasks %d median %v, want 1 and 0", tasks, median)
	}
	counts, err := s.RatingCounts()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range counts {
		if c.Model == "p/m" && c.Defects != 0 {
			t.Fatalf("model defect count after the move: %+v", c)
		}
	}
}

// Only the session that filed the defect may move it. A peer gets a clear
// refusal and nothing changes.
func TestMoveDefectOnlyFilerMayMove(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	other := ratingCaller(t, s, node, "other", "gpt-6.1-sol")
	wid := ratingRun(t, s, node, caller, "p/m", "hard")
	filed, err := s.AddRating(defectFrame(wid, caller, "not the worker's fault"))
	if err != nil || !filed.Accepted {
		t.Fatalf("defect: %+v %v", filed, err)
	}

	out, err := s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{
		Worker: wid, Kind: protocol.RateKindMoveToBriefDefect, Defect: filed.Defect,
	}, Rater: other})
	if err != nil {
		t.Fatal(err)
	}
	if out.Accepted || !strings.Contains(out.Reason, "only the session that filed") {
		t.Fatalf("a peer moved another session's defect: %+v", out)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM worker_brief_defects WHERE worker_session_id = ?`, wid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("a refused move wrote %d brief defects", n)
	}
}

// Moving the same row twice is a no-op with a clear message.
func TestMoveDefectTwiceIsNoOp(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	wid := ratingRun(t, s, node, caller, "p/m", "hard")
	filed, err := s.AddRating(defectFrame(wid, caller, "the brief was wrong"))
	if err != nil || !filed.Accepted {
		t.Fatalf("defect: %+v %v", filed, err)
	}
	move := protocol.RateFrame{RateRequest: protocol.RateRequest{
		Worker: wid, Kind: protocol.RateKindMoveToBriefDefect, Defect: filed.Defect,
	}, Rater: caller}
	first, err := s.AddRating(move)
	if err != nil || !first.Accepted {
		t.Fatalf("first move: %+v %v", first, err)
	}
	second, err := s.AddRating(move)
	if err != nil {
		t.Fatal(err)
	}
	if second.Accepted || !strings.Contains(second.Reason, "already moved") || second.BriefDefect != first.BriefDefect {
		t.Fatalf("second move: %+v", second)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM worker_brief_defects WHERE worker_session_id = ?`, wid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("brief defects after a second move = %d, want 1", n)
	}
}

// A defect agent-net recorded by itself has no filer session, so nobody may
// move it.
func TestMoveDefectRefusesAutomaticDefect(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	wid := ratingRun(t, s, node, caller, "p/m", "hard")
	var runID int64
	if err := s.db.QueryRow(`SELECT id FROM worker_runs WHERE session_id = ? ORDER BY id DESC LIMIT 1`, wid).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	res, err := s.db.Exec(`INSERT INTO worker_defects (run_id, session_id, caller, severity, note, at) VALUES (?,?,?,?,?,?)`,
		runID, wid, caller, 1, "test failed", s.now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO worker_defect_auto (defect_id, change_id, kind, revision, at) VALUES (?,?,?,?,?)`,
		id, "ch_auto", "test", 1, s.now().Unix()); err != nil {
		t.Fatal(err)
	}

	out, err := s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{
		Worker: wid, Kind: protocol.RateKindMoveToBriefDefect, Defect: id,
	}, Rater: caller})
	if err != nil {
		t.Fatal(err)
	}
	if out.Accepted || !strings.Contains(out.Reason, "recorded this defect itself") {
		t.Fatalf("an automatic defect was moved: %+v", out)
	}
}

// The brief defect is charged to the session that wrote the worker's brief, not
// to the owner who filed and moved the defect.
func TestMoveDefectChargesTheBriefingCaller(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness,
		NativeID: protocol.OwnerNativeID, Name: "you (on mac)"})
	if err != nil {
		t.Fatal(err)
	}
	wid := ratingRun(t, s, node, caller, "p/m", "hard")
	filed, err := s.AddRating(defectFrame(wid, owner, "the brief named the wrong branch"))
	if err != nil || !filed.Accepted {
		t.Fatalf("owner defect: %+v %v", filed, err)
	}

	out, err := s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{
		Worker: wid, Kind: protocol.RateKindMoveToBriefDefect, Defect: filed.Defect,
	}, Rater: owner})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Accepted {
		t.Fatalf("owner move refused: %+v", out)
	}
	var chargedTo, rater string
	if err := s.db.QueryRow(`SELECT caller, rater FROM worker_brief_defects WHERE id = ?`, out.BriefDefect).Scan(&chargedTo, &rater); err != nil {
		t.Fatal(err)
	}
	if chargedTo != caller || rater != owner {
		t.Fatalf("brief defect charged to %q by %q, want the briefing caller %q by the owner %q", chargedTo, rater, caller, owner)
	}
}
