package protocol

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/settings"
)

func TestPipelineSettingsCustomRequirements(t *testing.T) {
	snap := settings.Snapshot{DeliveryCutover: true, DeliveryPipelineCutover: true, Values: []settings.Value{
		{Key: "delivery.pipeline.merge.requires", Level: "repo", Scope: "demo", Value: json.RawMessage(`["accept","review:findings_open=0","test:suite"]`)},
	}}
	p, err := PipelineFromSettings(snap, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if tests, _, _ := p.Shorthand(); tests != ShorthandCustom {
		t.Fatalf("custom pipeline shorthand = %q", tests)
	}
	merge, _ := p.Find(StepRef{Step: StepMerge})
	var review, test bool
	for _, req := range merge.Requires {
		if req.Step == StepReview && req.Fields[FieldFindingsOpen] == "0" {
			review = true
		}
		if req.Step == StepTest && req.Fields[FieldSuite] == FieldAny {
			test = true
		}
	}
	if !review || !test {
		t.Fatalf("custom merge requirements: %+v", merge.Requires)
	}
	text := NodeDefaultsWithSettings("", nil, snap, "demo").RulesText()
	if !strings.Contains(text, "test") || !strings.Contains(text, "review") {
		t.Fatalf("custom rule text = %q", text)
	}
}

func TestPipelineSettingsPreserveDeliveryBaseline(t *testing.T) {
	choices := map[string][]string{
		SettingTestsBeforeMerge: {TestsOptional, TestsSuite, TestsSuiteNewTests, TestsSuiteNewTestsReview},
		SettingMergeAuthority:   {MergeFull, MergeDelegated, MergeAsk},
		SettingDeployRule:       {DeployBrief, DeployAsk, DeployOwner},
	}
	aliases := map[string]string{SettingTestsBeforeMerge: "delivery.tests_before_merge", SettingMergeAuthority: "delivery.merge_authority", SettingDeployRule: "delivery.deploy_rule"}
	for _, maturity := range []string{"", "early", "growing", "mature", "critical"} {
		for name, values := range choices {
			for _, value := range values {
				alias := aliases[name]
				raw, _ := json.Marshal(value)
				canonical, err := settings.DeliveryCanonicalValue(alias, raw)
				if err != nil {
					t.Fatal(err)
				}
				snap := settings.Snapshot{DeliveryCutover: true, DeliveryPipelineCutover: true, Maturity: map[string]string{"demo": maturity}, Values: []settings.Value{{Key: settings.DeliveryCanonicalKey(alias), Level: "repo", Scope: "demo", Value: canonical}}}
				legacy := NodeDefaults(maturity, map[string]string{name: value})
				current := NodeDefaultsWithSettings(maturity, nil, snap, "demo")
				if current.RulesText() != legacy.RulesText() || current.TestText() != legacy.TestText() || current.MergeText() != legacy.MergeText() || current.DeployText() != legacy.DeployText() {
					t.Fatalf("%s %s=%s: current %+v %q, legacy %+v %q", maturity, name, value, current, current.RulesText(), legacy, legacy.RulesText())
				}
				p, err := PipelineFromSettings(snap, "demo")
				if err != nil {
					t.Fatal(err)
				}
				tests, merge, deploy := p.Shorthand()
				if tests != current.TestsBeforeMerge || merge != current.MergeAuthority || deploy != current.DeployRule {
					t.Fatalf("%s %s=%s: pipeline %s %s %s, rules %+v", maturity, name, value, tests, merge, deploy, current)
				}
			}
		}
	}
}

func TestPipelineSettingsRenderGraphApprovalOwner(t *testing.T) {
	snap := settings.Snapshot{DeliveryCutover: true, DeliveryPipelineCutover: true,
		DeliveryAssignments: []settings.DeliveryAssignment{{Project: "demo", Step: StepApprove, Node: "reviewer", Kind: "approves"}}}
	p, err := PipelineFromSettings(snap, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, merge, _ := p.Shorthand(); merge != ShorthandCustom {
		t.Fatalf("graph approver shorthand = %q", merge)
	}
	if got := snap.String("delivery.merge_authority", settings.Where{Repo: "demo"}); got != ShorthandCustom {
		t.Fatalf("settings shorthand = %q", got)
	}
	text := NodeDefaultsWithSettings("", nil, snap, "demo").RulesText()
	if !strings.Contains(text, "reviewer") || !strings.Contains(text, "approval") {
		t.Fatalf("approval rule text = %q", text)
	}
	approval := MergeApprovalForWithSettings(Graph{Nodes: []Node{{Name: "demo"}}}, "demo", snap)
	if !approval.Required || len(approval.Approvers) != 1 || approval.Approvers[0] != "reviewer" {
		t.Fatalf("graph approval = %+v", approval)
	}
	if note := AuthorityNote(true, false, ShorthandCustom); !strings.Contains(note, MergeAskText) {
		t.Fatalf("custom authority fallback = %q", note)
	}
}

func TestWorkerNoticeNamesAssignedMergeOwner(t *testing.T) {
	snap := settings.Snapshot{DeliveryPipelineCutover: true, DeliveryAssignments: []settings.DeliveryAssignment{{Project: "demo", Step: StepMerge, Node: "merger", Kind: "merges"}}}
	notice := FormatWorkerNoticeWithSettings(WorkerNotice{Project: "demo"}, "done", snap)
	if !strings.Contains(notice, "merge step") || !strings.Contains(notice, "merger") {
		t.Fatalf("notice does not name merge owner: %s", notice)
	}
}

func TestWorkerNoticeTellsCallerToReportAccept(t *testing.T) {
	caller := settings.Snapshot{DeliveryPipelineCutover: true}
	notice := FormatWorkerNoticeWithSettings(WorkerNotice{Project: "demo", Change: "ch_x", Worktree: "/w"}, "done", caller)
	want := "You own the accept step of ch_x. Report it: agent_step_report change=ch_x step=accept result=pass commit=<head>, or result=close with a one-line reason if nothing will be merged."
	if !strings.Contains(notice, want) {
		t.Fatalf("notice does not tell the caller to report accept: %s", notice)
	}
	assigned := settings.Snapshot{DeliveryPipelineCutover: true, DeliveryAssignments: []settings.DeliveryAssignment{{Project: "demo", Step: StepAccept, Node: "reviewer", Kind: "accepts"}}}
	if notice := FormatWorkerNoticeWithSettings(WorkerNotice{Project: "demo", Change: "ch_x", Worktree: "/w"}, "done", assigned); strings.Contains(notice, "You own the accept step") {
		t.Fatalf("notice tells the caller to report a step that another node owns: %s", notice)
	}
	merger := settings.Snapshot{DeliveryPipelineCutover: true, DeliveryAssignments: []settings.DeliveryAssignment{{Project: "demo", Step: StepAccept, Node: "@merger", Kind: "accepts"}}}
	if notice := FormatWorkerNoticeWithSettings(WorkerNotice{Project: "demo", Change: "ch_x", Worktree: "/w"}, "done", merger); strings.Contains(notice, "You own the accept step") {
		t.Fatalf("notice tells the caller to report a merger step: %s", notice)
	}
	if notice := FormatWorkerNoticeWithSettings(WorkerNotice{Project: "demo", Worktree: "/w"}, "done", caller); strings.Contains(notice, "You own the accept step") {
		t.Fatalf("notice names a change that was not recorded: %s", notice)
	}
}

func TestPipelineSettingsRenderCustomDeployApprover(t *testing.T) {
	snap := settings.Snapshot{DeliveryCutover: true, DeliveryPipelineCutover: true,
		DeliveryAssignments: []settings.DeliveryAssignment{{Project: "demo", Step: StepApprove, For: StepDeploy, AfterMerge: true, Node: "release-lead", Kind: "approves_deploy"}}}
	if got := snap.String("delivery.deploy_rule", settings.Where{Repo: "demo"}); got != ShorthandCustom {
		t.Fatalf("deploy shorthand = %q", got)
	}
	text := NodeDefaultsWithSettings("", nil, snap, "demo").RulesText()
	if !strings.Contains(text, "release-lead") || !strings.Contains(text, "deploy") {
		t.Fatalf("custom deploy rule text = %q", text)
	}
}

func TestPipelineSettingsResolveReviewRules(t *testing.T) {
	snap := settings.Snapshot{DeliveryCutover: true, DeliveryPipelineCutover: true, Values: []settings.Value{
		{Key: "delivery.pipeline.review.required_from", Level: "repo", Scope: "demo", Value: json.RawMessage(`"normal"`)},
		{Key: "delivery.pipeline.review.independent", Level: "repo", Scope: "demo", Value: json.RawMessage(`"session"`)},
		{Key: "delivery.pipeline.review.requires", Level: "repo", Scope: "demo", Value: json.RawMessage(`["test"]`)},
	}}
	p, err := PipelineFromSettings(snap, "demo")
	if err != nil {
		t.Fatal(err)
	}
	review, _ := p.Find(StepRef{Step: StepReview})
	if review.RequiredFrom != "normal" || review.Independent != IndependentSession || len(review.Requires) != 1 || review.Requires[0].Step != StepTest {
		t.Fatalf("review rules = %+v", review)
	}
	text := NodeDefaultsWithSettings("", nil, snap, "demo").RulesText()
	if !strings.Contains(text, "normal") || !strings.Contains(text, "session") {
		t.Fatalf("review rule text = %q", text)
	}
}

func TestMergeSuiteAndTestsAddedBothRequired(t *testing.T) {
	snap := settings.Snapshot{DeliveryPipelineCutover: true, Values: []settings.Value{{Key: "delivery.pipeline.merge.requires", Level: "repo", Scope: "demo", Value: json.RawMessage(`["accept","review","test:suite","test:tests_added"]`)}}}
	for _, canonical := range []bool{true, false} {
		var p Pipeline
		var err error
		if canonical {
			p, err = PipelineFromSettings(snap, "demo")
		} else {
			p, err = PipelineFromShorthand(TestsSuiteNewTests, MergeFull, DeployBrief)
		}
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, step := range p.Steps {
			if step.Step == StepMerge {
				for _, req := range step.Requires {
					if req.Step == StepTest {
						found = req.Fields[FieldSuite] == FieldAny && req.Fields[FieldTestsAdded] == FieldYes
					}
				}
			}
		}
		if !found {
			t.Errorf("canonical=%v: suite and tests_added must both be required: %+v", canonical, p)
		}
	}
}

func TestPipelineReviewRiskSetting(t *testing.T) {
	snap := settings.Snapshot{DeliveryPipelineCutover: true, Values: []settings.Value{
		{Key: "delivery.pipeline.review.required_risk", Level: "network", Scope: "network", Value: json.RawMessage(`3`)},
		{Key: "delivery.pipeline.review.required_risk", Level: "repo", Scope: "demo", Value: json.RawMessage(`4`)},
	}}
	p, err := PipelineFromSettings(snap, "demo")
	if err != nil {
		t.Fatal(err)
	}
	review, _ := p.Find(StepRef{Step: StepReview})
	if review.RequiredRisk != 3 {
		t.Fatalf("repo loosened risk threshold: %+v", review)
	}
	c := testChange()
	c.Risk = 3
	for _, st := range p.Status(c, nil) {
		if st.Step == StepReview && st.State == StepStateSkipped {
			t.Fatal("risk 3 review skipped")
		}
	}
}

func TestPipelineSettingsAfterMergeTestOwner(t *testing.T) {
	merger := settings.DeliveryAssignment{Project: "demo", Step: StepTest, Node: "/delivery/merger", Kind: "tests"}
	explicit := settings.DeliveryAssignment{Project: "demo", Step: StepTest, AfterMerge: true, Node: "/delivery/caller", Kind: "tests_after_merge"}
	for _, tc := range []struct {
		name        string
		assignments []settings.DeliveryAssignment
		want        StepOwner
	}{
		{"inherits merger", []settings.DeliveryAssignment{merger}, StepOwner{Kind: OwnerMerger}},
		{"explicit after merger", []settings.DeliveryAssignment{merger, explicit}, StepOwner{Kind: OwnerCaller}},
		{"explicit before merger", []settings.DeliveryAssignment{explicit, merger}, StepOwner{Kind: OwnerCaller}},
		{"default", nil, StepOwner{Kind: OwnerCaller}},
		{"other project", []settings.DeliveryAssignment{{Project: "other", Step: StepTest, Node: "/delivery/merger", Kind: "tests"}}, StepOwner{Kind: OwnerCaller}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := PipelineFromSettings(settings.Snapshot{DeliveryPipelineCutover: true, DeliveryAssignments: tc.assignments}, "demo")
			if err != nil {
				t.Fatal(err)
			}
			step, ok := p.Find(StepRef{Step: StepTest, AfterMerge: true})
			if !ok || step.Owner != tc.want {
				t.Fatalf("after-merge test owner=%+v found=%t want=%+v", step.Owner, ok, tc.want)
			}
		})
	}
}

func TestReleaseOwnerMergerAndGitHub(t *testing.T) {
	if err := (StepOwner{Kind: OwnerMerger}).validateFor(StepRelease); err != nil {
		t.Fatalf("merger must own release: %v", err)
	}
	if err := (StepOwner{Kind: OwnerGitHub}).validateFor(StepRelease); err == nil {
		t.Fatal("github must not own release")
	}
}
