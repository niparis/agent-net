package protocol

import (
	"strings"
	"testing"
)

// Decision 0175: reports that say the same thing share a group. Paths, ids and
// numbers do not make a difference, but the area and the words do.
func TestFrictionGroupIgnoresPathsIDsAndNumbers(t *testing.T) {
	a := FrictionGroup(FrictionCI, "the suite flaked on run 12 in /w/a from s_497b6837220c0c89")
	b := FrictionGroup(FrictionCI, "The suite flaked on run 99 in /w/b from s_1111aaaa2222bbbb")
	if a != b {
		t.Fatalf("the same report has two groups: %q and %q", a, b)
	}
	if !strings.HasPrefix(a, "fg_") || len(a) != len("fg_")+12 {
		t.Fatalf("group id shape: %q", a)
	}
	if FrictionGroup(FrictionTooling, "the suite flaked on run 12") == a {
		t.Fatal("the area does not change the group")
	}
	if FrictionGroup(FrictionCI, "the review took two days") == a {
		t.Fatal("different words share a group")
	}
}

// A mark names one way to pick reports, a status, and short plain link and note.
func TestFrictionMarkValidatesSelectionAndFields(t *testing.T) {
	ok := []FrictionMarkRequest{
		{IDs: []int64{1, 2}, Status: FrictionRead},
		{Group: "fg_0123456789ab", Status: FrictionFixed, Link: "ch_1234"},
		{Area: FrictionCI, Text: "flaked", Status: FrictionWontfix, Link: "https://example.test/issue/1"},
		{Project: "demo", Status: FrictionInfra, Note: "the runner was full"},
	}
	for i, q := range ok {
		if err := q.Validate(); err != nil {
			t.Fatalf("valid mark %d refused: %v", i, err)
		}
	}
	refused := map[string]FrictionMarkRequest{
		"no status":         {IDs: []int64{1}},
		"unknown status":    {IDs: []int64{1}, Status: "closed"},
		"no selection":      {Status: FrictionRead},
		"ids and group":     {IDs: []int64{1}, Group: "fg_x", Status: FrictionRead},
		"group and filter":  {Group: "fg_x", Area: FrictionCI, Status: FrictionRead},
		"zero id":           {IDs: []int64{0}, Status: FrictionRead},
		"unknown area":      {Area: "docs", Status: FrictionRead},
		"link with a space": {IDs: []int64{1}, Status: FrictionRead, Link: "ch_1 2"},
		"link not a change": {IDs: []int64{1}, Status: FrictionRead, Link: "issue 12"},
		"note too long":     {IDs: []int64{1}, Status: FrictionRead, Note: strings.Repeat("n", MaxFrictionNote+1)},
	}
	for name, q := range refused {
		if err := q.Validate(); err == nil {
			t.Fatalf("%s: the mark was accepted", name)
		}
	}
}
