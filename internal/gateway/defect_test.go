package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// defectFrame is one late defect report from rater.
func defectFrame(worker, rater, note string) protocol.RateFrame {
	return protocol.RateFrame{
		RateRequest: protocol.RateRequest{Worker: worker, Kind: protocol.RateKindDefectLater, Severity: 1, Note: note},
		Rater:       rater,
	}
}

// defectTaskCalibration returns the one caller's single task's median.
func defectTaskCalibration(t *testing.T, s *Store, caller string) (tasks int, median float64) {
	t.Helper()
	cal, err := s.Calibration()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cal.Callers {
		if c.Caller != caller {
			continue
		}
		for _, l := range c.Levels {
			if l.Difficulty == "hard" {
				return l.Tasks, l.Median
			}
		}
	}
	t.Fatalf("caller %s is not in the calibration: %+v", caller, cal.Callers)
	return 0, 0
}

// A defect reported after the hand-back is stored and counts as one fix on
// the task, even though the caller sent the worker no follow-up.
func TestDefectAfterHandbackCountsOneFix(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	wid := ratingRun(t, s, node, caller, "p/m:high", "hard")

	out, err := s.AddRating(defectFrame(wid, caller, "the merge left the old path in place"))
	if err != nil {
		t.Fatal(err)
	}
	if !out.Accepted || out.Worker != wid || out.Model != "p/m" || out.Difficulty != "hard" || out.Kind != protocol.RateKindDefectLater {
		t.Fatalf("defect result: %+v", out)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM worker_defects WHERE session_id = ?`, wid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("defects stored %d times", n)
	}
	tasks, median := defectTaskCalibration(t, s, caller)
	if tasks != 1 || median != 1 {
		t.Fatalf("calibration after one defect: tasks %d median %v, want 1 and 1", tasks, median)
	}
	cal, err := s.Calibration()
	if err != nil {
		t.Fatal(err)
	}
	if cal.Defects != 1 {
		t.Fatalf("calibration defects = %d, want 1", cal.Defects)
	}
	found := false
	for _, r := range cal.Ratings {
		if r.Model == "p/m" && r.Count == 0 && r.Defects == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("model defect count missing from the ratings line: %+v", cal.Ratings)
	}
}

// Each report is one defect: a second one counts again and the rating stays
// separate (a defect may follow a rating).
func TestSecondDefectCountsAgain(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	wid := ratingRun(t, s, node, caller, "p/m", "hard")

	if _, err := s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{Worker: wid, Score: 4}, Rater: caller}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		out, err := s.AddRating(defectFrame(wid, caller, "second look found another bug"))
		if err != nil {
			t.Fatal(err)
		}
		if !out.Accepted {
			t.Fatalf("defect %d refused: %+v", i+1, out)
		}
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM worker_defects WHERE session_id = ?`, wid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("defects stored %d times, want 2", n)
	}
	tasks, median := defectTaskCalibration(t, s, caller)
	if tasks != 1 || median != 2 {
		t.Fatalf("calibration after two defects: tasks %d median %v, want 1 and 2", tasks, median)
	}
}

// A foreign caller is refused; the owner at the machine is accepted.
func TestDefectRefusesForeignCallerAcceptsOwner(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	other := ratingCaller(t, s, node, "other", "gpt-6.1-sol")
	ownerID, err := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness,
		NativeID: protocol.OwnerNativeID, Name: "you (on mac)"})
	if err != nil {
		t.Fatal(err)
	}
	wid := ratingRun(t, s, node, caller, "p/m", "normal")

	out, err := s.AddRating(defectFrame(wid, other, "not mine"))
	if err != nil {
		t.Fatal(err)
	}
	if out.Accepted || !strings.Contains(out.Reason, "not started") {
		t.Fatalf("foreign defect was not refused: %+v", out)
	}
	out, err = s.AddRating(defectFrame(wid, ownerID, "found after the merge"))
	if err != nil {
		t.Fatal(err)
	}
	if !out.Accepted {
		t.Fatalf("owner defect was refused: %+v", out)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM worker_defects WHERE session_id = ?`, wid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("defects stored %d times, want the owner's one", n)
	}
}

// The note is one line, cut to the rating limit, with secrets masked.
func TestDefectNoteIsCutAndMasked(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	wid := ratingRun(t, s, node, caller, "p/m", "normal")
	note := "old path\nleft in place\t sk-abcdefghijklmnopqrstuvwxyz123456 " + strings.Repeat("x", 400)
	out, err := s.AddRating(defectFrame(wid, caller, note))
	if err != nil {
		t.Fatal(err)
	}
	if !out.Accepted {
		t.Fatalf("defect refused: %+v", out)
	}
	var stored string
	if err := s.db.QueryRow(`SELECT note FROM worker_defects WHERE session_id = ?`, wid).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(stored, "\n\t") {
		t.Fatalf("stored note has control characters: %q", stored)
	}
	if len(stored) > protocol.RateNoteMax {
		t.Fatalf("stored note is %d bytes, want at most %d", len(stored), protocol.RateNoteMax)
	}
	if strings.Contains(stored, "abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("secret survived in the note: %q", stored)
	}
	if !strings.HasPrefix(stored, "old path left in place ") {
		t.Fatalf("stored note: %q", stored)
	}
}

// A defect needs a finished run to attach to: a worker session alone is not
// a hand-back.
func TestDefectBeforeHandbackIsRefused(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	wid := ratingWorker(t, s, node, caller, "p/m", "normal")

	out, err := s.AddRating(defectFrame(wid, caller, "not finished yet"))
	if err != nil {
		t.Fatal(err)
	}
	if out.Accepted || !strings.Contains(out.Reason, "not finished") {
		t.Fatalf("defect before hand-back was not refused: %+v", out)
	}
}

func TestRateRejectsUnknownKind(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	wid := ratingRun(t, s, node, caller, "p/m", "normal")
	if _, err := s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{Worker: wid, Kind: "bug", Note: "x"}, Rater: caller}); err == nil {
		t.Fatal("an unknown kind was accepted")
	}
}

// A defect carries a required severity of 1 to 4, and the report adds that
// many points of rework to the task.
func TestDefectSeverityAddsPoints(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	wid := ratingRun(t, s, node, caller, "p/m", "hard")
	frame := defectFrame(wid, caller, "the merge left data behind")
	frame.Severity = 3
	out, err := s.AddRating(frame)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Accepted || out.Severity != 3 {
		t.Fatalf("severity 3 defect: %+v", out)
	}
	var stored int
	if err := s.db.QueryRow(`SELECT severity FROM worker_defects WHERE session_id = ?`, wid).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 3 {
		t.Fatalf("stored severity = %d, want 3", stored)
	}
	tasks, median := defectTaskCalibration(t, s, caller)
	if tasks != 1 || median != 3 {
		t.Fatalf("calibration after a severity 3 defect: tasks %d median %v, want 1 and 3", tasks, median)
	}
}

func TestDefectSeverityMustBeOneToFour(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	wid := ratingRun(t, s, node, caller, "p/m", "normal")
	for _, severity := range []int{0, -1, 5, 9} {
		frame := defectFrame(wid, caller, "no")
		frame.Severity = severity
		if _, err := s.AddRating(frame); err == nil {
			t.Fatalf("severity %d was accepted", severity)
		}
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM worker_defects WHERE session_id = ?`, wid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("an invalid severity was stored")
	}
}

// A continue now weighs 0.33 in the task's rework, and a fix stays 1.
func TestContinueWeighsThirtyThreeHundredths(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: "s_cont", Harness: "opencode",
		Model: "p/m", Outcome: protocol.OutcomeComplete, EndedAt: s.now().Unix(), Caller: caller,
		Difficulty: "hard", Continue: 1}); err != nil {
		t.Fatal(err)
	}
	tasks, median := defectTaskCalibration(t, s, caller)
	if tasks != 1 || median != 0.33 {
		t.Fatalf("one continue: tasks %d median %v, want 1 and 0.33", tasks, median)
	}
}

// Defects are read only for the chooser, like ratings.
func TestDefectsChangeNoChooserBehavior(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	candidates := []protocol.WorkerModelCandidate{
		{Name: "a", Harness: "codex", Model: "a"},
		{Name: "b", Harness: "codex", Model: "b"},
	}
	scores := []protocol.ModelScore{{Match: "a", Score: 40}, {Match: "b", Score: 40}}
	first := protocol.WorkerRotation{}
	firstPick, err := chooseModelTest(candidates, "normal", scores, nil, nil, nil, &first)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.WorkerRotation()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		wid := ratingRun(t, s, node, caller, "a", "normal")
		if _, err := s.AddRating(defectFrame(wid, caller, "bad")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Calibration(); err != nil {
		t.Fatal(err)
	}
	after, err := s.WorkerRotation()
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Levels) != len(after.Levels) {
		t.Fatalf("defects changed the rotation: %+v -> %+v", before, after)
	}
	second := protocol.WorkerRotation{}
	secondPick, err := chooseModelTest(candidates, "normal", scores, nil, nil, nil, &second)
	if err != nil {
		t.Fatal(err)
	}
	if firstPick.Model != secondPick.Model {
		t.Fatalf("defects changed the pick: %s -> %s", firstPick.Model, secondPick.Model)
	}
}
