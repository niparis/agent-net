package protocol

import (
	"testing"
)

func TestWorkerFloorsFloorLookup(t *testing.T) {
	wf := WorkerFloors{
		Floors: map[string]int{
			"anthropic": 40,
			"openai":    25,
		},
	}
	if got := wf.Floor("anthropic"); got != 40 {
		t.Errorf("Floor(anthropic) = %d, want 40", got)
	}
	if got := wf.Floor("Anthropic"); got != 40 {
		t.Errorf("Floor(Anthropic) = %d, want 40", got)
	}
	if got := wf.Floor("claude"); got != 40 {
		t.Errorf("Floor(claude) = %d, want 40", got)
	}
	if got := wf.Floor("codex"); got != 25 {
		t.Errorf("Floor(codex) = %d, want 25", got)
	}
	if got := wf.Floor("deepseek"); got != 0 {
		t.Errorf("Floor(deepseek) = %d, want 0", got)
	}
}

func TestCheckWorkerFloor(t *testing.T) {
	floors := WorkerFloors{
		Floors: map[string]int{
			"anthropic": 40,
			"deepseek":  30,
		},
	}

	// 1. Anthropic under floor: 35% left < 40%
	qUnder := &Quotas{
		Providers: []QuotaProvider{
			{
				Provider: "claude",
				Windows: []QuotaWindow{
					{ID: "5h", PercentUsed: 65}, // 35% left
				},
			},
		},
	}
	under, note, noReading := CheckWorkerFloor(qUnder, floors, "claude", "claude-opus-5-5")
	if !under {
		t.Fatalf("expected under floor, got under=%v", under)
	}
	if noReading {
		t.Fatalf("expected reading present, got noReading=true")
	}
	wantSentence := "anthropic has 35% left in its 5h window, under your 40% worker floor"
	if note != wantSentence {
		t.Errorf("note = %q, want %q", note, wantSentence)
	}

	// 2. Anthropic above floor: 50% left >= 40%
	qAbove := &Quotas{
		Providers: []QuotaProvider{
			{
				Provider: "claude",
				Windows: []QuotaWindow{
					{ID: "5h", PercentUsed: 50}, // 50% left
				},
			},
		},
	}
	under, note, noReading = CheckWorkerFloor(qAbove, floors, "claude", "claude-opus-5-5")
	if under {
		t.Fatalf("expected above floor, got under=true (note=%s)", note)
	}
	if noReading {
		t.Fatalf("expected reading present")
	}
	if note != "" {
		t.Errorf("note = %q, want empty", note)
	}

	// 3. Provider with floor but no quota reading: kept with note
	under, note, noReading = CheckWorkerFloor(qAbove, floors, "opencode", "deepseek/deepseek-v4.1-flash")
	if under {
		t.Fatalf("expected not under floor when no reading, got under=true")
	}
	if !noReading {
		t.Fatalf("expected noReading=true")
	}
	if note != "no quota reading, floor not applied" {
		t.Errorf("note = %q, want 'no quota reading, floor not applied'", note)
	}

	// 4. Provider with floor 0: off
	under, note, noReading = CheckWorkerFloor(qAbove, floors, "codex", "gpt-6.1-terra")
	if under || noReading || note != "" {
		t.Errorf("floor 0 gave under=%v, note=%q, noReading=%v", under, note, noReading)
	}

	// 5. Monthly window at 99% used with low rolling/weekly windows is skipped with monthly reason
	qMonthly := &Quotas{
		Providers: []QuotaProvider{
			{
				Provider: "opencode-go",
				Windows: []QuotaWindow{
					{ID: "rolling", Label: "rolling", Kind: "rolling", PercentUsed: 5},
					{ID: "weekly", Label: "weekly", Kind: "weekly", PercentUsed: 10},
					{ID: "included_usage", Label: "month", Kind: "monthly", PercentUsed: 99},
				},
			},
		},
	}
	floorsOpencodeGo := WorkerFloors{
		Floors: map[string]int{
			"opencode-go": 10,
		},
	}
	under, note, noReading = CheckWorkerFloor(qMonthly, floorsOpencodeGo, "opencode", "opencode-go/deepseek-v4.1-flash")
	if !under {
		t.Fatalf("expected monthly at 99%% to be under floor, got under=false")
	}
	if noReading {
		t.Fatalf("expected reading present")
	}
	wantMonthlySentence := "opencode-go has 1% left in its monthly window, under your 10% worker floor"
	if note != wantMonthlySentence {
		t.Errorf("note = %q, want %q", note, wantMonthlySentence)
	}

	// 6. Provider with all windows above floor is kept
	qMonthlyAbove := &Quotas{
		Providers: []QuotaProvider{
			{
				Provider: "opencode-go",
				Windows: []QuotaWindow{
					{ID: "rolling", Label: "rolling", Kind: "rolling", PercentUsed: 5},
					{ID: "weekly", Label: "weekly", Kind: "weekly", PercentUsed: 10},
					{ID: "included_usage", Label: "month", Kind: "monthly", PercentUsed: 40},
				},
			},
		},
	}
	under, note, noReading = CheckWorkerFloor(qMonthlyAbove, floorsOpencodeGo, "opencode", "opencode-go/deepseek-v4.1-flash")
	if under {
		t.Fatalf("expected all windows above floor to be kept, got under=true (note=%s)", note)
	}
	if noReading {
		t.Fatalf("expected reading present")
	}
	if note != "" {
		t.Errorf("note = %q, want empty", note)
	}
}

func TestQuotaWorkerFloorsMonthlyWindow(t *testing.T) {
	floors := WorkerFloors{
		Floors: map[string]int{
			"opencode-go": 10,
		},
	}

	// 1. A provider with a monthly window at 99% and other windows low is skipped, with the monthly reason.
	// Even if rolling window is also under floor (e.g. 8% left), the more constrained monthly window (1% left) wins.
	qMonthly := &Quotas{
		Providers: []QuotaProvider{
			{
				Provider: "opencode-go",
				Windows: []QuotaWindow{
					{ID: "rolling", Label: "rolling", Kind: "rolling", PercentUsed: 92},      // 8% left (< 10%)
					{ID: "weekly", Label: "weekly", Kind: "weekly", PercentUsed: 10},         // 90% left
					{ID: "included_usage", Label: "month", Kind: "monthly", PercentUsed: 99}, // 1% left (< 10%, most constrained)
				},
			},
		},
	}

	under, note, noReading := CheckWorkerFloor(qMonthly, floors, "opencode", "opencode-go/deepseek-v4.1-flash")
	if !under {
		t.Fatalf("expected monthly at 99%% to be under floor, got under=false")
	}
	if noReading {
		t.Fatalf("expected reading present, got noReading=true")
	}
	wantSentence := "opencode-go has 1% left in its monthly window, under your 10% worker floor"
	if note != wantSentence {
		t.Errorf("note = %q, want %q", note, wantSentence)
	}

	// 2. A provider with all windows above the floor is still a candidate.
	qAbove := &Quotas{
		Providers: []QuotaProvider{
			{
				Provider: "opencode-go",
				Windows: []QuotaWindow{
					{ID: "rolling", Label: "rolling", Kind: "rolling", PercentUsed: 5},
					{ID: "weekly", Label: "weekly", Kind: "weekly", PercentUsed: 10},
					{ID: "included_usage", Label: "month", Kind: "monthly", PercentUsed: 40},
				},
			},
		},
	}
	under, note, noReading = CheckWorkerFloor(qAbove, floors, "opencode", "opencode-go/deepseek-v4.1-flash")
	if under {
		t.Fatalf("expected above floor, got under=true (note=%s)", note)
	}
	if noReading {
		t.Fatalf("expected reading present")
	}
	if note != "" {
		t.Errorf("note = %q, want empty", note)
	}
}
