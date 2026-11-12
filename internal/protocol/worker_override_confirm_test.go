package protocol

import (
	"strings"
	"testing"
)

// confirmCase is a held override of deepseek/cheap-1 for a normal start, with
// a quota reading of 20% used and scores for a cheap and a big model.
func confirmCase() (OverrideFacts, ConfirmContext) {
	f := OverrideFacts{Harness: "opencode", Model: "deepseek/cheap-1", Level: "normal", Provider: "deepseek", CallerFamily: "openai", OverrideFamily: "deepseek"}
	c := ConfirmContext{
		Quotas: &Quotas{Providers: []QuotaProvider{{Provider: "deepseek", Windows: []QuotaWindow{{ID: "5h", PercentUsed: 20}}}}},
		Scores: []ModelScore{{Match: "cheap-1", Score: 32}, {Match: "big-1", Score: 55}},
	}
	return f, c
}

// Decision 0151: the chief confirms only when the tier is the same or
// cheaper, quota allows it and the provider is not protected. Any missing
// fact sends the ask to the CTO.
func TestChiefConfirmRule(t *testing.T) {
	f, c := confirmCase()
	if err := ConfirmOverride(f, c, true); err != nil {
		t.Fatalf("cheaper with quota allowed: %v", err)
	}
	for name, change := range map[string]func(*OverrideFacts, *ConfirmContext){
		"higher tier": func(f *OverrideFacts, _ *ConfirmContext) { f.Model = "deepseek/big-1" },
		"under the worker floor": func(_ *OverrideFacts, c *ConfirmContext) {
			c.Floors = WorkerFloors{Floors: map[string]int{"deepseek": 90}}
		},
		"no score":                func(f *OverrideFacts, _ *ConfirmContext) { f.Model = "deepseek/unknown-9" },
		"no quota reading":        func(_ *OverrideFacts, c *ConfirmContext) { c.Quotas = nil },
		"unknown start level":     func(f *OverrideFacts, _ *ConfirmContext) { f.Level = "" },
		"unknown provider":        func(f *OverrideFacts, _ *ConfirmContext) { f.Provider = "" },
		"no model":                func(f *OverrideFacts, _ *ConfirmContext) { f.Model = "" },
		"protected provider":      func(_ *OverrideFacts, c *ConfirmContext) { c.Protected = []string{"deepseek"} },
		"review, unknown caller":  func(f *OverrideFacts, _ *ConfirmContext) { f.Review, f.CallerFamily = true, "" },
		"review, unknown overrid": func(f *OverrideFacts, _ *ConfirmContext) { f.Review, f.OverrideFamily = true, "" },
	} {
		f, c := confirmCase()
		change(&f, &c)
		err := ConfirmOverride(f, c, true)
		if err == nil || !strings.Contains(err.Error(), "Send it to the CTO") {
			t.Errorf("%s: err = %v, want the ask sent to the CTO", name, err)
		}
	}
	// The CTO is held only by the family rule.
	f, c = confirmCase()
	f.Model, f.Provider, f.Level = "deepseek/big-1", "", ""
	if err := ConfirmOverride(f, ConfirmContext{}, false); err != nil {
		t.Fatalf("CTO confirm: %v", err)
	}
	f.Review, f.CallerFamily = true, "deepseek"
	if err := ConfirmOverride(f, c, false); err == nil {
		t.Fatal("the CTO confirmed the caller's own family for a review of its own work")
	}
}
