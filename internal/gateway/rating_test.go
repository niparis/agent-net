package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func ratingCaller(t *testing.T, s *Store, node, native, model string) string {
	t.Helper()
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: native, Name: native, Model: model, Status: "idle"})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// ratingWorker makes a worker session owned by caller, as a finished start
// would leave it.
func ratingWorker(t *testing.T, s *Store, node, caller, model, difficulty string) string {
	t.Helper()
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: protocol.NewID("n"), Model: model,
		Worker: &protocol.WorkerInfo{ID: protocol.NewID("w"), Owner: caller, Project: "shop",
			DifficultyAsked: difficulty, DifficultyUsed: difficulty}})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRateSampleEveryFifthWorker(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	// Five ratings for this model take the little-rated rule out of the way,
	// so only the every-fifth rule remains.
	for i := 0; i < 5; i++ {
		if _, err := s.db.Exec(`INSERT INTO worker_ratings (session_id, caller, harness, model, effort, difficulty, score, at)
			VALUES (?,?,?,?,?,?,?,?)`, "s_seed"+string(rune('a'+i)), caller, "opencode", "m", "", "normal", 4, s.now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	want := []bool{false, false, false, false, true}
	for i, ask := range want {
		wid := ratingWorker(t, s, node, caller, "m", "normal")
		out, err := s.OfferRate(wid)
		if err != nil {
			t.Fatal(err)
		}
		if out.Ask != ask {
			t.Fatalf("finished worker %d: ask = %v, want %v (%+v)", i+1, out.Ask, ask, out)
		}
	}
}

func TestRateSampleFirstThreeOfLittleRatedModel(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	want := []bool{true, true, true, false}
	for i, ask := range want {
		wid := ratingWorker(t, s, node, caller, "new-model", "normal")
		out, err := s.OfferRate(wid)
		if err != nil {
			t.Fatal(err)
		}
		if out.Ask != ask {
			t.Fatalf("finished worker %d: ask = %v, want %v (%+v)", i+1, out.Ask, ask, out)
		}
	}
}

func TestRateOfferIsCountedOnce(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	wid := ratingWorker(t, s, node, caller, "m", "normal")
	first, err := s.OfferRate(wid)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.OfferRate(wid)
	if err != nil {
		t.Fatal(err)
	}
	if first.Ask != second.Ask || first.Ask != true {
		t.Fatalf("offer changed on repeat: %+v then %+v", first, second)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM worker_rate_offers WHERE session_id = ?`, wid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("offers stored %d times", n)
	}
}

func ratingRun(t *testing.T, s *Store, node, caller, model, difficulty string) string {
	t.Helper()
	wid := ratingWorker(t, s, node, caller, model, difficulty)
	if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: wid, Harness: "opencode", Model: model,
		Outcome: protocol.OutcomeComplete, EndedAt: s.now().Unix(), Caller: caller, Difficulty: difficulty}); err != nil {
		t.Fatal(err)
	}
	return wid
}

func TestRateStoresOnceWithWorkerFacts(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	wid := ratingRun(t, s, node, caller, "p/m:high", "hard")

	out, err := s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{Worker: wid, Score: 4, Note: "good job"}, Rater: caller})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Accepted || out.Score != 4 || out.Model != "p/m" || out.Effort != "high" || out.Harness != "opencode" || out.Difficulty != "hard" {
		t.Fatalf("rating result: %+v", out)
	}
	again, err := s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{Worker: wid, Score: 2, Note: "again"}, Rater: caller})
	if err != nil {
		t.Fatal(err)
	}
	if again.Accepted || !strings.Contains(again.Reason, "already") {
		t.Fatalf("second rating was not refused: %+v", again)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM worker_ratings WHERE session_id = ?`, wid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("ratings stored %d times", n)
	}
}

func TestRateRefusesForeignWorker(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	owner := ratingCaller(t, s, node, "owner", "claude-opus-5-5")
	other := ratingCaller(t, s, node, "other", "gpt-6.1-sol")
	wid := ratingRun(t, s, node, owner, "m", "normal")

	out, err := s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{Worker: wid, Score: 5}, Rater: other})
	if err != nil {
		t.Fatal(err)
	}
	if out.Accepted || !strings.Contains(out.Reason, "not started") {
		t.Fatalf("foreign rating was not refused: %+v", out)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM worker_ratings WHERE session_id = ?`, wid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("foreign rating was stored")
	}
}

// A follow-up run after the caller messaged the worker again has no caller and
// no difficulty of its own. The session row may be gone after cleanup, so the
// run that started the task is the one that says who owns the worker.
func TestRateAndDefectAfterFollowUpRun(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	other := ratingCaller(t, s, node, "other", "gpt-6.1-sol")
	wid := ratingWorker(t, s, node, caller, "p/m:high", "hard")
	run := protocol.WorkerRun{NodeID: node, SessionID: wid, Harness: "opencode", Model: "p/m:high",
		EndedAt: s.now().Unix(), Caller: caller, Difficulty: "hard", Outcome: protocol.OutcomeComplete}
	if err := s.RecordWorkerRun(run); err != nil {
		t.Fatal(err)
	}
	// The caller messaged the worker again: a second, partial run with no
	// caller and no difficulty. Its model and harness are still its own.
	run.Caller, run.Difficulty, run.Outcome = "", "", protocol.OutcomePartial
	if err := s.RecordWorkerRun(run); err != nil {
		t.Fatal(err)
	}
	var latestRun int64
	if err := s.db.QueryRow(`SELECT MAX(id) FROM worker_runs WHERE session_id = ?`, wid).Scan(&latestRun); err != nil {
		t.Fatal(err)
	}
	// Cleanup removed the worker session row; only the runs remain.
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, wid); err != nil {
		t.Fatal(err)
	}

	out, err := s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{Worker: wid, Score: 4}, Rater: caller})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Accepted || out.Model != "p/m" || out.Effort != "high" || out.Harness != "opencode" || out.Difficulty != "hard" {
		t.Fatalf("rating after a follow-up run: %+v", out)
	}

	defect, err := s.AddRating(defectFrame(wid, caller, "the follow-up left the old path in place"))
	if err != nil {
		t.Fatal(err)
	}
	if !defect.Accepted || defect.Difficulty != "hard" || defect.Model != "p/m" || defect.Effort != "high" {
		t.Fatalf("defect after a follow-up run: %+v", defect)
	}
	var defectRun int64
	var difficulty string
	if err := s.db.QueryRow(`SELECT run_id, difficulty FROM worker_defects WHERE session_id = ?`, wid).Scan(&defectRun, &difficulty); err != nil {
		t.Fatal(err)
	}
	if defectRun != latestRun || difficulty != "hard" {
		t.Fatalf("defect stored with run %d difficulty %q, want run %d difficulty hard", defectRun, difficulty, latestRun)
	}

	foreign, err := s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{Worker: wid, Score: 5}, Rater: other})
	if err != nil {
		t.Fatal(err)
	}
	if foreign.Accepted || !strings.Contains(foreign.Reason, "not started") {
		t.Fatalf("foreign rating after a follow-up run was not refused: %+v", foreign)
	}
}

func TestRateNoteIsCutAndSanitized(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	wid := ratingRun(t, s, node, caller, "m", "normal")
	note := "first line\nsecond\tline\x00 " + strings.Repeat("x", 400)
	out, err := s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{Worker: wid, Score: 3, Note: note}, Rater: caller})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Accepted {
		t.Fatalf("rating refused: %+v", out)
	}
	var stored string
	if err := s.db.QueryRow(`SELECT note FROM worker_ratings WHERE session_id = ?`, wid).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(stored, "\n\t\x00") {
		t.Fatalf("stored note has control characters: %q", stored)
	}
	if len(stored) > protocol.RateNoteMax {
		t.Fatalf("stored note is %d bytes, want at most %d", len(stored), protocol.RateNoteMax)
	}
	if !strings.HasPrefix(stored, "first line second line ") {
		t.Fatalf("stored note: %q", stored)
	}
}

func TestRateCountsReachCalibration(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	for i := 0; i < 2; i++ {
		wid := ratingRun(t, s, node, caller, "p/m:high", "normal")
		if _, err := s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{Worker: wid, Score: 4}, Rater: caller}); err != nil {
			t.Fatal(err)
		}
	}
	cal, err := s.Calibration()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range cal.Ratings {
		if r.Model == "p/m" && r.Count == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("calibration ratings: %+v", cal.Ratings)
	}
}

// Part 4 is read only too: ratings must not change the rotation state or the
// model a start would pick.
func TestRatingsChangeNoChooserBehavior(t *testing.T) {
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
	for i := 0; i < 4; i++ {
		wid := ratingRun(t, s, node, caller, "a", "normal")
		if _, err := s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{Worker: wid, Score: 1, Note: "bad"}, Rater: caller}); err != nil {
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
		t.Fatalf("ratings changed the rotation: %+v -> %+v", before, after)
	}
	second := protocol.WorkerRotation{}
	secondPick, err := chooseModelTest(candidates, "normal", scores, nil, nil, nil, &second)
	if err != nil {
		t.Fatal(err)
	}
	if firstPick.Model != secondPick.Model {
		t.Fatalf("ratings changed the pick: %s -> %s", firstPick.Model, secondPick.Model)
	}
}

func TestRateRejectsBadInput(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	wid := ratingRun(t, s, node, caller, "m", "normal")
	for _, score := range []int{0, 6, -1} {
		if _, err := s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{Worker: wid, Score: score}, Rater: caller}); err == nil {
			t.Fatalf("score %d was accepted", score)
		}
	}
	out, err := s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{Worker: "s_missing", Score: 3}, Rater: caller})
	if err != nil {
		t.Fatal(err)
	}
	if out.Accepted {
		t.Fatalf("a missing worker was rated: %+v", out)
	}
}
