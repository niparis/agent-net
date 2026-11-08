package protocol

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

const (
	commitA    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	commitB    = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	commitM    = "cccccccccccccccccccccccccccccccccccccccc"
	commitBase = "dddddddddddddddddddddddddddddddddddddddd"
)

func testChange() Change {
	return Change{
		ID: "ch_1", Project: "demo", Machine: "n_1", Branch: "agent-net/w_1", TargetBranch: "main", BaseCommit: commitBase, HeadCommit: commitA, Revision: 1,
		Origin: ChangeOrigin{Kind: OriginWorker, WorkerID: "w_1", SessionID: "s_author", Model: "deepseek/deepseek-v4-pro", ModelFamily: "deepseek"},
		Caller: "s_caller", Difficulty: "normal", State: ChangeOpen, CreatedAt: 100,
	}
}

// pass is a pass on the commit the step runs on: the merged commit for a step
// after merge, the head otherwise.
func pass(c Change, ref StepRef, at int64, fields map[string]string) Evidence {
	commit := c.HeadCommit
	if ref.AfterMerge {
		commit = c.MergedCommit
	}
	return Evidence{ChangeID: c.ID, Revision: c.Revision, StepRef: ref, Result: StepResultPass, Commit: commit, Fields: fields, By: "s_caller", At: at}
}

var (
	refAccept        = StepRef{Step: StepAccept}
	refReview        = StepRef{Step: StepReview}
	refTest          = StepRef{Step: StepTest}
	refTestAfter     = StepRef{Step: StepTest, AfterMerge: true}
	refMerge         = StepRef{Step: StepMerge}
	refRelease       = StepRef{Step: StepRelease, AfterMerge: true}
	refVerify        = StepRef{Step: StepVerify, AfterMerge: true}
	refDeploy        = StepRef{Step: StepDeploy, AfterMerge: true}
	refApproveMerge  = StepRef{Step: StepApprove, For: StepMerge}
	refApproveDeploy = StepRef{Step: StepApprove, For: StepDeploy, AfterMerge: true}
)

func TestStepNamesInOrder(t *testing.T) {
	want := []string{"accept", "review", "test", "approve", "merge", "release", "deploy", "verify"}
	if got := StepNames(); !reflect.DeepEqual(got, want) {
		t.Fatalf("StepNames = %v, want %v", got, want)
	}
	if ValidStep("check") {
		t.Fatal("check is the idle check and an Inbox kind, not a step")
	}
	if got := refApproveDeploy.String(); got != "approve for deploy after merge" {
		t.Fatalf("approve ref text = %q", got)
	}
	r := Requirement{StepRef: refTest, Fields: map[string]string{FieldTestsAdded: "yes", FieldSuite: FieldAny}}
	if got := r.String(); got != "test(suite=*, tests_added=yes)" {
		t.Fatalf("requirement text = %q", got)
	}
	r = Requirement{StepRef: refTestAfter, Fields: map[string]string{FieldSuite: SuiteFull}}
	if got := r.String(); got != "test(suite=full) after merge" {
		t.Fatalf("after-merge requirement text = %q", got)
	}
}

// A step serializes flat: step, for and after_merge sit next to owner and the
// rules.
func TestPipelineStepJSONIsFlat(t *testing.T) {
	var s PipelineStep
	if err := json.Unmarshal([]byte(`{"step":"test","after_merge":true,"owner":{"kind":"github"}}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.StepRef != refTestAfter || s.Owner.Kind != OwnerGitHub {
		t.Fatalf("decoded %+v", s)
	}
	raw, _ := json.Marshal(PipelineStep{StepRef: refMerge, Owner: StepOwner{Kind: OwnerNode, Node: "merger"}, Requires: []Requirement{{StepRef: refTest, Fields: map[string]string{FieldSuite: FieldAny}}}})
	if string(raw) != `{"step":"merge","owner":{"kind":"node","node":"merger"},"requires":[{"step":"test","fields":{"suite":"*"}}]}` {
		t.Fatalf("encoded %s", raw)
	}
}

func TestPipelineValidate(t *testing.T) {
	caller := StepOwner{Kind: OwnerCaller}
	step := func(ref StepRef, owner StepOwner, req ...Requirement) PipelineStep {
		return PipelineStep{StepRef: ref, Owner: owner, Requires: req}
	}
	req := func(ref StepRef) Requirement { return Requirement{StepRef: ref} }
	person := StepOwner{Kind: OwnerPerson}
	cases := []struct {
		name  string
		steps []PipelineStep
		want  string
	}{
		{"empty", nil, "no steps"},
		{"unknown step", []PipelineStep{step(StepRef{Step: "check"}, caller)}, `unknown step "check"`},
		{"approve without for", []PipelineStep{step(StepRef{Step: StepApprove}, person)}, "approve needs for"},
		{"for on review", []PipelineStep{step(StepRef{Step: StepReview, For: StepMerge}, caller)}, "review takes no for"},
		{"duplicate", []PipelineStep{step(refAccept, caller), step(refAccept, caller)}, "accept appears twice"},
		{"approve twice", []PipelineStep{step(StepRef{Step: StepApprove, For: StepDeploy}, person), step(refMerge, caller), step(refApproveDeploy, person), step(refDeploy, caller, req(refApproveDeploy))}, "approve for deploy appears twice"},
		{"node without name", []PipelineStep{step(refReview, StepOwner{Kind: OwnerNode})}, "needs a node name"},
		{"caller with name", []PipelineStep{step(refReview, StepOwner{Kind: OwnerCaller, Node: "x"})}, "takes no node name"},
		{"unknown owner", []PipelineStep{step(refReview, StepOwner{Kind: "robot"})}, `unknown owner kind "robot"`},
		{"github review", []PipelineStep{step(refReview, StepOwner{Kind: OwnerGitHub})}, "github is only for test and merge"},
		{"delegate merge", []PipelineStep{step(refMerge, StepOwner{Kind: OwnerDelegate})}, "delegate is only for approve"},
		{"none test", []PipelineStep{step(refTest, StepOwner{Kind: OwnerNone})}, "none is only for approve"},
		{"requires unknown", []PipelineStep{step(refMerge, caller, req(refTest))}, "merge requires test, which is not earlier in the pipeline"},
		{"requires later", []PipelineStep{step(refMerge, caller, req(refTest)), step(refTest, caller)}, "merge requires test, which is not earlier in the pipeline"},
		{"requires the other test", []PipelineStep{step(refTest, caller), step(refMerge, caller), step(refRelease, caller, req(refTestAfter))}, "release after merge requires test after merge, which is not earlier"},
		{"requires itself", []PipelineStep{step(refMerge, caller, req(refMerge))}, "merge requires itself"},
		{"after merge without merge", []PipelineStep{step(refDeploy, caller)}, "deploy after merge: the pipeline has no merge step"},
		{"merge after merge", []PipelineStep{step(StepRef{Step: StepMerge, AfterMerge: true}, caller)}, "merge cannot run after merge"},
		{"step after merge not marked", []PipelineStep{step(refMerge, caller), step(StepRef{Step: StepDeploy}, caller)}, "deploy comes after merge, so it needs after_merge"},
		{"after merge before merge", []PipelineStep{step(refTestAfter, caller), step(refMerge, caller)}, "test after merge is listed before merge"},
		{"approve merge not required", []PipelineStep{step(refApproveMerge, person), step(refMerge, caller)}, "merge must require approve for merge"},
		{"approve merge without merge", []PipelineStep{step(refApproveMerge, person)}, "approve for merge needs a merge step"},
		{"approve deploy without deploy", []PipelineStep{step(StepRef{Step: StepApprove, For: StepDeploy}, person)}, "approve for deploy needs a deploy step"},
		{"deploy without approve", []PipelineStep{step(StepRef{Step: StepApprove, For: StepDeploy}, person), step(StepRef{Step: StepDeploy}, caller)}, "deploy must require approve for deploy"},
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
	// The same step may run once before merge and once after it.
	twice := Pipeline{Steps: []PipelineStep{step(refTest, caller), step(refMerge, caller, req(refTest)), step(refTestAfter, caller), step(refRelease, caller, req(refTestAfter))}}
	if err := twice.Validate(); err != nil {
		t.Fatalf("test before and after merge: %v", err)
	}
	if err := PipelineToday().Validate(); err != nil {
		t.Fatalf("today's pipeline: %v", err)
	}
}

// merge requires test(suite=*, tests_added=yes) and review(findings_open=0): evidence
// counts only on the exact head commit and current revision, with the right
// fields, and the latest result of a step wins.
func TestReadyNeedsEvidenceOnTheExactCommit(t *testing.T) {
	p, err := PipelineFromShorthand(TestsSuiteNewTestsReview, MergeFull, DeployBrief)
	if err != nil {
		t.Fatal(err)
	}
	c := testChange()
	review := func(at int64) Evidence {
		return pass(c, refReview, at, map[string]string{FieldFindingsOpen: "0", FieldIndependent: IndependentFamily})
	}
	added := map[string]string{FieldSuite: SuiteChanged, FieldTestsAdded: FieldYes}
	good := []Evidence{pass(c, refAccept, 1, nil), review(2), pass(c, refTest, 3, added)}
	if ok, missing := p.Ready(refMerge, c, good); !ok {
		t.Fatalf("merge not ready: %v", missing)
	}

	older := pass(c, refTest, 3, added)
	older.Commit = commitB
	ok, missing := p.Ready(refMerge, c, []Evidence{good[0], good[1], older})
	if ok || !strings.Contains(strings.Join(missing, "; "), "test(suite=*, tests_added=yes): no pass on aaaaaaaaaaaa") {
		t.Fatalf("evidence on another commit counted: %v %v", ok, missing)
	}

	none := pass(c, refTest, 3, map[string]string{FieldSuite: SuiteFull, FieldTestsAdded: FieldNo})
	ok, missing = p.Ready(refMerge, c, []Evidence{good[0], good[1], none})
	if ok || !strings.Contains(strings.Join(missing, "; "), `tests_added is "no", needs "yes"`) {
		t.Fatalf("tests_added=no counted: %v %v", ok, missing)
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
	if ok, missing := p.Ready(refRelease, c, good); ok || !strings.Contains(missing[0], "release after merge is not in the pipeline") {
		t.Fatalf("release in a pipeline without it: %v %v", ok, missing)
	}
}

// suite=* holds for any suite the test step names, but not for none.
func TestAnySuite(t *testing.T) {
	p, _ := PipelineFromShorthand(TestsSuite, MergeFull, DeployBrief)
	c := testChange()
	acc := pass(c, refAccept, 1, nil)
	if ok, missing := p.Ready(refMerge, c, []Evidence{acc, pass(c, refTest, 2, nil)}); ok || !strings.Contains(strings.Join(missing, "; "), "suite is not set, needs any value") {
		t.Fatalf("a test without a suite counted: %v %v", ok, missing)
	}
	for _, suite := range []string{SuiteChanged, SuiteFull} {
		if ok, missing := p.Ready(refMerge, c, []Evidence{acc, pass(c, refTest, 2, map[string]string{FieldSuite: suite})}); !ok {
			t.Fatalf("suite %s did not count: %v", suite, missing)
		}
	}
	e := pass(c, refTest, 2, map[string]string{"lint": FieldAny})
	if err := e.Validate(); err == nil || !strings.Contains(err.Error(), "* is for requirements") {
		t.Fatalf("evidence with * accepted: %v", err)
	}
}

func TestNewHeadBumpsRevisionAndDropsOldEvidence(t *testing.T) {
	p := PipelineToday()
	c := testChange()
	ev := []Evidence{pass(c, refAccept, 1, nil), pass(c, refTest, 2, map[string]string{FieldSuite: SuiteChanged})}
	if ok, missing := p.Ready(refMerge, c, ev); !ok {
		t.Fatalf("merge not ready on revision 1: %v", missing)
	}
	same, bumped, err := c.WithHead(c.Branch, commitA, false, 200)
	if err != nil || bumped || !reflect.DeepEqual(same, c) {
		t.Fatalf("same head: %+v %v %v", same, bumped, err)
	}
	// continue moves the work to a new branch; the same commit is the same
	// work, so the evidence still counts.
	moved, bumped, err := c.WithHead("agent-net/w_2", commitA, false, 200)
	if err != nil || bumped || moved.Branch != "agent-net/w_2" || moved.Revision != 1 {
		t.Fatalf("moved branch: %+v %v %v", moved, bumped, err)
	}
	if ok, _ := p.Ready(refMerge, moved, ev); !ok {
		t.Fatal("evidence lost when only the branch moved")
	}
	next, bumped, err := c.WithHead("agent-net/w_2", commitB, true, 300)
	if err != nil || !bumped || next.Revision != 2 || next.HeadCommit != commitB || next.Branch != "agent-net/w_2" || !next.Uncommitted || next.UpdatedAt != 300 {
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
	if _, _, err := c.WithHead(c.Branch, "abc", false, 200); err == nil {
		t.Fatal("short commit accepted")
	}
	if _, _, err := c.WithHead("bad branch", commitB, false, 200); err == nil {
		t.Fatal("bad branch accepted")
	}
	c.State = ChangeMerged
	if _, _, err := c.WithHead(c.Branch, commitB, false, 200); err == nil || !strings.Contains(err.Error(), "merged") {
		t.Fatalf("new head on a merged change: %v", err)
	}
}

// Today's pipeline: test follows accept; review is left out below
// difficulty hard; approve is none; merge waits for accept and test; after
// merge CI tests the merged commit, then release, then verify.
func TestTodayRunsInOrder(t *testing.T) {
	p := PipelineToday()
	c := testChange()
	if got := p.NextSteps(c, nil); !reflect.DeepEqual(got, []StepRef{refAccept}) {
		t.Fatalf("first steps = %v", got)
	}
	ev := []Evidence{pass(c, refAccept, 1, nil)}
	if got := p.NextSteps(c, ev); !reflect.DeepEqual(got, []StepRef{refTest}) {
		t.Fatalf("after accept = %v", got)
	}
	ev = append(ev, pass(c, refTest, 2, map[string]string{FieldSuite: SuiteChanged}))
	if got := p.NextSteps(c, ev); !reflect.DeepEqual(got, []StepRef{refMerge}) {
		t.Fatalf("after test = %v", got)
	}
	ev = append(ev, pass(c, refMerge, 3, map[string]string{FieldMergedCommit: commitM}))
	c.State, c.MergedCommit = ChangeMerged, commitM
	if got := p.NextSteps(c, ev); !reflect.DeepEqual(got, []StepRef{refTestAfter}) {
		t.Fatalf("after merge = %v", got)
	}
	// The CI test counts on the merged commit, not on the branch head.
	onHead := pass(c, refTestAfter, 4, map[string]string{FieldSuite: SuiteFull})
	onHead.Commit = commitA
	if got := p.NextSteps(c, append(ev, onHead)); !reflect.DeepEqual(got, []StepRef{refTestAfter}) {
		t.Fatalf("CI evidence on the head counted: %v", got)
	}
	changed := pass(c, refTestAfter, 4, map[string]string{FieldSuite: SuiteChanged})
	if ok, missing := p.Ready(refRelease, c, append(ev, changed)); ok || !strings.Contains(strings.Join(missing, "; "), `test(suite=full) after merge: suite is "changed", needs "full"`) {
		t.Fatalf("release after a partial CI run: %v %v", ok, missing)
	}
	ev = append(ev, pass(c, refTestAfter, 4, map[string]string{FieldSuite: SuiteFull}))
	if got := p.NextSteps(c, ev); !reflect.DeepEqual(got, []StepRef{refRelease}) {
		t.Fatalf("after CI = %v", got)
	}
	ev = append(ev, pass(c, refRelease, 5, map[string]string{FieldTag: "v1.2.3"}))
	if got := p.NextSteps(c, ev); !reflect.DeepEqual(got, []StepRef{refVerify}) {
		t.Fatalf("after release = %v", got)
	}
	ev = append(ev, pass(c, refVerify, 6, nil))
	if got := p.NextSteps(c, ev); len(got) != 0 {
		t.Fatalf("after verify = %v", got)
	}
	for _, s := range p.Status(c, ev) {
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

// A step owner may close a change without merge: nothing more starts.
func TestCloseStopsTheChange(t *testing.T) {
	p := PipelineToday()
	c := testChange()
	closed := pass(c, refAccept, 1, nil)
	closed.Result = StepResultClose
	if err := p.Accepts(c, closed); err != nil {
		t.Fatal(err)
	}
	if got := p.NextSteps(c, []Evidence{closed}); len(got) != 0 {
		t.Fatalf("a closed change has next steps: %v", got)
	}
	if st := p.Status(c, []Evidence{closed}); st[0].State != StepStateClosed {
		t.Fatalf("accept state = %s", st[0].State)
	}
}

// From difficulty hard, review is required and runs beside accept;
// below it, a review that did run still counts ("review when present"). No
// label counts as normal.
func TestReviewRequiredFromHard(t *testing.T) {
	p := PipelineToday()
	c := testChange()
	c.Difficulty = "hard"
	if got := p.NextSteps(c, nil); !reflect.DeepEqual(got, []StepRef{refAccept, refReview}) {
		t.Fatalf("hard first steps = %v", got)
	}
	ev := []Evidence{pass(c, refAccept, 1, nil), pass(c, refTest, 2, map[string]string{FieldSuite: SuiteChanged})}
	if ok, missing := p.Ready(refMerge, c, ev); ok || !strings.Contains(strings.Join(missing, "; "), "review: no pass") {
		t.Fatalf("hard change merged without review: %v %v", ok, missing)
	}
	c.Difficulty = "very_complex"
	if ok, _ := p.Ready(refMerge, c, ev); ok {
		t.Fatal("very_complex change merged without review")
	}
	for _, d := range []string{"", "simple", "normal"} {
		c.Difficulty = d
		if ok, missing := p.Ready(refMerge, c, ev); !ok {
			t.Fatalf("difficulty %q waits for review: %v", d, missing)
		}
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
	for i := range p.Steps {
		if p.Steps[i].Step == StepReview {
			p.Steps[i].Independent = IndependentFamily
		}
	}
	c := testChange()
	c.Difficulty = "hard"
	base := []Evidence{pass(c, refAccept, 1, nil), pass(c, refTest, 2, map[string]string{FieldSuite: SuiteChanged})}
	same := pass(c, refReview, 3, map[string]string{FieldIndependent: IndependentSession})
	if ok, _ := p.Ready(refMerge, c, append(base, same)); ok {
		t.Fatal("a same-family review counted")
	}
	st := p.Status(c, append(base, same))
	if st[2].StepRef != refReview || st[2].State != StepStateReady || !strings.Contains(st[2].Note, "needs independent=family") {
		t.Fatalf("review status = %+v", st[2])
	}
	other := pass(c, refReview, 4, map[string]string{FieldIndependent: IndependentFamily})
	if ok, missing := p.Ready(refMerge, c, append(base, same, other)); !ok {
		t.Fatalf("an other-family review did not count: %v", missing)
	}

	author := ChangeOrigin{Kind: OriginWorker, WorkerID: "w_1", SessionID: "s_a", ModelFamily: "anthropic"}
	for _, tc := range []struct{ by, family, want string }{
		{"s_a", "anthropic", IndependentNone},
		{"s_b", "anthropic", IndependentSession},
		{"s_b", "deepseek", IndependentFamily},
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
}

// With independence none, the author's own pass counts.
func TestReviewNoneCountsTheAuthor(t *testing.T) {
	p := PipelineToday()
	c := testChange()
	c.Difficulty = "hard"
	base := []Evidence{pass(c, refAccept, 1, nil), pass(c, refTest, 2, map[string]string{FieldSuite: SuiteChanged})}
	own := pass(c, refReview, 3, map[string]string{FieldIndependent: IndependentNone})
	ev := append(base, own)
	if ok, missing := p.Ready(refMerge, c, ev); !ok {
		t.Fatalf("the author's own review did not count: %v", missing)
	}
	for _, st := range p.Status(c, ev) {
		if st.Step == StepReview && (st.State != StepStatePassed || st.Note != "") {
			t.Fatalf("review status = %+v", st)
		}
	}
}

// The family is the model maker, so a Sonnet review of an Opus change is not
// independent, whoever hosts the model.
func TestModelFamilyIsTheMaker(t *testing.T) {
	for _, tc := range []struct{ harness, model, want string }{
		{"claude", "claude-opus-5-5", "anthropic"},
		{"claude", "sonnet", "anthropic"},
		{"opencode", "anthropic/claude-sonnet-5-5", "anthropic"},
		{"codex", "gpt-6-1-terra", "openai"},
		{"opencode", "openai/gpt-6-1-sol:high", "openai"},
		{"agy", "gemini-3-8-flash", "google"},
		{"opencode", "openrouter/deepseek/deepseek-v4-pro", "deepseek"},
		{"opencode", "deepseek/deepseek-v4-1-flash-1017", "deepseek"},
		{"opencode", "moonshot/kimi-k3", "moonshot"},
		{"opencode", "zai/glm-5-3", "zhipu"},
		{"opencode", "alibaba/qwen3-8-max", "alibaba"},
		{"opencode", "acme/foo-1", "acme"},
	} {
		if got := ModelFamily(tc.harness, tc.model); got != tc.want {
			t.Errorf("ModelFamily(%s, %s) = %s, want %s", tc.harness, tc.model, got, tc.want)
		}
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
	ev := []Evidence{pass(c, refAccept, 1, nil), pass(c, refTest, 2, map[string]string{FieldSuite: SuiteChanged, FieldTestsAdded: FieldYes})}
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
	// Under deploy rule ask, deploy waits for its own approval after merge.
	ev = append(ev, pass(c, refMerge, 4, map[string]string{FieldMergedCommit: commitM}))
	c.State, c.MergedCommit = ChangeMerged, commitM
	if got := p.NextSteps(c, ev); !reflect.DeepEqual(got, []StepRef{refApproveDeploy}) {
		t.Fatalf("after merge = %v", got)
	}
}

// Each maturity level's pipeline reads back as the step 6 settings the level
// gives today, and so does today's pipeline.
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
	today := PipelineToday()
	d := DefaultsForMaturity("")
	if tests, merge, deploy := today.Shorthand(); tests != d.TestsBeforeMerge || merge != d.MergeAuthority || deploy != d.DeployRule {
		t.Errorf("today's pipeline reads %s/%s/%s", tests, merge, deploy)
	}
	var refs []StepRef
	for _, s := range today.Steps {
		refs = append(refs, s.StepRef)
		want := OwnerCaller
		if s.Step == StepApprove {
			want = OwnerNone
		}
		if s.Owner.Kind != want {
			t.Errorf("today's %s owner = %s, want %s", s.StepRef, s.Owner.Kind, want)
		}
	}
	if want := []StepRef{refAccept, refTest, refReview, refApproveMerge, refMerge, refTestAfter, refRelease, refVerify}; !reflect.DeepEqual(refs, want) {
		t.Errorf("today's steps = %v, want %v", refs, want)
	}
	// A fresh copy each time: changing one must not change the next.
	p := PipelineToday()
	p.Steps[0].Owner.Kind = OwnerPerson
	p.Steps[4].Requires[2].Fields[FieldSuite] = SuiteFull
	if !reflect.DeepEqual(PipelineToday(), today) {
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
		{TestsOptional, MergeFull, DeployBrief, "accept, review, approve for merge", OwnerNone, ""},
		{TestsSuite, MergeDelegated, DeployAsk, "accept, review, test(suite=*), approve for merge", OwnerDelegate, OwnerDelegate},
		{TestsSuiteNewTests, MergeAsk, DeployOwner, "accept, review, test(suite=*, tests_added=yes), approve for merge", OwnerPerson, OwnerPerson},
		{TestsSuiteNewTestsReview, MergeFull, DeployBrief, "accept, review(findings_open=0), test(suite=*, tests_added=yes), approve for merge", OwnerNone, ""},
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
			t.Errorf("%s: approve for deploy owner %q, want %q", tc.deploy, got, tc.approveDeploy)
		}
		// Deploy brief cannot add a deploy step by itself.
		if _, has := p.Find(refDeploy); has != (tc.deploy != DeployBrief) {
			t.Errorf("%s: deploy step present = %v", tc.deploy, has)
		}
		review, _ := p.Find(refReview)
		wantFrom := "hard"
		if tc.tests == TestsSuiteNewTestsReview {
			wantFrom = ""
		}
		if review.RequiredFrom != wantFrom || review.Independent != IndependentNone {
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
	p.Steps[4].Requires[2].Fields[FieldSuite] = SuiteFull
	if tests, merge, deploy := p.Shorthand(); tests != ShorthandCustom || merge != MergeFull || deploy != DeployBrief {
		t.Errorf("suite=full reads %s/%s/%s", tests, merge, deploy)
	}
	p = PipelineToday()
	p.Steps[3].Owner = StepOwner{Kind: OwnerNode, Node: "architect"}
	if _, merge, _ := p.Shorthand(); merge != ShorthandCustom {
		t.Errorf("node approver reads %s", merge)
	}
	// voicemechs: a deploy step with no approval is deploy rule brief.
	p = PipelineToday()
	p.Steps = append(p.Steps, PipelineStep{StepRef: refDeploy, Owner: StepOwner{Kind: OwnerCaller}})
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, _, deploy := p.Shorthand(); deploy != DeployBrief {
		t.Errorf("deploy without approval reads %s", deploy)
	}
}

func TestReportFields(t *testing.T) {
	p, _ := PipelineFromShorthand(TestsSuiteNewTestsReview, MergeFull, DeployBrief)
	for _, tc := range []struct {
		p    Pipeline
		ref  StepRef
		want []string
	}{
		{p, refTest, []string{FieldSuite, FieldTestsAdded}},
		{p, refReview, []string{FieldFindingsOpen}},
		{p, refMerge, []string{FieldMergedCommit}},
		{p, refAccept, []string{}},
		{PipelineToday(), refTest, []string{FieldSuite}},
		{PipelineToday(), refTestAfter, []string{FieldSuite}},
	} {
		if got := tc.p.ReportFields(tc.ref); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s report fields = %v, want %v", tc.ref, got, tc.want)
		}
	}
}

func TestDeployOwnerFromTheGraph(t *testing.T) {
	g := Graph{Edges: []Edge{{From: "agent-net", To: "homelab", Kind: "depends"}, {From: "homelab", To: "voicemechs", Kind: "deploys"}}}
	if got := DefaultDeployOwner(g, "voicemechs"); got != (StepOwner{Kind: OwnerNode, Node: "homelab"}) {
		t.Errorf("voicemechs deploy owner = %+v", got)
	}
	if got := DefaultDeployOwner(g, "agent-net"); got != (StepOwner{Kind: OwnerCaller}) {
		t.Errorf("agent-net deploy owner = %+v", got)
	}
}

func TestAcceptsChecksTheHead(t *testing.T) {
	p := PipelineToday()
	c := testChange()
	ok := pass(c, refAccept, 1, nil)
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
		{"not in pipeline", func(_ *Change, e *Evidence) { e.StepRef = refDeploy }, "has no step deploy after merge"},
		{"left out", func(_ *Change, e *Evidence) { e.StepRef = refApproveMerge }, "approve for merge is left out"},
		{"after merge too early", func(_ *Change, e *Evidence) { e.StepRef = refTestAfter }, "test after merge runs on the merged commit, and change ch_1 is not merged"},
		{"merge without commit", func(_ *Change, e *Evidence) { e.StepRef = refMerge }, "a merge pass needs merged_commit"},
		{"stopped", func(c *Change, _ *Evidence) { c.State = ChangeStopped }, "change ch_1 is stopped"},
		{"bad result", func(_ *Change, e *Evidence) { e.Result = "ok" }, "result must be pass, fail, needs_owner or close"},
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
	ci := pass(merged, refTestAfter, 5, map[string]string{FieldSuite: SuiteFull})
	if err := p.Accepts(merged, ci); err != nil {
		t.Fatalf("CI on the merged commit: %v", err)
	}
	ci.Commit = commitA
	if err := p.Accepts(merged, ci); err == nil || !strings.Contains(err.Error(), "is not the merged commit cccccccccccc") {
		t.Fatalf("CI on the head: %v", err)
	}
	if err := p.Accepts(merged, pass(merged, refAccept, 6, nil)); err == nil || !strings.Contains(err.Error(), "change ch_1 is merged") {
		t.Fatalf("accept after merge: %v", err)
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
		{"project", func(c *Change) { c.Project = "" }, "project is empty"},
		{"branch", func(c *Change) { c.Branch = "bad branch" }, "branch"},
		{"same as target", func(c *Change) { c.Branch = "main" }, "is the target branch"},
		{"commit", func(c *Change) { c.HeadCommit = "HEAD" }, "head_commit must be a full commit hash"},
		{"base", func(c *Change) { c.BaseCommit = "main" }, "base_commit must be a full commit hash"},
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
	submit := ChangeSubmitRequest{SessionID: "s_1", ChangeID: "ch_0", Project: "demo", Branch: "feature/x", TargetBranch: "main", BaseCommit: commitBase, HeadCommit: commitA, Uncommitted: true, Origin: ChangeOrigin{Kind: OriginSession, SessionID: "s_1"}, Difficulty: "hard", Title: "Fix the login"}
	if err := submit.Validate(); err != nil {
		t.Fatal(err)
	}
	report := StepReportRequest{SessionID: "s_1", ChangeID: "ch_1", StepRef: refTestAfter, Result: StepResultPass, Commit: commitA, Fields: map[string]string{FieldSuite: SuiteFull}, DoneBy: "s_w", Note: "ok"}
	if err := report.Validate(); err != nil {
		t.Fatal(err)
	}
	claim := StepClaimRequest{SessionID: "s_1", ChangeID: "ch_1", StepRef: refTest, Commit: commitA}
	if err := claim.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*StepClaimRequest){
		func(q *StepClaimRequest) { q.SessionID = "" },
		func(q *StepClaimRequest) { q.ChangeID = "bad" },
		func(q *StepClaimRequest) { q.Step = "bad" },
		func(q *StepClaimRequest) { q.Commit = "bad" },
	} {
		q := claim
		edit(&q)
		if err := q.Validate(); err == nil {
			t.Fatalf("bad claim accepted: %+v", q)
		}
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
	for _, edit := range []func(*ChangeSubmitRequest){
		func(q *ChangeSubmitRequest) { q.HeadCommit = "" },
		func(q *ChangeSubmitRequest) { q.ChangeID = "x" },
		func(q *ChangeSubmitRequest) { q.SessionID = "" },
		func(q *ChangeSubmitRequest) { q.DiffStat = strings.Repeat("x", MaxDiffStatBytes+1) },
		func(q *ChangeSubmitRequest) { q.DiffStat = "file\x00name" },
	} {
		q := submit
		edit(&q)
		if err := q.Validate(); err == nil {
			t.Fatalf("bad submit accepted: %+v", q)
		}
	}

	view := ChangeView{Change: testChange(), Steps: PipelineToday().Status(testChange(), nil)}
	frames := map[string]any{
		FrameChangeSubmit: submit,
		FrameChangeGet:    ChangeGetRequest{ChangeID: "ch_1"},
		FrameChangeList:   ChangeListRequest{Project: "demo", State: ChangeListActive, Caller: "s_1", Limit: 10, After: "ch_0"},
		FrameStepClaim:    claim,
		FrameStepReport:   report,
		FrameStepTask:     StepTask{TaskID: "st_1", SessionID: "s_owner", Change: testChange(), StepRef: refTest, Owner: StepOwner{Kind: OwnerCaller}, Commit: commitA, DiffStat: "1 file", Evidence: []Evidence{pass(testChange(), refAccept, 1, nil)}, ReportFields: []string{FieldSuite}},
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
	for _, v := range []any{ChangeSubmitResult{Change: view}, ChangeListResult{Changes: []ChangeView{view}}, StepClaimResult{Claimed: true, SessionID: "s_1"}, StepReportResult{Change: view}} {
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
		ChangeSubmitRequest{}, ChangeSubmitResult{}, ChangeGetRequest{}, ChangeListRequest{}, ChangeListResult{}, StepClaimRequest{}, StepClaimResult{}, StepReportRequest{}, StepReportResult{}, StepTask{}}
	frames := map[reflect.Type]bool{reflect.TypeOf(ChangeSubmitRequest{}): true, reflect.TypeOf(ChangeGetRequest{}): true, reflect.TypeOf(ChangeListRequest{}): true, reflect.TypeOf(StepClaimRequest{}): true, reflect.TypeOf(StepReportRequest{}): true, reflect.TypeOf(StepTask{}): true}
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

func TestDeliveryPreMergeTestRequiresAccept(t *testing.T) {
	for _, tests := range []string{TestsOptional, TestsSuite, TestsSuiteNewTests, TestsSuiteNewTestsReview} {
		for _, merge := range []string{MergeFull, MergeDelegated, MergeAsk} {
			p, err := PipelineFromShorthand(tests, merge, DeployBrief)
			if err != nil {
				t.Fatal(err)
			}
			step, ok := p.Find(StepRef{Step: StepTest})
			if !ok || len(step.Requires) != 1 || step.Requires[0].Step != StepAccept {
				t.Errorf("tests=%s merge=%s: test requirements=%+v", tests, merge, step.Requires)
			}
		}
	}
}
