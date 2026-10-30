package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func addFinding(t *testing.T, s *Store, kind, subject, finding, reporter string) protocol.LearnResult {
	t.Helper()
	res, err := s.AddFinding(protocol.Finding{SubjectKind: kind, Subject: subject, Finding: finding, Reporter: reporter})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestFindingsPromoteAfterThreeFromTwoSessions(t *testing.T) {
	s, _ := newTestStore(t)
	addFinding(t, s, protocol.SubjectModel, "terra", "First sign: stops early.", "s_a")
	addFinding(t, s, protocol.SubjectModel, "terra", "Hands back partial work.", "s_b")
	res := addFinding(t, s, protocol.SubjectModel, "terra", "Hands back partial work after ten minutes.", "s_b")
	if !res.Accepted || res.Status != protocol.FindingPromoted {
		t.Fatalf("third finding: %+v", res)
	}
	notes, err := s.PlaybookForChoice("terra", "pi", "shop", "mac")
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) == 0 {
		t.Fatal("no note was promoted")
	}
	n := notes[0]
	if n.SubjectKind != protocol.SubjectModel || n.Subject != "terra" || n.Source != protocol.PlaybookSourceAuto || n.Support != 3 {
		t.Errorf("note: %+v", n)
	}
	if !strings.Contains(n.Note, "Hands back partial work after ten minutes.") || !strings.Contains(n.Note, "3 findings from 2 sessions") {
		t.Errorf("note text: %q", n.Note)
	}
	findings, _ := s.Findings(protocol.SubjectModel, "terra", 10)
	for _, f := range findings {
		if f.Status != protocol.FindingPromoted {
			t.Errorf("finding not promoted: %+v", f)
		}
	}
}

func TestFindingDailyLimit(t *testing.T) {
	s, _ := newTestStore(t)
	for i := 0; i < protocol.FindingsPerSessionDay; i++ {
		if res := addFinding(t, s, protocol.SubjectProject, "shop", "finding", "s_a"); !res.Accepted {
			t.Fatalf("finding %d: %+v", i+1, res)
		}
	}
	res := addFinding(t, s, protocol.SubjectProject, "shop", "one too many", "s_a")
	if res.Accepted || !strings.Contains(res.Reason, "at most 3 findings") || res.Remaining != 0 {
		t.Errorf("fourth finding: %+v", res)
	}
}

func TestBadTrackRecordPromotesANote(t *testing.T) {
	s, _ := newTestStore(t)
	now := s.now()
	for i := 0; i < 4; i++ {
		if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: "n1", SessionID: "s_w" + string(rune('a'+i)),
			Harness: "pi", Model: "terra", Outcome: protocol.OutcomePartial,
			StartedAt: now.Add(-time.Duration(i+1) * time.Hour).Unix(), EndedAt: now.Add(-time.Duration(i+1) * time.Hour).Unix(),
			RunSeconds: 60}); err != nil {
			t.Fatal(err)
		}
	}
	notes, err := s.PlaybookForChoice("terra", "pi", "shop", "mac")
	if err != nil {
		t.Fatal(err)
	}
	var modelNote *protocol.PlaybookNote
	for i := range notes {
		if notes[i].SubjectKind == protocol.SubjectModel {
			modelNote = &notes[i]
		}
	}
	if modelNote == nil {
		t.Fatalf("no bad-track note: %+v", notes)
	}
	if modelNote.Source != protocol.PlaybookSourceAuto || !strings.Contains(modelNote.TrackLine, "4 runs: 4 partial") ||
		!strings.Contains(modelNote.Note, "poor") {
		t.Errorf("note: %+v", modelNote)
	}
}

func TestBadTrackNeedsFourRunsAndUnderHalf(t *testing.T) {
	add := func(t *testing.T, s *Store, outcome string, i int) {
		t.Helper()
		if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: "n1", SessionID: "s_" + outcome + string(rune('a'+i)),
			Harness: "codex", Model: "terra", Outcome: outcome, EndedAt: s.now().Unix(), RunSeconds: 30}); err != nil {
			t.Fatal(err)
		}
	}
	// Three runs are too few, whatever their outcome.
	s, _ := newTestStore(t)
	for i := 0; i < 3; i++ {
		add(t, s, protocol.OutcomePartial, i)
	}
	if notes, _ := s.PlaybookForChoice("terra", "codex", "shop", "mac"); len(notes) != 0 {
		t.Errorf("three runs promoted a note: %+v", notes)
	}
	// Four runs at exactly half complete are not under half.
	s, _ = newTestStore(t)
	add(t, s, protocol.OutcomePartial, 0)
	add(t, s, protocol.OutcomePartial, 1)
	add(t, s, protocol.OutcomeComplete, 2)
	add(t, s, protocol.OutcomeComplete, 3)
	if notes, _ := s.PlaybookForChoice("terra", "codex", "shop", "mac"); len(notes) != 0 {
		t.Errorf("50 percent complete promoted a note: %+v", notes)
	}
	add(t, s, protocol.OutcomePartial, 4)
	if notes, _ := s.PlaybookForChoice("terra", "codex", "shop", "mac"); len(notes) == 0 {
		t.Error("under half complete with five runs did not promote a note")
	}
}

func TestPlaybookNoteGoesStaleAndPinnedStays(t *testing.T) {
	s, c := newTestStore(t)
	addFinding(t, s, protocol.SubjectProject, "shop", "tests need the TEI service", "s_a")
	addFinding(t, s, protocol.SubjectProject, "shop", "tests need the TEI service", "s_b")
	addFinding(t, s, protocol.SubjectProject, "shop", "tests need the TEI service", "s_b")
	notes, _ := s.PlaybookForChoice("m", "pi", "shop", "mac")
	if len(notes) != 1 || notes[0].SubjectKind != protocol.SubjectProject {
		t.Fatalf("project note: %+v", notes)
	}
	// Pinning an automatic note keeps it after its support is old.
	if err := s.SetPlaybookPin("terra", "", "keep this", boolPtr(true)); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add((protocol.PlaybookStaleDays + 1) * 24 * time.Hour)
	notes, _ = s.PlaybookForChoice("m", "pi", "shop", "mac")
	if len(notes) != 0 {
		t.Errorf("stale automatic note still shown: %+v", notes)
	}
	// The owner's pinned note is still there.
	if n, ok, _ := s.PlaybookFor("terra", ""); !ok || n.Note != "keep this" || !n.Pinned {
		t.Errorf("pinned owner note: %+v %v", n, ok)
	}
}

func TestOwnerNoteIsNotOverwrittenByLearning(t *testing.T) {
	s, _ := newTestStore(t)
	addFinding(t, s, protocol.SubjectModel, "terra", "a", "s_a")
	addFinding(t, s, protocol.SubjectModel, "terra", "b", "s_b")
	addFinding(t, s, protocol.SubjectModel, "terra", "c", "s_b")
	if err := s.SetPlaybookPin("terra", "", "owner text", nil); err != nil {
		t.Fatal(err)
	}
	addFinding(t, s, protocol.SubjectModel, "terra", "d", "s_c")
	addFinding(t, s, protocol.SubjectModel, "terra", "e", "s_d")
	addFinding(t, s, protocol.SubjectModel, "terra", "f", "s_d")
	n, ok, _ := s.PlaybookFor("terra", "")
	if !ok || n.Note != "owner text" || n.Source != protocol.PlaybookSourceOwner {
		t.Errorf("owner note changed: %+v %v", n, ok)
	}
}

func boolPtr(v bool) *bool { return &v }
