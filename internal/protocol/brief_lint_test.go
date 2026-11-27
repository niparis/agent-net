package protocol

import (
	"reflect"
	"testing"
)

// realBrief is a structured fix as a caller writes it: a problem with who
// asked, evidence, outcomes the worker can check, a hypothesis and a scope.
func realBrief() WorkerStartRequest {
	return WorkerStartRequest{
		Project:    "shop",
		Problem:    `The owner asked: "the parser crashes on an empty file" (message m_86b8d8cd77f796f0).`,
		Evidence:   []string{"The crash is in internal/parser/parse.go near line 88.", "An empty file reaches the loop that reads one byte too far."},
		Done:       []string{"A test with an empty input fails first and passes after the fix.", "Only internal/parser changes."},
		Hypothesis: "The loop reads one byte past the end of the input.",
		Scope:      "Do not touch the lexer. Tests use temp dirs and fakes only.",
	}
}

// lintChecks returns the check names of the flags, in order.
func lintChecks(flags []BriefFlag) []string {
	var out []string
	for _, f := range flags {
		out = append(out, f.Check)
	}
	return out
}

// wantChecks fails unless the flags name exactly the wanted checks.
func wantChecks(t *testing.T, flags []BriefFlag, want ...string) {
	t.Helper()
	if len(want) == 0 {
		want = nil
	}
	if got := lintChecks(flags); !reflect.DeepEqual(got, want) {
		t.Fatalf("flags = %v, want checks %v", flags, want)
	}
}

func TestBriefLintLeavesANormalBriefAlone(t *testing.T) {
	facts := BriefLintFacts{Commits: map[string]bool{"3fa9c1e2b7": true}, Sandbox: "bubblewrap"}
	wantChecks(t, LintBrief(realBrief(), facts))
	wantChecks(t, LintBrief(realBrief(), BriefLintFacts{}))
}

func TestBriefLintFlagsPathsOutsideTheWorktree(t *testing.T) {
	p := realBrief()
	p.Scope = "Read /home/nicolas/code/niparis/a2a-ocs/AGENTS.md first. Do not touch the lexer."
	wantChecks(t, LintBrief(p, BriefLintFacts{}), BriefCheckPath)

	outside := realBrief()
	outside.Hypothesis = "Compare with ../shop-old/parser.go and ~/code/niparis/PLAN.md."
	wantChecks(t, LintBrief(outside, BriefLintFacts{}), BriefCheckPath, BriefCheckPath)

	inside := realBrief()
	inside.Evidence = []string{"Read AGENTS.md and docs/archive/brief.md first.", "The URL https://example.com/a/b is fine.", "The system tool /usr/bin/git is fine."}
	wantChecks(t, LintBrief(inside, BriefLintFacts{}))
}

func TestBriefCommitIDsNamesOnlyCommitLines(t *testing.T) {
	text := "The bug first showed at commit 3fa9c1e2b7.\nToken 0123abcd9 is only a value."
	if got, want := BriefCommitIDs(text), []string{"3fa9c1e2b7"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("BriefCommitIDs = %v, want %v", got, want)
	}
}

func TestBriefLintFlagsACommitThatDoesNotResolve(t *testing.T) {
	p := realBrief()
	p.Evidence = []string{"The bug first showed at commit 3fa9c1e2b7."}
	wantChecks(t, LintBrief(p, BriefLintFacts{Commits: map[string]bool{"3fa9c1e2b7": false}}), BriefCheckCommit)
	wantChecks(t, LintBrief(p, BriefLintFacts{Commits: map[string]bool{"3fa9c1e2b7": true}}))
	// A machine that did not say whether the commit resolves gives no flag.
	wantChecks(t, LintBrief(p, BriefLintFacts{}))
}

func TestBriefLintFlagsContradictoryDirectives(t *testing.T) {
	race := realBrief()
	race.Done = []string{"Run go test -race on the package.", "Only internal/parser changes."}
	race.Scope = "Do not use -race here, the suite is slow."
	wantChecks(t, LintBrief(race, BriefLintFacts{}), BriefCheckContradiction)

	merge := realBrief()
	merge.Done = []string{"Merge the change to main when it is green."}
	merge.Scope = "Do not merge anything else."
	wantChecks(t, LintBrief(merge, BriefLintFacts{}), BriefCheckContradiction)

	suite := realBrief()
	suite.Done = []string{"Run the full test suite before hand-back."}
	suite.Hypothesis = "Run only the changed tests with scripts/test-changed.sh."
	wantChecks(t, LintBrief(suite, BriefLintFacts{}), BriefCheckContradiction)

	// One side alone is a choice, not a contradiction.
	alone := realBrief()
	alone.Scope = "Do not use -race and do not merge."
	wantChecks(t, LintBrief(alone, BriefLintFacts{}))
}

func TestBriefLintFlagsBrowserChecksInASandbox(t *testing.T) {
	p := realBrief()
	p.Done = []string{"Screenshot the settings page at 1280x800 and attach it."}
	wantChecks(t, LintBrief(p, BriefLintFacts{Sandbox: "bubblewrap"}), BriefCheckSandbox)
	// Without a sandbox fact nothing is known, so nothing is flagged.
	wantChecks(t, LintBrief(p, BriefLintFacts{}))

	// A text frame of a terminal screen needs no browser.
	frames := realBrief()
	frames.Done = []string{"The text frame at 120x34 shows the new row."}
	wantChecks(t, LintBrief(frames, BriefLintFacts{Sandbox: "bubblewrap"}))
}

// The false flags seen on real starts: each brief below is fine and must give
// no flag. The last row of each group keeps a real flag.
func TestBriefLintIgnoresWhatIsNotAPathToEdit(t *testing.T) {
	for name, scope := range map[string]string{
		"time":            "The error was logged at 2026-11-27 ~11:30Z.",
		"approx line":     "See internal/parser/parse.go ~550 for the loop.",
		"approx range":    "See internal/parser/parse.go ~342-349 for the loop.",
		"home cache fact": "The build cache is ~/.cache and is already full.",
		"home folder":     "Agy keeps state in ~/.gemini and we leave it alone.",
	} {
		t.Run(name, func(t *testing.T) {
			p := realBrief()
			p.Scope = scope
			wantChecks(t, LintBrief(p, BriefLintFacts{}))
		})
	}
	p := realBrief()
	p.Scope = "Fix ~/code/niparis/PLAN.md and ../shop-old/internal/parser."
	wantChecks(t, LintBrief(p, BriefLintFacts{}), BriefCheckPath, BriefCheckPath)
}

func TestBriefLintIgnoresASentenceThatNamesBothCases(t *testing.T) {
	p := realBrief()
	p.Done = []string{"Run go test -race on the package (about 5 minutes with -race; about 2 minutes without -race)."}
	wantChecks(t, LintBrief(p, BriefLintFacts{}))

	// A real contradiction in separate sentences is still flagged.
	p.Done = []string{"Run go test -race on the package. Do not use -race, it is slow."}
	wantChecks(t, LintBrief(p, BriefLintFacts{}), BriefCheckContradiction)
}
