package protocol

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Maturity (decision 0022). A node's maturity level sets the defaults of the
// controls that used to be separate switches: tests before merge, merge
// authority, the worker sandbox level, how strictly the node judges requests
// from its consumers, and the deploy rule. A node may override any one default
// with a node setting stored beside its charter.
const (
	MaturityEarly    = "early"
	MaturityGrowing  = "growing"
	MaturityMature   = "mature"
	MaturityCritical = "critical"
)

// Values of the maturity defaults. They are stable names, not display text.
const (
	TestsOptional            = "optional"
	TestsSuite               = "suite"
	TestsSuiteNewTests       = "suite-new-tests"
	TestsSuiteNewTestsReview = "suite-new-tests-review"

	MergeFull      = "full"
	MergeDelegated = "delegated"
	MergeAsk       = "ask"

	JudgeFree     = "free"
	JudgeCharter  = "charter"
	JudgeStrict   = "strict"
	JudgeEscalate = "escalate"

	DeployBrief = "brief"
	DeployAsk   = "ask"
	DeployOwner = "owner"
)

// Node setting names. They are charter-adjacent: stored in the graph beside the
// charter, with an owner-only method to read and set them.
const (
	SettingTestsBeforeMerge = "tests_before_merge"
	SettingMergeAuthority   = "merge_authority"
	SettingWorkerSandbox    = "worker_sandbox"
	SettingJudgeConsumers   = "judge_consumers"
	SettingDeployRule       = "deploy_rule"
)

// MaturityDefaults is the set of controls a maturity level selects.
type MaturityDefaults struct {
	Level            string `json:"level,omitempty"`
	TestsBeforeMerge string `json:"tests_before_merge"`
	MergeAuthority   string `json:"merge_authority"`
	WorkerSandbox    string `json:"worker_sandbox"`
	JudgeConsumers   string `json:"judge_consumers"`
	DeployRule       string `json:"deploy_rule"`
}

// NodeSettingsResult answers node_settings_get and node_settings_set: the
// node's overrides, the defaults its maturity gives, and the effective values.
type NodeSettingsResult struct {
	Node      string            `json:"node"`
	Maturity  string            `json:"maturity,omitempty"`
	Overrides map[string]string `json:"overrides"`
	Defaults  MaturityDefaults  `json:"defaults"`
	Effective MaturityDefaults  `json:"effective"`
}

// NodeSettingsGetRequest is the request of node_settings_get.
type NodeSettingsGetRequest struct {
	Node string `json:"node"`
}

// NodeSettingsSetRequest is the request of node_settings_set. An empty Value
// clears the override.
type NodeSettingsSetRequest struct {
	Node    string `json:"node"`
	Setting string `json:"setting"`
	Value   string `json:"value"`
}

// DefaultsForMaturity returns the defaults of a maturity value, which may carry
// an explanation after " - ". An empty or unknown level gets the growing
// defaults with an empty Level: nothing says otherwise, so the conservative
// middle applies. Only a set maturity is written into a node's text.
func DefaultsForMaturity(value string) MaturityDefaults {
	level, _, _ := ParseMaturity(strings.TrimSpace(value))
	switch level {
	case MaturityEarly:
		return MaturityDefaults{Level: MaturityEarly, TestsBeforeMerge: TestsOptional, MergeAuthority: MergeFull,
			WorkerSandbox: WorkersSandboxed, JudgeConsumers: JudgeFree, DeployRule: DeployBrief}
	case MaturityMature:
		return MaturityDefaults{Level: MaturityMature, TestsBeforeMerge: TestsSuiteNewTests, MergeAuthority: MergeDelegated,
			WorkerSandbox: WorkersSandboxed, JudgeConsumers: JudgeStrict, DeployRule: DeployAsk}
	case MaturityCritical:
		return MaturityDefaults{Level: MaturityCritical, TestsBeforeMerge: TestsSuiteNewTestsReview, MergeAuthority: MergeAsk,
			WorkerSandbox: WorkersNoNetwork, JudgeConsumers: JudgeEscalate, DeployRule: DeployOwner}
	}
	return MaturityDefaults{Level: level, TestsBeforeMerge: TestsSuite, MergeAuthority: MergeFull,
		WorkerSandbox: WorkersSandboxed, JudgeConsumers: JudgeCharter, DeployRule: DeployBrief}
}

// NodeDefaults applies a node's overrides to its maturity defaults. A setting
// or value that is not valid is ignored, so old or hand-written data cannot
// widen a control by accident.
func NodeDefaults(maturity string, settings map[string]string) MaturityDefaults {
	d := DefaultsForMaturity(maturity)
	for setting, value := range settings {
		if ValidateNodeSetting(setting, value) != nil {
			continue
		}
		switch setting {
		case SettingTestsBeforeMerge:
			d.TestsBeforeMerge = value
		case SettingMergeAuthority:
			d.MergeAuthority = value
		case SettingWorkerSandbox:
			d.WorkerSandbox = value
		case SettingJudgeConsumers:
			d.JudgeConsumers = value
		case SettingDeployRule:
			d.DeployRule = value
		}
	}
	return d
}

// Apply is NodeDefaults for a value that already has its level parsed.
func (d MaturityDefaults) Apply(settings map[string]string) MaturityDefaults {
	return NodeDefaults(d.Level, settings)
}

// NodeSettingNames lists the settings an owner may override.
func NodeSettingNames() []string {
	return []string{SettingTestsBeforeMerge, SettingMergeAuthority, SettingWorkerSandbox, SettingJudgeConsumers, SettingDeployRule}
}

// ValidateNodeSetting checks a setting name and value. An empty value is valid:
// it clears the override.
func ValidateNodeSetting(setting, value string) error {
	var allowed []string
	switch setting {
	case SettingTestsBeforeMerge:
		allowed = []string{TestsOptional, TestsSuite, TestsSuiteNewTests, TestsSuiteNewTestsReview}
	case SettingMergeAuthority:
		allowed = []string{MergeFull, MergeDelegated, MergeAsk}
	case SettingWorkerSandbox:
		allowed = []string{WorkersSandboxed, WorkersNoNetwork, WorkersReadOnly}
	case SettingJudgeConsumers:
		allowed = []string{JudgeFree, JudgeCharter, JudgeStrict, JudgeEscalate}
	case SettingDeployRule:
		allowed = []string{DeployBrief, DeployAsk, DeployOwner}
	default:
		return fmt.Errorf("unknown node setting")
	}
	if value == "" {
		return nil
	}
	for _, v := range allowed {
		if v == value {
			return nil
		}
	}
	return fmt.Errorf("%s must be one of %s", setting, strings.Join(allowed, ", "))
}

// TestText is the tests-before-merge rule in plain words.
func (d MaturityDefaults) TestText() string {
	switch d.TestsBeforeMerge {
	case TestsOptional:
		return "tests optional"
	case TestsSuiteNewTests:
		return "run the suite and add tests for new behavior"
	case TestsSuiteNewTestsReview:
		return "run the suite, add tests for new behavior and get a review"
	}
	return "run the suite"
}

// MergeText is the merge-authority rule in plain words.
func (d MaturityDefaults) MergeText() string {
	switch d.MergeAuthority {
	case MergeDelegated:
		return "merge needs your owner's or a delegate's approval"
	case MergeAsk:
		return "merge needs your owner's written approval"
	}
	return "merge and push to the default branch"
}

// DeployText is the deploy rule in plain words.
func (d MaturityDefaults) DeployText() string {
	switch d.DeployRule {
	case DeployAsk:
		return "ask before deploying"
	case DeployOwner:
		return "ask the owner before deploying"
	}
	return "deploy as the brief says"
}

// ConsumerText is how strictly the node judges requests from its consumers.
func (d MaturityDefaults) ConsumerText() string {
	switch d.JudgeConsumers {
	case JudgeFree:
		return "accept requests from consumers freely"
	case JudgeStrict:
		return "judge requests from consumers strictly"
	case JudgeEscalate:
		return "escalate changes requested by consumers to the owner"
	}
	return "judge requests from consumers by the charter"
}

// RulesText is the short sentence a session of the node gets every turn and a
// worker gets in its brief. It is empty when the node has no maturity: a level
// that was never set must not invent rules.
func (d MaturityDefaults) RulesText() string {
	if d.Level == "" {
		return ""
	}
	return fmt.Sprintf("maturity %s: %s; %s; %s", d.Level, d.TestText(), d.MergeText(), d.DeployText())
}

// MergeApproval is the approval a merge under a node's authority needs.
type MergeApproval struct {
	Authority string   `json:"authority"`
	Required  bool     `json:"required"`
	Written   bool     `json:"written"`
	Approvers []string `json:"approvers,omitempty"`
	Text      string   `json:"text"`
}

// MergeApprovalFor works out the merge authority of a node from its maturity
// and overrides. The delegates of a delegated node are the architects with an
// owner or arbiter edge over it.
func MergeApprovalFor(g Graph, node string) MergeApproval {
	var n *Node
	for i := range g.Nodes {
		if g.Nodes[i].Name == node {
			n = &g.Nodes[i]
			break
		}
	}
	if n == nil {
		return MergeApproval{Text: "no node in the graph: ask your owner before merging"}
	}
	d := NodeDefaults(n.Charter.Maturity, n.Settings)
	out := MergeApproval{Authority: d.MergeAuthority}
	switch d.MergeAuthority {
	case MergeDelegated:
		out.Required = true
		out.Approvers = []string{"your owner"}
		for _, e := range g.Edges {
			if e.Kind == "governs" && e.To == node && (e.Mode == "owner" || e.Mode == "arbiter") {
				out.Approvers = append(out.Approvers, e.From)
			}
		}
		out.Text = d.MergeText()
		if len(out.Approvers) > 1 {
			out.Text += " (delegates: " + strings.Join(out.Approvers[1:], ", ") + ")"
		} else {
			out.Text += " (an architect with mode owner or arbiter over the node)"
		}
	case MergeAsk:
		out.Required = true
		out.Written = true
		out.Approvers = []string{"your owner"}
		out.Text = d.MergeText()
	default:
		out.Text = d.MergeText()
	}
	return out
}

// ApprovalRequest is an action that needs the owner's written approval.
type ApprovalRequest struct {
	Action         string `json:"action"`
	Recommendation string `json:"recommendation,omitempty"`
}

// ErrInboxNotBuilt says the owner's inbox is not in this build. Decision 0022
// step 3b adds it; until then a caller must ask its owner directly.
var ErrInboxNotBuilt = errors.New("the owner's inbox is not built yet: ask your owner directly; only an agent-net approval note counts as approval")

// RequestWrittenApproval is the inbox entry point. The inbox and its approval
// notes arrive with decision 0022 step 3b; this stub names that fact instead of
// pretending an approval can be requested.
func RequestWrittenApproval(req ApprovalRequest) (string, error) {
	if strings.TrimSpace(req.Action) == "" {
		return "", errors.New("an approval request needs the exact action")
	}
	return "", ErrInboxNotBuilt
}

// WrittenApprovalNote is the note only agent-net can write for an approved
// action. It names one action: a different commit needs a new approval.
func WrittenApprovalNote(owner, action string, at time.Time) string {
	return fmt.Sprintf("%s approved: %s, %s", owner, action, at.Format("15:04"))
}
