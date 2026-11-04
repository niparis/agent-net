package protocol

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

const (
	commitA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	commitB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	commitM = "cccccccccccccccccccccccccccccccccccccccc"
)

func testChange() Change {
	return Change{
		ID: "ch_1", Repo: "demo", Branch: "agent-net/w_1", TargetBranch: "main", HeadCommit: commitA, Revision: 1,
		Origin: ChangeOrigin{Kind: OriginWorker, WorkerID: "w_1", SessionID: "s_author", Model: "deepseek/deepseek-v4", ModelFamily: "deepseek"},
		Caller: "s_caller", Difficulty: "normal", State: ChangeOpen, CreatedAt: 100,
	}
}

func pass(c Change, ref StepRef, at int64, fields map[string]string) Evidence {
	return Evidence{ChangeID: c.ID, Revision: c.Revision, StepRef: ref, Result: StepResultPass, Commit: c.HeadCommit, Fields: fields, By: "s_caller", At: at}
}

var (
	refCheck         = StepRef{Step: StepCheck}
	refReview        = StepRef{Step: StepReview}
	refTest          = StepRef{Step: StepTest}
	refMerge         = StepRef{Step: StepMerge}
	refDeploy        = StepRef{Step: StepDeploy}
	refApproveMerge  = StepRef{Step: StepApprove, For: StepMerge}
	refApproveDeploy = StepRef{Step: StepApprove, For: StepDeploy}
)

func TestStepNamesInOrder(t *testing.T) {
	want := []string{"check", "review", "test", "approve", "merge", "release", "deploy", "verify"}
	if got := StepNames(); !reflect.DeepEqual(got, want) {
		t.Fatalf("StepNames = %v, want %v", got, want)
	}
	if (StepRef{Step: StepApprove, For: StepDeploy}).String() != "approve for deploy" {
		t.Fatal("approve ref text")
	}
	r := Requirement{StepRef: refTest, Fields: map[string]string{FieldTestsAdded: "yes", FieldSuite: SuiteFull}}
	if got := r.String(); got != "test(suite=full, tests_added=yes)" {
		t.Fatalf("requirement text = %q", got)
	}
}

// A step serializes flat: step and for sit next to owner and the rules.
func TestPipelineStepJSONIsFlat(t *testing.T) {
	var s PipelineStep
	if err := json.Unmarshal([]byte(`{"step":"approve","for":"merge","owner":{"kind":"delegate"}}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.Step != StepApprove || s.For != StepMerge || s.Owner.Kind != OwnerDelegate {
		t.Fatalf("decoded %+v", s)
	}
	raw, _ := json.Marshal(PipelineStep{StepRef: refMerge, Owner: StepOwner{Kind: OwnerNode, Node: "merger"}, Requires: []Requirement{{StepRef: refTest, Fields: map[string]string{FieldSuite: SuiteFull}}}})
	if string(raw) != `{"step":"merge","owner":{"kind":"node","node":"merger"},"requires":[{"step":"test","fields":{"suite":"full"}}]}` {
		t.Fatalf("encoded %s", raw)
	}
}

func TestPipelineValidate(t *testing.T) {
	caller := StepOwner{Kind: OwnerCaller}
	step := func(ref StepRef, owner StepOwner, req ...Requirement) PipelineStep {
		return PipelineStep{StepRef: ref, Owner: owner, Requires: req}
	}
	after := func(s PipelineStep) PipelineStep { s.AfterMerge = true; return s }
	req := func(ref StepRef) Requirement { return Requirement{StepRef: ref} }
	cases := []struct {
		name  string
		steps []PipelineStep
		want  string
	}{
		{"empty", nil, "no steps"},
		{"unknown step", []PipelineStep{step(StepRef{Step: "lint"}, caller)}, `unknown step "lint"`},
		{"approve without for", []PipelineStep{step(StepRef{Step: StepApprove}, StepOwner{Kind: OwnerPerson})}, "approve needs for"},
		{"for on review", []PipelineStep{step(StepRef{Step: StepReview, For: StepMerge}, caller)}, "review takes no for"},
		{"duplicate", []PipelineStep{step(refCheck, caller), step(refCheck, caller)}, "check appears twice"},
		{"node without name", []PipelineStep{step(refReview, StepOwner{Kind: OwnerNode})}, "needs a node name"},
		{"caller with name", []PipelineStep{step(refReview, StepOwner{Kind: OwnerCaller, Node: "x"})}, "takes no node name"},
		{"unknown owner", []PipelineStep{step(refReview, StepOwner{Kind: "robot"})}, `unknown owner kind "robot"`},
		{"github review", []PipelineStep{step(refReview, StepOwner{Kind: OwnerGitHub})}, "github is only for test and merge"},
		{"delegate merge", []PipelineStep{step(refMerge, StepOwner{Kind: OwnerDelegate})}, "delegate is only for approve"},
		{"none test", []PipelineStep{step(refTest, StepOwner{Kind: OwnerNone})}, "none is only for approve"},
		{"requires unknown", []PipelineStep{step(refMerge, caller, req(refTest))}, "merge requires test, which is not earlier in the pipeline"},
		{"requires later", []PipelineStep{step(refMerge, caller, req(refTest)), step(refTest, caller)}, "merge requires test, which is not earlier in the pipeline"},
		{"requires itself", []PipelineStep{step(refMerge, caller, req(refMerge))}, "merge requires itself"},
		{"after merge without merge", []PipelineStep{after(step(refDeploy, caller))}, "deploy runs after merge, but the pipeline has no merge step"},
		{"merge after merge", []PipelineStep{after(step(refMerge, caller))}, "merge cannot run after merge"},
		{"step after merge not marked", []PipelineStep{step(refMerge, caller), step(refDeploy, caller)}, "deploy comes after merge, so it needs after_merge"},
		{"after merge before merge", []PipelineStep{after(step(refTest, caller)), step(refMerge, caller)}, "test has after_merge but comes before merge"},
		{"approve merge not required", []PipelineStep{step(refApproveMerge, StepOwner{Kind: OwnerPerson}), step(refMerge, caller)}, "merge must require approve for merge"},
		{"approve merge without merge", []PipelineStep{step(refApproveMerge, StepOwner{Kind: OwnerPerson})}, "approve for merge needs a merge step"},
		{"approve deploy without deploy", []PipelineStep{step(refApproveDeploy, StepOwner{Kind: OwnerPerson})}, "approve for deploy needs a deploy step"},
		{"deploy without approve", []PipelineStep{step(refApproveDeploy, StepOwner{Kind: OwnerPerson}), step(refDeploy, caller)}, "deploy must require approve for deploy"},
		{"bad independent", []PipelineStep{{StepRef: refReview, Owner: caller, Independent: "model"}}, "independent must be none, session or family"},
		{"bad on_fail", []PipelineStep{{StepRef: refReview, Owner: caller, OnFail: "drop"}}, "on_fail must be caller or author"},
		{"bad required_from", []PipelineStep{{StepRef: refReview, Owner: caller, RequiredFrom: "huge"}}, "required_from must be one of"},
		{"bad field name", []PipelineStep{step(refTest, caller), step(refMerge, caller, Requirement{StepRef: refTest, Fields: map[string]string{"Suite": "full"}})}, `field name "Suite"`},
		{"bad field value", []PipelineStep{step(refTest, caller), step(refMerge, caller, Requirement{StepRef: refTest, Fields: map[string]string{FieldSuite: "most"}})}, "suite must be changed or full"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Pipeline{Steps: tc.steps}.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate = %v, want an error with %q", err, tc.want)
			}
		})
	}
	if err := PipelineToday().Validate(); err != nil {
		t.Fatalf("today's pipeline: %v", err)
	}
}

// merge requires test(suite=full, tests_added=yes) and review(findings_open=0):
// evidence counts only on the exact head commit and current revision, with
// the right fields, and the latest result of a step wins.
func TestReadyNeedsEvidenceOnTheExactCommit(t *testing.T) {
	p, err := PipelineFromShorthand(TestsSuiteNewTestsReview, MergeFull, DeployBrief)
	if err != nil {
		t.Fatal(err)
	}
	c := testChange()
	review := func(at int64) Evidence {
		e := pass(c, refReview, at, map[string]string{FieldFindingsOpen: "0", FieldIndependent: IndependentFamily})
		return e
	}
	full := map[string]string{FieldSuite: SuiteFull, FieldTestsAdded: "yes"}
	good := []Evidence{pass(c, refCheck, 1, nil), review(2), pass(c, refTest, 3, full)}
	if ok, missing := p.Ready(refMerge, c, good); !ok {
		t.Fatalf("merge not ready: %v", missing)
	}

	older := pass(c, refTest, 3, full)
	older.Commit = commitB
	ok, missing := p.Ready(refMerge, c, []Evidence{good[0], good[1], older})
	if ok || !strings.Contains(strings.Join(missing, "; "), "test(suite=full, tests_added=yes): no pass on aaaaaaaaaaaa") {
		t.Fatalf("evidence on another commit counted: %v %v", ok, missing)
	}

	changed := pass(c, refTest, 3, map[string]string{FieldSuite: SuiteChanged, FieldTestsAdded: "yes"})
	ok, missing = p.Ready(refMerge, c, []Evidence{good[0], good[1], changed})
	if ok || !strings.Contains(strings.Join(missing, "; "), `suite is "changed", needs "full"`) {
		t.Fatalf("wrong suite counted: %v %v", ok, missing)
	}

	fail := good[2]
	fail.Result, fail.At = StepResultFail, 4
	ok, missing = p.Ready(refMerge, c, append(append([]Evidence{}, good...), fail))
	if ok || !strings.Contains(strings.Join(missing, "; "), "fail on aaaaaaaaaaaa") {
		t.Fatalf("a later fail did not win: %v %v", ok, missing)
	}
	again := good[2]
	again.At = 5
	if ok, missing := p.Ready(refMerge, c, append(append([]Evidence{}, good...), fail, again)); !ok {
		t.Fatalf("a pass after the fail did not win: %v", missing)
	}

	open := review(2)
	open.Fields[FieldFindingsOpen] = "2"
	if ok, _ := p.Ready(refMerge, c, []Evidence{good[0], open, good[2]}); ok {
		t.Fatal("merge ready with open findings")
	}
	if ok, missing := p.Ready(StepRef{Step: StepRelease}, c, good); ok || !strings.Contains(missing[0], "release is not in the pipeline") {
		t.Fatalf("release in a pipeline without it: %v %v", ok, missing)
	}
}

func TestNewHeadBumpsRevisionAndDropsOldEvidence(t *testing.T) {
	p := PipelineToday()
	c := testChange()
	ev := []Evidence{pass(c, refCheck, 1, nil), pass(c, refTest, 2, map[string]string{FieldSuite: SuiteFull})}
	if ok, missing := p.Ready(refMerge, c, ev); !ok {
		t.Fatalf("merge not ready on revision 1: %v", missing)
	}
	same, bumped, err := c.WithHead(commitA, 200)
	if err != nil || bumped || same.Revision != 1 {
		t.Fatalf("same head: %+v %v %v", same, bumped, err)
	}
	next, bumped, err := c.WithHead(commitB, 200)
	if err != nil || !bumped || next.Revision != 2 || next.HeadCommit != commitB || next.UpdatedAt != 200 {
		t.Fatalf("new head: %+v %v %v", next, bumped, err)
	}
	if ok, _ := p.Ready(refMerge, next, ev); ok {
		t.Fatal("evidence of revision 1 still counts on revision 2")
	}
	// Evidence that names the new commit but the old revision does not count.
	stale := ev[1]
	stale.Commit = commitB
	if ok, _ := p.Ready(refMerge, next, []Evidence{stale}); ok {
		t.Fatal("old revision counted")
	}
	if _, _, err := c.WithHead("abc", 200); err == nil {
		t.Fatal("short commit accepted")
	}
	c.State = ChangeMerged
	if _, _, err := c.WithHead(commitB, 200); err == nil || !strings.Contains(err.Error(), "merged") {
		t.Fatalf("new head on a merged change: %v", err)
	}
}

// Today's pipeline: check and test start at once; review is left out below
// difficulty hard; approve is none; merge waits for check and test; deploy
// waits for the merged commit.
func TestNextStepsInParallelThenMergeThenDeploy(t *testing.T) {
	p := PipelineToday()
	c := testChange()
	if got := p.NextSteps(c, nil); !reflect.DeepEqual(got, []StepRef{refCheck, refTest}) {
		t.Fatalf("first steps = %v", got)
	}
	ev := []Evidence{pass(c, refCheck, 1, nil)}
	if got := p.NextSteps(c, ev); !reflect.DeepEqual(got, []StepRef{refTest}) {
		t.Fatalf("after check = %v", got)
	}
	ev = append(ev, pass(c, refTest, 2, map[string]string{FieldSuite: SuiteFull}))
	if got := p.NextSteps(c, ev); !reflect.DeepEqual(got, []StepRef{refMerge}) {
		t.Fatalf("after test = %v", got)
	}
	merged := pass(c, refMerge, 3, map[string]string{FieldMergedCommit: commitM})
	ev = append(ev, merged)
	c.State, c.MergedCommit = ChangeMerged, commitM
	if got := p.NextSteps(c, ev); !reflect.DeepEqual(got, []StepRef{refDeploy}) {
		t.Fatalf("after merge = %v", got)
	}
	// Deploy runs on the merged commit, not on the branch head.
	onHead := pass(c, refDeploy, 4, nil)
	if got := p.NextSteps(c, append(ev, onHead)); !reflect.DeepEqual(got, []StepRef{refDeploy}) {
		t.Fatalf("deploy evidence on the head counted: %v", got)
	}
	onMerged := onHead
	onMerged.Commit = commitM
	if got := p.NextSteps(c, append(ev, onMerged)); len(got) != 0 {
		t.Fatalf("after deploy = %v", got)
	}
	status := p.Status(c, append(ev, onMerged))
	for _, s := range status {
		want := StepStatePassed
		if s.Step == StepReview || s.Step == StepApprove {
			want = StepStateSkipped
		}
		if s.State != want {
			t.Errorf("%s state = %s, want %s", s.StepRef, s.State, want)
		}
	}
	c.State = ChangeStopped
	if got := p.NextSteps(c, nil); len(got) != 0 {
		t.Fatalf("a stopped change has next steps: %v", got)
	}
}

// From difficulty hard, review is required and runs beside check and test;
// below it, a review that did run still counts ("review when present").
func TestReviewRequiredFromHard(t *testing.T) {
	p := PipelineToday()
	c := testChange()
	c.Difficulty = "hard"
	if got := p.NextSteps(c, nil); !reflect.DeepEqual(got, []StepRef{refCheck, refReview, refTest}) {
		t.Fatalf("hard first steps = %v", got)
	}
	ev := []Evidence{pass(c, refCheck, 1, nil), pass(c, refTest, 2, map[string]string{FieldSuite: SuiteFull})}
	if ok, missing := p.Ready(refMerge, c, ev); ok || !strings.Contains(strings.Join(missing, "; "), "review: no pass") {
		t.Fatalf("hard change merged without review: %v %v", ok, missing)
	}
	c.Difficulty = ""
	if ok, _ := p.Ready(refMerge, c, ev); ok {
		t.Fatal("unknown difficulty skipped the review")
	}
	c.Difficulty = "simple"
	if ok, missing := p.Ready(refMerge, c, ev); !ok {
		t.Fatalf("simple change waits for review: %v", missing)
	}
	failed := Evidence{ChangeID: c.ID, Revision: 1, StepRef: refReview, Result: StepResultFail, Commit: commitA, By: "s_r", At: 3}
	if ok, _ := p.Ready(refMerge, c, append(ev, failed)); ok {
		t.Fatal("a failed optional review did not hold the merge")
	}
}

// A pass counts for an independent step only when agent-net found it
// independent enough; a fail always counts.
func TestIndependentReview(t *testing.T) {
	p := PipelineToday()
	c := testChange()
	c.Difficulty = "hard"
	base := []Evidence{pass(c, refCheck, 1, nil), pass(c, refTest, 2, map[string]string{FieldSuite: SuiteFull})}
	same := pass(c, refReview, 3, map[string]string{FieldIndependent: IndependentSession})
	if ok, _ := p.Ready(refMerge, c, append(base, same)); ok {
		t.Fatal("a same-family review counted")
	}
	st := p.Status(c, append(base, same))
	if st[1].State != StepStateReady || !strings.Contains(st[1].Note, "needs independent=family") {
		t.Fatalf("review status = %+v", st[1])
	}
	other := pass(c, refReview, 4, map[string]string{FieldIndependent: IndependentFamily})
	if ok, missing := p.Ready(refMerge, c, append(base, same, other)); !ok {
		t.Fatalf("an other-family review did not count: %v", missing)
	}

	author := ChangeOrigin{Kind: OriginWorker, WorkerID: "w_1", SessionID: "s_a", ModelFamily: "deepseek"}
	for _, tc := range []struct{ by, family, want string }{
		{"s_a", "deepseek", IndependentNone},
		{"s_b", "deepseek", IndependentSession},
		{"s_b", "anthropic", IndependentFamily},
		{"s_b", "", IndependentSession},
		{EvidenceByOwner, "", IndependentFamily},
		{EvidenceByGitHub, "", IndependentFamily},
	} {
		if got := IndependenceOf(author, tc.by, tc.family); got != tc.want {
			t.Errorf("IndependenceOf(%s, %s) = %s, want %s", tc.by, tc.family, got, tc.want)
		}
	}
	if got := IndependenceOf(ChangeOrigin{Kind: OriginPerson, Person: "owner"}, "s_b", "anthropic"); got != IndependentFamily {
		t.Errorf("a model reviewing a person's work = %s", got)
	}
	if ModelFamily("claude", "claude-opus-5-5") != "anthropic" || ModelFamily("opencode", "deepseek/deepseek-v4") != "deepseek" {
		t.Error("ModelFamily is not the model's vendor")
	}
}

func TestApproveBeforeMerge(t *testing.T) {
	p, err := PipelineForMaturity(MaturityMature)
	if err != nil {
		t.Fatal(err)
	}
	approve, _ := p.Find(refApproveMerge)
	if approve.Owner.Kind != OwnerDelegate {
		t.Fatalf("mature approve owner = %+v", approve.Owner)
	}
	c := testChange()
	ev := []Evidence{pass(c, refCheck, 1, nil), pass(c, refTest, 2, map[string]string{FieldSuite: SuiteFull, FieldTestsAdded: "yes"})}
	if got := p.NextSteps(c, ev); !reflect.DeepEqual(got, []StepRef{refApproveMerge}) {
		t.Fatalf("next = %v", got)
	}
	if ok, _ := p.Ready(refMerge, c, ev); ok {
		t.Fatal("merge ready without approval")
	}
	ev = append(ev, Evidence{ChangeID: c.ID, Revision: 1, StepRef: refApproveMerge, Result: StepResultPass, Commit: commitA, By: EvidenceByOwner, At: 3})
	if ok, missing := p.Ready(refMerge, c, ev); !ok {
		t.Fatalf("merge not ready after approval: %v", missing)
	}
}

// Each maturity level's pipeline reads back as the step 6 settings the level
// gives today, and today's pipeline is the default level's.
func TestMaturityPipelinesMatchDefaults(t *testing.T) {
	for _, level := range []string{"", MaturityEarly, MaturityGrowing, MaturityMature, MaturityCritical} {
		p, err := PipelineForMaturity(level)
		if err != nil {
			t.Fatalf("%q: %v", level, err)
		}
		if err := p.Validate(); err != nil {
			t.Fatalf("%q: %v", level, err)
		}
		d := DefaultsForMaturity(level)
		tests, merge, deploy := p.Shorthand()
		if tests != d.TestsBeforeMerge || merge != d.MergeAuthority || deploy != d.DeployRule {
			t.Errorf("%q reads %s/%s/%s, defaults are %s/%s/%s", level, tests, merge, deploy, d.TestsBeforeMerge, d.MergeAuthority, d.DeployRule)
		}
	}
	growing, _ := PipelineForMaturity(MaturityGrowing)
	if !reflect.DeepEqual(PipelineToday(), growing) {
		t.Fatal("today's pipeline differs from the default level's")
	}
	for _, s := range PipelineToday().Steps {
		want := OwnerCaller
		if s.Step == StepApprove {
			want = OwnerNone
		}
		if s.Owner.Kind != want {
			t.Errorf("today's %s owner = %s, want %s", s.StepRef, s.Owner.Kind, want)
		}
	}
	// A fresh copy each time: changing one must not change the next.
	p := PipelineToday()
	p.Steps[0].Owner.Kind = OwnerPerson
	p.Steps[4].Requires[0].Fields = map[string]string{"x": "y"}
	if !reflect.DeepEqual(PipelineToday(), growing) {
		t.Fatal("PipelineToday shares memory between calls")
	}
}

func TestShorthandTable(t *testing.T) {
	reqs := func(p Pipeline) string {
		m, _ := p.Find(refMerge)
		var out []string
		for _, r := range m.Requires {
			out = append(out, r.String())
		}
		return strings.Join(out, ", ")
	}
	owner := func(p Pipeline, ref StepRef) string { s, _ := p.Find(ref); return s.Owner.Kind }
	cases := []struct {
		tests, merge, deploy string
		requires             string
		approveMerge         string
		approveDeploy        string
	}{
		{TestsOptional, MergeFull, DeployBrief, "check, review, approve for merge", OwnerNone, OwnerNone},
		{TestsSuite, MergeDelegated, DeployAsk, "check, review, test(suite=full), approve for merge", OwnerDelegate, OwnerDelegate},
		{TestsSuiteNewTests, MergeAsk, DeployOwner, "check, review, test(suite=full, tests_added=yes), approve for merge", OwnerPerson, OwnerPerson},
		{TestsSuiteNewTestsReview, MergeFull, DeployBrief, "check, review(findings_open=0), test(suite=full, tests_added=yes), approve for merge", OwnerNone, OwnerNone},
	}
	for _, tc := range cases {
		p, err := PipelineFromShorthand(tc.tests, tc.merge, tc.deploy)
		if err != nil {
			t.Fatal(err)
		}
		if got := reqs(p); got != tc.requires {
			t.Errorf("%s: merge requires %s, want %s", tc.tests, got, tc.requires)
		}
		if got := owner(p, refApproveMerge); got != tc.approveMerge {
			t.Errorf("%s: approve for merge owner %s, want %s", tc.merge, got, tc.approveMerge)
		}
		if got := owner(p, refApproveDeploy); got != tc.approveDeploy {
			t.Errorf("%s: approve for deploy owner %s, want %s", tc.deploy, got, tc.approveDeploy)
		}
		review, _ := p.Find(refReview)
		wantFrom := "hard"
		if tc.tests == TestsSuiteNewTestsReview {
			wantFrom = ""
		}
		if review.RequiredFrom != wantFrom || review.Independent != IndependentFamily {
			t.Errorf("%s: review %+v", tc.tests, review)
		}
	}
	for _, tests := range []string{TestsOptional, TestsSuite, TestsSuiteNewTests, TestsSuiteNewTestsReview} {
		for _, merge := range []string{MergeFull, MergeDelegated, MergeAsk} {
			for _, deploy := range []string{DeployBrief, DeployAsk, DeployOwner} {
				p, err := PipelineFromShorthand(tests, merge, deploy)
				if err != nil {
					t.Fatal(err)
				}
				if err := p.Validate(); err != nil {
					t.Fatalf("%s/%s/%s: %v", tests, merge, deploy, err)
				}
				gt, gm, gd := p.Shorthand()
				if gt != tests || gm != merge || gd != deploy {
					t.Errorf("%s/%s/%s reads back as %s/%s/%s", tests, merge, deploy, gt, gm, gd)
				}
			}
		}
	}
	for _, bad := range [][3]string{{"some", MergeFull, DeployBrief}, {TestsSuite, "maybe", DeployBrief}, {TestsSuite, MergeFull, "later"}} {
		if _, err := PipelineFromShorthand(bad[0], bad[1], bad[2]); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}

	// What a shorthand cannot say reads as custom.
	p := PipelineToday()
	p.Steps[4].Requires[2].Fields[FieldSuite] = SuiteChanged
	if tests, merge, deploy := p.Shorthand(); tests != ShorthandCustom || merge != MergeFull || deploy != DeployBrief {
		t.Errorf("changed suite reads %s/%s/%s", tests, merge, deploy)
	}
	p = PipelineToday()
	p.Steps[3].Owner = StepOwner{Kind: OwnerNode, Node: "architect"}
	if _, merge, _ := p.Shorthand(); merge != ShorthandCustom {
		t.Errorf("node approver reads %s", merge)
	}
	p = PipelineToday()
	p.Steps = p.Steps[:5]
	if _, _, deploy := p.Shorthand(); deploy != DeployBrief {
		t.Errorf("no deploy step reads %s", deploy)
	}
}

func TestReportFields(t *testing.T) {
	p, _ := PipelineFromShorthand(TestsSuiteNewTestsReview, MergeFull, DeployBrief)
	if got := p.ReportFields(refTest); !reflect.DeepEqual(got, []string{FieldSuite, FieldTestsAdded}) {
		t.Errorf("test report fields = %v", got)
	}
	if got := p.ReportFields(refReview); !reflect.DeepEqual(got, []string{FieldFindingsOpen}) {
		t.Errorf("review report fields = %v", got)
	}
	if got := p.ReportFields(refMerge); !reflect.DeepEqual(got, []string{FieldMergedCommit}) {
		t.Errorf("merge report fields = %v", got)
	}
	if got := p.ReportFields(refCheck); len(got) != 0 {
		t.Errorf("check report fields = %v", got)
	}
}

func TestAcceptsChecksTheHead(t *testing.T) {
	p := PipelineToday()
	c := testChange()
	ok := pass(c, refCheck, 1, nil)
	if err := p.Accepts(c, ok); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		edit func(*Change, *Evidence)
		want string
	}{
		{"old commit", func(_ *Change, e *Evidence) { e.Commit = commitB }, "is not the head of change ch_1 (head aaaaaaaaaaaa, revision 1)"},
		{"old revision", func(c *Change, e *Evidence) { c.Revision = 2 }, "revision 1 is not the current revision 2"},
		{"other change", func(_ *Change, e *Evidence) { e.ChangeID = "ch_2" }, "is for change ch_2"},
		{"not in pipeline", func(_ *Change, e *Evidence) { e.StepRef = StepRef{Step: StepRelease} }, "has no step release"},
		{"left out", func(_ *Change, e *Evidence) { e.StepRef = refApproveMerge }, "approve for merge is left out"},
		{"after merge too early", func(_ *Change, e *Evidence) { e.StepRef = refDeploy }, "deploy runs on the merged commit, and change ch_1 is not merged"},
		{"merge without commit", func(_ *Change, e *Evidence) { e.StepRef = refMerge }, "a merge pass needs merged_commit"},
		{"stopped", func(c *Change, _ *Evidence) { c.State = ChangeStopped }, "change ch_1 is stopped"},
		{"bad result", func(_ *Change, e *Evidence) { e.Result = "ok" }, "result must be pass, fail or needs_owner"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c2, e2 := c, ok
			tc.edit(&c2, &e2)
			if err := p.Accepts(c2, e2); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Accepts = %v, want %q", err, tc.want)
			}
		})
	}
	merged := c
	merged.State, merged.MergedCommit = ChangeMerged, commitM
	deploy := pass(merged, refDeploy, 5, nil)
	deploy.Commit = commitM
	if err := p.Accepts(merged, deploy); err != nil {
		t.Fatalf("deploy on the merged commit: %v", err)
	}
	deploy.Commit = commitA
	if err := p.Accepts(merged, deploy); err == nil || !strings.Contains(err.Error(), "is not the merged commit cccccccccccc") {
		t.Fatalf("deploy on the head: %v", err)
	}
	if err := p.Accepts(merged, pass(merged, refCheck, 6, nil)); err == nil || !strings.Contains(err.Error(), "change ch_1 is merged") {
		t.Fatalf("check after merge: %v", err)
	}
}

func TestValidateEvidenceAndChange(t *testing.T) {
	c := testChange()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*Change)
		want string
	}{
		{"id", func(c *Change) { c.ID = "x1" }, `change id must start with "ch_"`},
		{"repo", func(c *Change) { c.Repo = "" }, "repo is empty"},
		{"branch", func(c *Change) { c.Branch = "bad branch" }, "branch"},
		{"same as target", func(c *Change) { c.Branch = "main" }, "is the target branch"},
		{"commit", func(c *Change) { c.HeadCommit = "HEAD" }, "head_commit must be a full commit hash"},
		{"revision", func(c *Change) { c.Revision = 0 }, "revision must be 1 or more"},
		{"origin", func(c *Change) { c.Origin = ChangeOrigin{Kind: OriginWorker} }, "a worker origin needs worker_id"},
		{"origin kind", func(c *Change) { c.Origin.Kind = "bot" }, "origin kind must be worker, session or person"},
		{"state", func(c *Change) { c.State = "closed" }, "state must be open, merged, done or stopped"},
		{"difficulty", func(c *Change) { c.Difficulty = "epic" }, "difficulty must be one of"},
	} {
		c2 := c
		tc.edit(&c2)
		if err := c2.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: Validate = %v, want %q", tc.name, err, tc.want)
		}
	}

	e := pass(c, refTest, 1, map[string]string{FieldSuite: SuiteFull, FieldTestsAdded: "yes", FieldFindingsOpen: "3", FieldIndependent: IndependentNone, FieldRun: "go test ./..."})
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	many := map[string]string{}
	for i := 0; i <= MaxEvidenceFields; i++ {
		many["f"+strings.Repeat("x", i)] = "1"
	}
	for _, tc := range []struct {
		name string
		edit func(*Evidence)
		want string
	}{
		{"short commit", func(e *Evidence) { e.Commit = "aaaaaaa" }, "commit must be a full commit hash"},
		{"tests_added", func(e *Evidence) { e.Fields = map[string]string{FieldTestsAdded: "maybe"} }, "tests_added must be yes or no"},
		{"findings", func(e *Evidence) { e.Fields = map[string]string{FieldFindingsOpen: "-1"} }, "findings_open must be a count"},
		{"independent", func(e *Evidence) { e.Fields = map[string]string{FieldIndependent: "yes"} }, "independent must be none, session or family"},
		{"merged commit", func(e *Evidence) { e.Fields = map[string]string{FieldMergedCommit: "main"} }, "merged_commit must be a full commit hash"},
		{"too many", func(e *Evidence) { e.Fields = many }, "at most 16 fields"},
		{"long value", func(e *Evidence) {
			e.Fields = map[string]string{FieldRun: strings.Repeat("x", MaxEvidenceFieldBytes+1)}
		}, "run is longer than 200 bytes"},
		{"newline value", func(e *Evidence) { e.Fields = map[string]string{FieldRun: "a\nb"} }, "run holds a control character"},
		{"note", func(e *Evidence) { e.Note = strings.Repeat("x", MaxEvidenceNoteBytes+1) }, "note is longer than"},
		{"no by", func(e *Evidence) { e.By = "" }, "by is empty"},
		{"revision", func(e *Evidence) { e.Revision = 0 }, "revision must be 1 or more"},
	} {
		e2 := e
		tc.edit(&e2)
		if err := e2.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: Validate = %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestDeliveryFrameRequests(t *testing.T) {
	submit := ChangeSubmitRequest{SessionID: "s_1", Repo: "demo", Branch: "feature/x", TargetBranch: "main", HeadCommit: commitA, Origin: ChangeOrigin{Kind: OriginSession, SessionID: "s_1"}, Difficulty: "hard", Title: "Fix the login"}
	if err := submit.Validate(); err != nil {
		t.Fatal(err)
	}
	report := StepReportRequest{SessionID: "s_1", ChangeID: "ch_1", StepRef: refTest, Result: StepResultPass, Commit: commitA, Fields: map[string]string{FieldSuite: SuiteFull}, DoneBy: "s_w", Note: "ok"}
	if err := report.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := report
	bad.Fields = map[string]string{FieldIndependent: IndependentFamily}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "independent is set by agent-net") {
		t.Fatalf("reporter set independent: %v", err)
	}
	bad = report
	bad.SessionID = ""
	if err := bad.Validate(); err == nil {
		t.Fatal("report without a session accepted")
	}
	badSubmit := submit
	badSubmit.HeadCommit = ""
	if err := badSubmit.Validate(); err == nil {
		t.Fatal("submit without a commit accepted")
	}

	view := ChangeView{Change: testChange(), Steps: PipelineToday().Status(testChange(), nil)}
	frames := map[string]any{
		FrameChangeSubmit: submit,
		FrameChangeGet:    ChangeGetRequest{ChangeID: "ch_1"},
		FrameChangeList:   ChangeListRequest{Repo: "demo", State: ChangeListActive, Caller: "s_1", Limit: 10, After: "ch_0"},
		FrameStepReport:   report,
		FrameStepTask:     StepTask{TaskID: "st_1", SessionID: "s_owner", Change: testChange(), StepRef: refTest, Commit: commitA, DiffStat: "1 file", Evidence: []Evidence{pass(testChange(), refCheck, 1, nil)}, ReportFields: []string{FieldSuite}},
	}
	for name, params := range frames {
		raw, err := EncodeRequest(name, "7", params)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		var m map[string]json.RawMessage
		_ = json.Unmarshal(raw, &m)
		if string(m["type"]) != `"`+name+`"` || string(m["id"]) != `"7"` {
			t.Errorf("%s: frame fields lost: %s", name, raw)
		}
	}
	for _, v := range []any{ChangeSubmitResult{Change: view}, ChangeListResult{Changes: []ChangeView{view}}, StepReportResult{Change: view}} {
		if _, err := json.Marshal(v); err != nil {
			t.Fatal(err)
		}
	}
}

var snakeCase = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Every JSON name of the contract is snake_case, and no frame request has a
// top-level parameter named id or type.
func TestDeliveryJSONNames(t *testing.T) {
	types := []any{Change{}, ChangeOrigin{}, Revision{}, Evidence{}, StepRef{}, StepOwner{}, Requirement{}, PipelineStep{}, Pipeline{}, StepStatus{}, ChangeView{},
		ChangeSubmitRequest{}, ChangeSubmitResult{}, ChangeGetRequest{}, ChangeListRequest{}, ChangeListResult{}, StepReportRequest{}, StepReportResult{}, StepTask{}}
	frames := map[reflect.Type]bool{reflect.TypeOf(ChangeSubmitRequest{}): true, reflect.TypeOf(ChangeGetRequest{}): true, reflect.TypeOf(ChangeListRequest{}): true, reflect.TypeOf(StepReportRequest{}): true, reflect.TypeOf(StepTask{}): true}
	var walk func(reflect.Type, bool)
	walk = func(rt reflect.Type, top bool) {
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if f.Anonymous {
				walk(f.Type, top)
				continue
			}
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if !snakeCase.MatchString(name) {
				t.Errorf("%s.%s has JSON name %q", rt.Name(), f.Name, name)
			}
			if top && (name == "id" || name == "type") {
				t.Errorf("frame %s has a parameter named %q", rt.Name(), name)
			}
		}
	}
	for _, v := range types {
		rt := reflect.TypeOf(v)
		walk(rt, frames[rt])
	}
}
