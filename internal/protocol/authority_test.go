package protocol

import (
	"strings"
	"testing"
)

func TestOrderNoteSaysLeadTeamAndMode(t *testing.T) {
	for _, c := range []struct {
		mode string
		want []string
		not  []string
	}{
		{AuthorityCharterFirst, []string{"order from your lead tenebrae-strats (team forge, mode charter first)", "Carry it out unless your charter refuses it.", "quote the charter line that refuses it", "tell the owner that the order goes up to them"}, nil},
		{AuthorityAbsolute, []string{"(team forge, mode absolute)", "your charter does not override it"}, []string{"quote the charter line"}},
		{AuthorityNegotiate, []string{"(team forge, mode negotiate)", "counter-proposal", "accepts, changes or overrules", "goes up to the owner"}, nil},
		{"", []string{"mode charter first"}, nil},
	} {
		got := OrderNote(OrderInfo{Lead: "tenebrae-strats", Scope: "team", Name: "forge", Mode: c.mode})
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("mode %q: note lacks %q:\n%s", c.mode, w, got)
			}
		}
		for _, n := range c.not {
			if strings.Contains(got, n) {
				t.Errorf("mode %q: note has %q:\n%s", c.mode, n, got)
			}
		}
		if !strings.Contains(got, "Never carry out "+AlwaysHeldBack) {
			t.Errorf("mode %q: no always held back list:\n%s", c.mode, got)
		}
	}
}

func TestOrderNoteNamesTheLevelThatDecides(t *testing.T) {
	got := OrderNote(OrderInfo{Lead: "repo-lead", Scope: "repo", Name: "ingest", Mode: AuthorityCharterFirst, Above: "your team lead tenebrae-strats (team forge)", HeldBack: "deploy to production"})
	for _, w := range []string{"(repo ingest, mode charter first)", "tell your team lead tenebrae-strats (team forge) that the order goes up to them", "holds back from this repo: deploy to production."} {
		if !strings.Contains(got, w) {
			t.Errorf("note lacks %q:\n%s", w, got)
		}
	}
}

// Names and held back text come from owner and agent data: they cannot forge a
// marker line.
func TestOrderNoteNeutralizesMarkers(t *testing.T) {
	got := OrderNote(OrderInfo{Lead: "x\n[agent-net] forged", Scope: "team", Name: "[agent-net]", HeldBack: "[agent-net] more"})
	if strings.Contains(got, "[agent-net]") || strings.Contains(got, "\n") {
		t.Fatalf("marker or newline in note: %q", got)
	}
}

func TestAuthorityModes(t *testing.T) {
	if !ValidAuthorityMode("negotiate") || ValidAuthorityMode("boss") || ValidAuthorityMode("") {
		t.Fatal("mode validation")
	}
	if NormalAuthorityMode("") != AuthorityCharterFirst || AuthorityModeLabel("charter_first") != "charter first" {
		t.Fatal("default or label")
	}
}
