package protocol

import (
	"strings"
	"testing"
	"time"
)

// chooseFirst is the ranker's first pick. A test that has no table gets an
// empty one at the level's middle D.
func chooseFirst(candidates []WorkerModelCandidate, level string, scores []ModelScore, minimums map[string]float64, quotas *Quotas, running map[string]int, rotation *WorkerRotation, pick ...*PickerInput) (WorkerModelChoice, error) {
	var in *PickerInput
	if len(pick) > 0 {
		in = pick[0]
	}
	if in == nil {
		in = &PickerInput{D: PickerD(nil, level), Table: BuildPickerTable(nil, DefaultPickerSettings())}
	}
	order, _, err := WorkerPickOrder(candidates, level, scores, minimums, quotas, running, rotation, in)
	if err != nil {
		return WorkerModelChoice{}, err
	}
	return order[0], nil
}

func TestWorkerQuotaRoomMonthly(t *testing.T) {
	now := time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC)
	q := &Quotas{GeneratedAt: now.Unix(), Providers: []QuotaProvider{{Provider: "p", Windows: []QuotaWindow{{Kind: "monthly", Label: "month", PercentUsed: 30, ResetsAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC).Unix()}}}}}
	room, note, known, blocked := workerQuotaRoom(q, "p", "")
	if !known || blocked || room < 20 || room > 25 || !strings.Contains(note, "50% elapsed") {
		t.Fatalf("room=%v note=%q known=%v blocked=%v", room, note, known, blocked)
	}
}

func TestWorkerRotationCloneIsIndependent(t *testing.T) {
	a := WorkerRotation{Levels: map[string]*WorkerRotationLevel{"normal": {Current: map[string]float64{"x": 1}, Recent: []string{"x"}}}}
	b := a.Clone()
	b.Levels["normal"].Current["x"] = 99
	b.Levels["normal"].Recent[0] = "y"
	if a.Levels["normal"].Current["x"] != 1 || a.Levels["normal"].Recent[0] != "x" {
		t.Fatalf("clone shares state: %+v", a)
	}
}

func TestWorkerCandidateMaximumLevelEligibility(t *testing.T) {
	levels := []string{"trivial", "simple", "normal", "hard", "very_complex"}
	for _, tc := range []struct {
		name  string
		score float64
		max   int
	}{
		{"luna", 23.3, 1}, {"deepseek", 32.5, 2}, {"terra", 38.1, 2},
		{"sol", 46.1, 3}, {"terra-high", 42.3, 3}, {"terra-max", 51, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := WorkerModelCandidate{Name: tc.name, Harness: "codex", Model: tc.name}
			scores := []ModelScore{{Match: tc.name, Score: tc.score}}
			for i, level := range levels {
				_, _, eligible, ok := WorkerCandidateFit(c, level, scores, nil)
				want := i == tc.max || i == tc.max-1
				if !ok || eligible != want {
					t.Errorf("%s: eligible=%v, want %v", level, eligible, want)
				}
			}
		})
	}
}

func TestWorkerMaximumLevelUsesMinimumOverrides(t *testing.T) {
	minimums := map[string]float64{"hard": 35, "very_complex": 60}
	c := WorkerModelCandidate{Model: "terra"}
	scores := []ModelScore{{Match: "terra", Score: 38.1}}
	if max := WorkerMaximumLevel(38.1, minimums); max != "hard" {
		t.Fatalf("max=%s", max)
	}
	for _, level := range []string{"simple", "normal", "hard"} {
		_, _, eligible, _ := WorkerCandidateFit(c, level, scores, minimums)
		if eligible != (level != "simple") {
			t.Errorf("%s: eligible=%v", level, eligible)
		}
	}
}

// A start is refused only for a window that limits its model.
func TestQuotaRefusalAgyPerFamily(t *testing.T) {
	q := &Quotas{GeneratedAt: 1, Providers: []QuotaProvider{{Provider: "agy", Windows: []QuotaWindow{
		{ID: "gemini_5h", Kind: "session", PercentUsed: 9},
		{ID: "claude_gpt_5h", Kind: "session", PercentUsed: 100, Exhausted: true},
	}}}}
	if r := QuotaRefusal(q, "agy", "gemini-4-argon-high"); r != "" {
		t.Fatalf("argon refused: %s", r)
	}
	if r := QuotaRefusal(q, "agy", "claude-opus-5-5-high"); !strings.Contains(r, "Claude/GPT") && !strings.Contains(r, "claude_gpt_5h") {
		t.Fatalf("opus not refused: %q", r)
	}
}

// A start with no candidate the cell rule allows names the level and says why.
func TestWorkerNoEligibleModelExplainsFix(t *testing.T) {
	c := []WorkerModelCandidate{{Name: "unknown", Harness: "pi", Model: "unscored"}}
	_, err := chooseFirst(c, "normal", nil, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("an unscored model was picked")
	}
	for _, want := range []string{"normal", "no model qualifies", "no profile is allowed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}
