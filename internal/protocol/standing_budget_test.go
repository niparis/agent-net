package protocol

import (
	"strings"
	"testing"
)

func TestStandingOrdersBudgetPins(t *testing.T) {
	orders := []StandingOrder{
		{ID: "old", Text: strings.Repeat("old ", 1024), SavedAt: 1},
		{ID: "pin", Text: "always include this", Pinned: true, SavedAt: 2},
		{ID: "new", Text: strings.Repeat("new ", 1019), SavedAt: 3},
	}
	got := StandingOrdersBrief(orders)
	if !strings.Contains(got, "- always include this\n") || !strings.Contains(got, "1 more standing orders: search them with cto_standing_orders search") || strings.Contains(got, "- old ") {
		t.Fatal(got)
	}
}
