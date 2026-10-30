package protocol

import (
	"strings"
	"testing"
)

func TestCleanFinding(t *testing.T) {
	f, err := CleanFinding(SubjectModelHarness, " terra@pi ", "Hands back partial work.\x00", []string{"s_w1", "", "s_w2"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Subject != "terra@pi" || f.Finding != "Hands back partial work." || len(f.Evidence) != 2 {
		t.Errorf("finding: %+v", f)
	}
	if _, err := CleanFinding("wrong", "x", "y", nil); err == nil {
		t.Error("an unknown subject kind was accepted")
	}
	if _, err := CleanFinding(SubjectModelHarness, "terra", "y", nil); err == nil {
		t.Error("model_harness without @ was accepted")
	}
	if _, err := CleanFinding(SubjectModel, "terra", strings.Repeat("x", FindingMaxBytes+1), nil); err == nil {
		t.Error("an oversized finding was accepted")
	}
	if _, err := CleanFinding(SubjectProject, "", "y", nil); err == nil {
		t.Error("an empty subject was accepted")
	}
}

func TestFindingNoteKey(t *testing.T) {
	for _, c := range []struct {
		kind, subject, wantKind, wantSubject, wantHarness string
	}{
		{SubjectModel, "terra", SubjectModel, "terra", ""},
		{SubjectHarness, "pi", SubjectHarness, "pi", ""},
		{SubjectModelHarness, "terra@pi", SubjectModelHarness, "terra", "pi"},
		{SubjectProject, "shop", SubjectProject, "shop", ""},
		{SubjectMCP, "tenebrae-library", SubjectMCP, "tenebrae-library", ""},
		{SubjectMachine, "mac", SubjectMachine, "mac", ""},
	} {
		kind, subject, harness := (Finding{SubjectKind: c.kind, Subject: c.subject}).NoteKey()
		if kind != c.wantKind || subject != c.wantSubject || harness != c.wantHarness {
			t.Errorf("%s %s: %s %s %s", c.kind, c.subject, kind, subject, harness)
		}
	}
}
