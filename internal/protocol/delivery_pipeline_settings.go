package protocol

import (
	"fmt"

	"github.com/niparis/agent-net/internal/settings"
)

// PipelineFromSettings resolves the ordered rule components. Owners of graph
// assignments are overlaid by the gateway after this function returns.
func PipelineFromSettings(snap settings.Snapshot, repo string) (Pipeline, error) {
	where := settings.Where{Repo: repo}
	tests := snap.String("delivery.tests_before_merge", where)
	merge := snap.String("delivery.merge_authority", where)
	deploy := snap.String("delivery.deploy_rule", where)
	if snap.DeliveryPipelineCutover {
		merge = map[string]string{OwnerNone: MergeFull, OwnerDelegate: MergeDelegated, OwnerPerson: MergeAsk}[snap.String("delivery.pipeline.merge.approver", where)]
		deploy = map[string]string{OwnerNone: DeployBrief, OwnerDelegate: DeployAsk, OwnerPerson: DeployOwner}[snap.String("delivery.pipeline.deploy.approver", where)]
	}
	customTests := tests == ShorthandCustom
	if customTests {
		tests = TestsSuite
	}
	p, err := PipelineFromShorthand(tests, merge, deploy)
	if err != nil {
		return Pipeline{}, err
	}
	if customTests {
		set := map[string]bool{}
		for _, item := range snap.Strings("delivery.pipeline.merge.requires", where) {
			set[item] = true
		}
		var requires []Requirement
		if set["accept"] {
			requires = append(requires, Requirement{StepRef: StepRef{Step: StepAccept}})
		}
		if set["review"] || set["review:findings_open=0"] {
			req := Requirement{StepRef: StepRef{Step: StepReview}}
			if set["review:findings_open=0"] {
				req.Fields = map[string]string{FieldFindingsOpen: "0"}
				for i := range p.Steps {
					if p.Steps[i].Step == StepReview {
						p.Steps[i].RequiredFrom = ""
					}
				}
			}
			requires = append(requires, req)
		}
		if set["test:suite"] || set["test:tests_added"] {
			req := Requirement{StepRef: StepRef{Step: StepTest}, Fields: map[string]string{}}
			if set["test:tests_added"] {
				req.Fields[FieldTestsAdded] = FieldYes
			} else {
				req.Fields[FieldSuite] = FieldAny
			}
			requires = append(requires, req)
		}
		requires = append(requires, Requirement{StepRef: StepRef{Step: StepApprove, For: StepMerge}})
		for i := range p.Steps {
			if p.Steps[i].Step == StepMerge {
				p.Steps[i].Requires = requires
			}
		}
	}
	if snap.DeliveryPipelineCutover {
		for i := range p.Steps {
			if p.Steps[i].Step != StepReview {
				continue
			}
			p.Steps[i].Independent = snap.String("delivery.pipeline.review.independent", where)
			if p.Steps[i].RequiredFrom != "" {
				p.Steps[i].RequiredFrom = snap.String("delivery.pipeline.review.required_from", where)
			}
			for _, required := range snap.Strings("delivery.pipeline.review.requires", where) {
				p.Steps[i].Requires = append(p.Steps[i].Requires, Requirement{StepRef: StepRef{Step: required}})
			}
		}
	}
	for _, step := range PipelineToday().Steps {
		if step.AfterMerge {
			if _, have := p.Find(step.StepRef); !have {
				p.Steps = append(p.Steps, step)
			}
		}
	}
	if _, have := p.Find(StepRef{Step: StepApprove, For: StepDeploy, AfterMerge: true}); !have {
		for _, a := range snap.DeliveryAssignments {
			if a.Project == repo && a.Kind == "approves_deploy" && a.Node != "/delivery/none" {
				approve := StepRef{Step: StepApprove, For: StepDeploy, AfterMerge: true}
				p.Steps = append(p.Steps,
					PipelineStep{StepRef: approve, Owner: StepOwner{Kind: OwnerNone}},
					PipelineStep{StepRef: StepRef{Step: StepDeploy, AfterMerge: true}, Owner: StepOwner{Kind: OwnerCaller}, Requires: []Requirement{{StepRef: approve}}})
				break
			}
		}
	}
	for i := range p.Steps {
		var chosen, fallback string
		for _, a := range snap.DeliveryAssignments {
			if a.Project != repo || a.Step != p.Steps[i].Step || a.AfterMerge != p.Steps[i].AfterMerge ||
				(a.Step == StepApprove && a.For != p.Steps[i].For && !(a.For == "" && p.Steps[i].For == StepMerge)) {
				continue
			}
			if a.Kind == "deploys" {
				if fallback == "" {
					fallback = a.Node
				}
			} else {
				chosen = a.Node
			}
		}
		if chosen == "" {
			chosen = fallback
		}
		if chosen != "" {
			candidate := DeliveryOwnerFromSource(chosen)
			if p.Steps[i].Step == StepApprove && p.Steps[i].Owner.Kind != OwnerNone &&
				!(p.Steps[i].Owner.Kind == OwnerDelegate && candidate.Kind == OwnerPerson) {
				continue
			}
			p.Steps[i].Owner = candidate
		}
	}
	if err := p.Validate(); err != nil {
		return Pipeline{}, fmt.Errorf("delivery pipeline: %w", err)
	}
	return p, nil
}
