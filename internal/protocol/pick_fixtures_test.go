package protocol

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// pickFixture is one fixed scenario: the roster, the scores, the quotas and
// the picker input, and how many picks to make in a row. The rotation state
// carries from one pick to the next, as it does in the gateway.
type pickFixture struct {
	name       string
	candidates []WorkerModelCandidate
	level      string
	scores     []ModelScore
	quotas     *Quotas
	in         *PickerInput
	picks      int
}

// pickFixtures covers each branch of the picker: the cost rule with a thin
// cell and its exploration share, the cost rule with prices, the quota pace
// either way, the rotation with no qualifying model, the level rule when no
// model is allowed, the level rule without a picker input, the notes that name
// the left out and the benchmark models, and a provider out of quota.
func pickFixtures() []pickFixture {
	var thinTasks []PickerTask
	thinTasks = append(thinTasks, pickTasks("good", 2.5, repeat(15, 1, .5)...)...)
	thinTasks = append(thinTasks, pickTasks("new", 2.5, 1, 0)...)

	var costTasksAll []PickerTask
	costTasksAll = append(costTasksAll, costTasks("opus", 2.5, 2, repeat(10, 1, .75)...)...)
	costTasksAll = append(costTasksAll, costTasks("sonnet", 2.5, .6, repeat(10, 1, .5)...)...)
	costTasksAll = append(costTasksAll, costTasks("deepseek", 2.5, .05, repeat(10, .75)...)...)

	paceTasks := append(costTasks("a", 2.5, 1, repeat(5, 1)...), costTasks("b", 2.5, 1, repeat(5, 1)...)...)
	paceCandidates := pickCandidates("a", "b")
	paceCandidates[0].Provider, paceCandidates[1].Provider = "pa", "pb"
	weekly := func(used float64) []QuotaWindow {
		return []QuotaWindow{{Kind: "weekly", PercentUsed: used, ResetsAt: 1000 + 7*12*3600}}
	}

	priceTasks := costTasks("claude-opus-5-5:high", 2.5, 1, repeat(10, 1)...)
	priceCandidates := []WorkerModelCandidate{
		{Name: "opus", Harness: "claude", Model: "claude-opus-5-5:high"},
		{Name: "haiku", Harness: "claude", Model: "claude-haiku-5-5:high"},
		{Name: "mystery", Harness: "claude", Model: "mystery-9:high"},
	}

	leftTasks := append(pickTasks("deepseek", 2.5, repeat(15, 1, .5)...), pickTasks("flash", 2.5, 1, 1, .5, .5, 0, 1)...)
	leftTasks = append(leftTasks, pickTasks("terra:medium", 2.5, repeat(10, 0)...)...)
	leftTasks = append(leftTasks, pickTasks("deepseek", 1.2, 1, 1, 1, 1)...)
	leftCandidates := []WorkerModelCandidate{
		{Name: "deepseek", Harness: "pi", Model: "deepseek"}, {Name: "flash", Harness: "pi", Model: "flash"},
		{Name: "terra:medium", Harness: "pi", Model: "terra:medium"},
	}

	priorTasks := append(pickTasks("cheap", 1.8, repeat(10, 1, .8, 1)...), pickTasks("strong", 1.8, repeat(10, .5, .5, .5)...)...)
	lenient := DefaultPickerSettings()
	lenient.AllowedFraction = .3

	exhausted := &Quotas{Providers: []QuotaProvider{{Provider: "opencode-go", Windows: []QuotaWindow{{Kind: "monthly", Exhausted: true}}}}}
	exhaustedCandidates := []WorkerModelCandidate{
		{Name: "terra", Harness: "codex", Model: "gpt-6.1-terra"},
		{Name: "deepseek", Harness: "opencode", Model: "deepseek/deepseek-v4.1-flash"},
		{Name: "go", Harness: "opencode", Model: "opencode-go/deepseek-v4.1-flash"},
	}
	exhaustedScores := []ModelScore{{Match: "gpt-6.1-terra", Score: 51}, {Match: "deepseek/deepseek-v4.1-flash", Score: 45}, {Match: "opencode-go/deepseek-v4.1-flash", Score: 45}}

	paceInput := func(tasks []PickerTask) *PickerInput {
		return &PickerInput{D: 2.5, Table: BuildPickerTable(tasks, DefaultPickerSettings())}
	}
	return []pickFixture{
		{"cell rule with a thin cell at the explore share", pickCandidates("good", "new"), "normal",
			pickScores(map[string]float64{"good": 51, "new": 39}), nil, paceInput(thinTasks), 12},
		{"cell rule with API prices and a model with no price", priceCandidates, "normal",
			pickScores(map[string]float64{"claude-opus-5-5": 51, "claude-haiku-5-5": 51, "mystery-9": 51}), nil,
			&PickerInput{D: 2.5, Table: BuildPickerTable(priceTasks, DefaultPickerSettings()), Prices: fakePrices()}, 12},
		{"cost rule, cheapest per accepted result", pickCandidates("opus", "sonnet", "deepseek"), "normal",
			pickScores(map[string]float64{"opus": 51, "sonnet": 51, "deepseek": 51}), nil, paceInput(costTasksAll), 12},
		{"quota pace: the lagging provider wins a tie", paceCandidates, "normal",
			pickScores(map[string]float64{"a": 50, "b": 50}),
			&Quotas{GeneratedAt: 1000, Providers: []QuotaProvider{{Provider: "pa", Windows: weekly(30)}}}, paceInput(paceTasks), 12},
		{"quota pace: the provider ahead of pace loses a tie", paceCandidates, "normal",
			pickScores(map[string]float64{"a": 50, "b": 50}),
			&Quotas{GeneratedAt: 1000, Providers: []QuotaProvider{{Provider: "pa", Windows: weekly(70)}, {Provider: "pb", Windows: weekly(30)}}}, paceInput(paceTasks), 12},
		{"rotation over the allowed set when none qualifies", pickCandidates("x", "y"), "normal",
			pickScores(map[string]float64{"x": 39, "y": 39}), nil,
			paceInput(append(pickTasks("x", 2.5, 1, 0), pickTasks("y", 2.5, 1, 1)...)), 12},
		{"level rule when no model is allowed", pickCandidates("a", "b"), "normal",
			pickScores(map[string]float64{"a": 39, "b": 39}), nil,
			&PickerInput{D: 4.5, Table: BuildPickerTable(nil, DefaultPickerSettings())}, 12},
		{"level rule without a picker input", pickCandidates("a"), "normal",
			pickScores(map[string]float64{"a": 39}), nil, nil, 3},
		{"note names the left out models", leftCandidates, "normal",
			pickScores(map[string]float64{"deepseek": 51, "flash": 51, "terra:medium": 51}), nil,
			&PickerInput{D: 2.5, Table: BuildPickerTable(leftTasks, DefaultPickerSettings())}, 12},
		{"note names a prior and the chosen model, not the best", pickCandidates("cheap", "strong", "fresh"), "simple",
			pickScores(map[string]float64{"cheap": 29, "strong": 51, "fresh": 51}), nil,
			&PickerInput{D: 1.8, Table: BuildPickerTable(priorTasks, lenient)}, 12},
		{"a provider out of quota stays last", exhaustedCandidates, "hard",
			exhaustedScores, exhausted, nil, 3},
	}
}

// pickFixtureDump runs every fixture and writes what the picker returned for
// each pick: the order, the weight and share of the first choice, its why line
// and its note, and the candidates left out with their reasons.
func pickFixtureDump(t testing.TB) string {
	t.Helper()
	var b strings.Builder
	for _, fx := range pickFixtures() {
		fmt.Fprintf(&b, "== %s\n", fx.name)
		level := fx.level
		rotation := &WorkerRotation{}
		for i := 1; i <= fx.picks; i++ {
			order, leftOut, err := WorkerPickOrder(fx.candidates, level, fx.scores, nil, fx.quotas, nil, rotation, fx.in)
			if err != nil {
				t.Fatalf("%s pick %d: %v", fx.name, i, err)
			}
			first := order[0]
			fmt.Fprintf(&b, "pick %d: first=%s weight=%.6g share=%q quota=%q why=%q fallback=%t\n",
				i, dumpName(first.WorkerModelCandidate), first.Weight, first.ShareNote, first.QuotaNote, first.Why, first.Fallback)
			fmt.Fprintf(&b, "  note: %s\n", first.PickNote)
			names := make([]string, len(order))
			for j, c := range order {
				names[j] = dumpName(c.WorkerModelCandidate)
			}
			fmt.Fprintf(&b, "  order: %s\n", strings.Join(names, ", "))
			for _, lo := range leftOut {
				fmt.Fprintf(&b, "  left out: %s (%s)\n", lo.Name, lo.Reason)
			}
		}
	}
	return b.String()
}

func dumpName(c WorkerModelCandidate) string {
	if c.Name != "" {
		return c.Name
	}
	return c.Model
}

// The picks of the fixtures, as the picker made them before the pick rows were
// added. Only the section with no qualifying candidate changed on 2026-11-21,
// when the fallback draw became the inverse expected cost (owner yes). Every
// other section is the record from before that change, and a change to any of
// them, a pick, a weight, a share, a why line or a note, fails this test.
func TestPicksOnFixedFixturesAreUnchanged(t *testing.T) {
	want, err := os.ReadFile("testdata/pick_fixtures_before.txt")
	if err != nil {
		t.Fatal(err)
	}
	got := pickFixtureDump(t)
	if got != string(want) {
		t.Fatalf("the picks changed on the fixed fixtures; first difference:\n%s", firstDifference(string(want), got))
	}
}

// firstDifference shows the first line where two dumps part, with its
// neighbours, so a failure names the pick that changed.
func firstDifference(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			return fmt.Sprintf("line %d\nwant: %s\n got: %s", i+1, w, g)
		}
	}
	return "no difference found"
}
