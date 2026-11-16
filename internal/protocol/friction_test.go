package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// Decision 0170: a friction entry names one area and says one line about what
// slowed the agent down. The area is fixed, so reports can be grouped later.
func TestFrictionEntryValidate(t *testing.T) {
	ok := []FrictionEntry{
		{Area: FrictionTooling, Text: "the MCP server took two restarts"},
		{Area: FrictionSkill, Text: "the skill archive was read only"},
	}
	for _, f := range ok {
		if err := f.Validate(); err != nil {
			t.Errorf("%+v: %v", f, err)
		}
	}
	bad := []FrictionEntry{
		{Area: "mood", Text: "not an area"},
		{Area: FrictionTooling, Text: ""},
		{Area: FrictionTooling, Text: "   "},
		{Area: FrictionTooling, Text: "two\nlines"},
		{Area: FrictionTooling, Text: strings.Repeat("x", MaxFrictionText+1)},
	}
	for _, f := range bad {
		if err := f.Validate(); err == nil {
			t.Errorf("%+v was accepted", f)
		}
	}
	if err := ValidateFriction(nil); err != nil {
		t.Errorf("an empty list means nothing to report: %v", err)
	}
	if err := ValidateFriction([]FrictionEntry{{Area: FrictionCI, Text: "the suite flaked"}}); err != nil {
		t.Errorf("valid list refused: %v", err)
	}
}

// The hand-back carries the list through JSON as the tool call sends it.
func TestFrictionRidesOnHandbackAndStepReport(t *testing.T) {
	want := WorkerHandbackRequest{Report: "done", Friction: []FrictionEntry{{Area: FrictionBrief, Text: "the brief named the wrong branch"}}}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"friction":[{"area":"brief","text":"the brief named the wrong branch"}]`) {
		t.Fatalf("friction missing from JSON: %s", b)
	}
	args := StepReportArgs{Change: "ch_1", Result: "pass", Commit: strings.Repeat("a", 40), Friction: want.Friction}
	batch, err := args.IsBatch()
	if err != nil || batch {
		t.Fatalf("a single report with friction must not read as a batch: %v %v", batch, err)
	}
}

// The worker brief's closing checklist asks for friction, and says none is
// fine, so a worker does not have to invent a report.
func TestWorkerBriefAsksForFriction(t *testing.T) {
	if !strings.Contains(WorkerPreamble, "friction") || !strings.Contains(WorkerPreamble, "none when nothing did") {
		t.Fatalf("the worker brief does not ask for friction:\n%s", WorkerPreamble)
	}
}

func TestFrictionFindQueryValidate(t *testing.T) {
	if err := (FrictionQuery{Since: "2026-11-01T00:00:00Z", Limit: 20}).Validate(); err != nil {
		t.Errorf("valid query refused: %v", err)
	}
	for _, q := range []FrictionQuery{
		{Since: "yesterday"},
		{Area: "mood"},
		{Limit: -1},
	} {
		if err := q.Validate(); err == nil {
			t.Errorf("%+v was accepted", q)
		}
	}
}
