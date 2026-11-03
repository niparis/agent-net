package protocol

import (
	"encoding/json"
	"fmt"

	"github.com/niparis/agent-net/internal/settings"
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
	// SettingAutonomy is whether agent-net may wake an idle agent that still
	// has work (decision 0036). Unlike the maturity overrides it is a public
	// rule, so every agent can read it. The default is off.
	SettingAutonomy = "autonomy"
)

// Autonomy values. They are rules, not display text.
const (
	AutonomyOff  = "off"
	AutonomyWake = "wake"
)

// MaturityDefaults is the set of controls a maturity level selects.
type MaturityDefaults struct {
	recipe           *settings.Snapshot
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
	// Autonomy is the effective autonomy rule, off when it is unset. It sits
	// beside the maturity defaults because it is not one of them.
	Autonomy string `json:"autonomy"`
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
	return nodeDefaults(value, nil, settings.Snapshot{}, "repo", false)
}

// NodeDefaults resolves the recipe defaults and applies legacy node overrides.
func NodeDefaults(maturity string, overrides map[string]string) MaturityDefaults {
	return nodeDefaults(maturity, overrides, settings.Snapshot{}, "repo", false)
}

// NodeDefaultsWithSettings resolves the current snapshot for one node.
func NodeDefaultsWithSettings(maturity string, overrides map[string]string, snap settings.Snapshot, repo string) MaturityDefaults {
	return nodeDefaults(maturity, overrides, snap, repo, true)
}

func nodeDefaults(maturity string, overrides map[string]string, snap settings.Snapshot, repo string, carry bool) MaturityDefaults {
	level, _, _ := ParseMaturity(strings.TrimSpace(maturity))
	if repo == "" {
		repo = "repo"
	}
	snap = snap.Clone()
	if snap.Maturity == nil {
		snap.Maturity = map[string]string{}
	}
	snap.Maturity[repo] = level
	keys := map[string]string{SettingTestsBeforeMerge: "delivery.tests_before_merge", SettingMergeAuthority: "delivery.merge_authority", SettingJudgeConsumers: "charter.judge_consumers", SettingDeployRule: "delivery.deploy_rule"}
	if carry && !snap.DeliveryCutover {
		// A pre-cutover snapshot may contain shadow values which were not in
		// force on that gateway. Its graph remains authoritative for them.
		filtered := snap.Values[:0]
		for _, v := range snap.Values {
			moved := false
			for _, key := range keys {
				if v.Key == key {
					moved = true
					break
				}
			}
			if !moved {
				filtered = append(filtered, v)
			}
		}
		snap.Values = filtered
	}
	for old, key := range keys {
		v := overrides[old]
		if v == "" || ValidateNodeSetting(old, v) != nil {
			continue
		}
		// The graph remains the fallback for an older gateway. Once a settings
		// snapshot says the delivery cutover completed, it is authoritative.
		if carry && snap.DeliveryCutover {
			continue
		}
		b, _ := json.Marshal(v)
		snap.Values = append(snap.Values, settings.Value{Key: key, Level: "repo", Scope: repo, Value: b})
	}
	where := settings.Where{Repo: repo}
	d := MaturityDefaults{Level: level, TestsBeforeMerge: snap.String("delivery.tests_before_merge", where), MergeAuthority: snap.String("delivery.merge_authority", where), JudgeConsumers: snap.String("charter.judge_consumers", where), DeployRule: snap.String("delivery.deploy_rule", where), WorkerSandbox: WorkersSandboxed}
	if level == MaturityCritical {
		d.WorkerSandbox = WorkersNoNetwork
	}
	if v := overrides[SettingWorkerSandbox]; v != "" && ValidateNodeSetting(SettingWorkerSandbox, v) == nil {
		d.WorkerSandbox = v
	}
	if carry {
		d.recipe = &snap
	}
	return d
}

func (d MaturityDefaults) settingText(key string) string {
	if d.recipe != nil {
		return d.recipe.String(key, settings.Where{})
	}
	return (settings.Snapshot{}).String(key, settings.Where{})
}

// Apply is NodeDefaults for a value that already has its level parsed.
func (d MaturityDefaults) Apply(settings map[string]string) MaturityDefaults {
	return NodeDefaults(d.Level, settings)
}

// NodeSettingNames lists the settings an owner may override.
func NodeSettingNames() []string {
	return []string{SettingTestsBeforeMerge, SettingMergeAuthority, SettingWorkerSandbox, SettingJudgeConsumers, SettingDeployRule, SettingAutonomy}
}

// NodeAutonomy returns the node's effective autonomy rule. The default is off,
// and an invalid or unknown stored value can only narrow it to off.
func NodeAutonomy(settings map[string]string) string {
	if ValidateNodeSetting(SettingAutonomy, settings[SettingAutonomy]) != nil {
		return AutonomyOff
	}
	if settings[SettingAutonomy] == AutonomyWake {
		return AutonomyWake
	}
	return AutonomyOff
}

// ValidateNodeSetting checks a setting name and value. An empty value is valid:
// it clears the override.
func ValidateNodeSetting(setting, value string) error {
	var allowed []string
	keys := map[string]string{SettingTestsBeforeMerge: "delivery.tests_before_merge", SettingMergeAuthority: "delivery.merge_authority", SettingJudgeConsumers: "charter.judge_consumers", SettingDeployRule: "delivery.deploy_rule", SettingAutonomy: "charter.autonomy_mode"}
	if setting == SettingWorkerSandbox {
		allowed = []string{WorkersSandboxed, WorkersNoNetwork, WorkersReadOnly}
	} else if key, ok := keys[setting]; ok {
		d, _ := settings.DefinitionFor(key)
		allowed = d.Values
	} else {
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
	testRule := d.TestsBeforeMerge
	switch testRule {
	case TestsOptional, TestsSuiteNewTests, TestsSuiteNewTestsReview:
	default:
		testRule = TestsSuite
	}
	suffix := strings.ReplaceAll(testRule, "-", "_")
	return d.settingText("delivery.test_text_" + suffix)
}

// These exported values are retained for older callers; the recipe owns them.
var (
	MergeFullText      = (settings.Snapshot{}).String("delivery.merge_text_full", settings.Where{})
	MergeDelegatedText = (MaturityDefaults{MergeAuthority: MergeDelegated}).MergeText()
	MergeAskText       = (settings.Snapshot{}).String("delivery.merge_text_ask", settings.Where{})
)

func MergeTextWithSettings(authority string, snap settings.Snapshot) string {
	return (MaturityDefaults{MergeAuthority: authority, recipe: &snap}).MergeText()
}

func (d MaturityDefaults) approverModes() []string {
	snap := settings.Snapshot{}
	if d.recipe != nil {
		snap = *d.recipe
	}
	modes := snap.Strings("delivery.delegated_approver_modes", settings.Where{})
	ordered := make([]string, 0, len(modes))
	for _, preferred := range []string{"owner", "arbiter"} {
		for _, mode := range modes {
			if mode == preferred {
				ordered = append(ordered, mode)
			}
		}
	}
	for _, mode := range modes {
		if mode != "owner" && mode != "arbiter" {
			ordered = append(ordered, mode)
		}
	}
	return ordered
}
func (d MaturityDefaults) approverModesText() string {
	modes := d.approverModes()
	if len(modes) == 0 {
		return "none"
	}
	return strings.Join(modes, " or ")
}
func (d MaturityDefaults) MergeText() string {
	authority := d.MergeAuthority
	if authority != MergeDelegated && authority != MergeAsk {
		authority = MergeFull
	}
	text := d.settingText("delivery.merge_text_" + authority)
	return strings.ReplaceAll(text, "{{modes}}", d.approverModesText())
}
func (d MaturityDefaults) DeployText() string {
	rule := d.DeployRule
	if rule != DeployAsk && rule != DeployOwner {
		rule = DeployBrief
	}
	return d.settingText("delivery.deploy_text_" + rule)
}
func (d MaturityDefaults) ConsumerText() string {
	rule := d.JudgeConsumers
	if rule != JudgeFree && rule != JudgeStrict && rule != JudgeEscalate {
		rule = JudgeCharter
	}
	return d.settingText("charter.consumer_text_" + rule)
}

// RulesText is the short sentence a session of the node gets every turn and a
// worker gets in its brief. It is empty when the node has no maturity and no
// approval override.
func (d MaturityDefaults) RulesText() string {
	level := d.Level
	if level == "" {
		if d.MergeAuthority != MergeAsk && d.MergeAuthority != MergeDelegated && d.DeployRule != DeployOwner {
			return ""
		}
		level = "not set"
	}
	return fmt.Sprintf("maturity %s: %s; %s; %s", level, d.TestText(), d.MergeText(), d.DeployText())
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
	return MergeApprovalForWithSettings(g, node, settings.Snapshot{})
}
func MergeApprovalForWithSettings(g Graph, node string, snap settings.Snapshot) MergeApproval {
	var n *Node
	for i := range g.Nodes {
		if g.Nodes[i].Name == node {
			n = &g.Nodes[i]
			break
		}
	}
	var d MaturityDefaults
	if n == nil {
		d = NodeDefaultsWithSettings("", nil, snap, "")
		d.MergeAuthority = snap.String("delivery.merge_authority", settings.Where{})
	} else {
		d = NodeDefaultsWithSettings(n.Charter.Maturity, n.Settings, snap, node)
	}
	out := MergeApproval{Authority: d.MergeAuthority}
	switch d.MergeAuthority {
	case MergeDelegated:
		out.Required = true
		out.Approvers = []string{"the owner"}
		modes := d.approverModes()
		for _, e := range g.Edges {
			allowed := false
			for _, mode := range modes {
				if e.Mode == mode {
					allowed = true
				}
			}
			if e.Kind == "governs" && e.To == node && allowed {
				out.Approvers = append(out.Approvers, e.From)
			}
		}
		out.Text = d.MergeText()
		if len(out.Approvers) > 1 {
			out.Text += " (delegates: " + strings.Join(out.Approvers[1:], ", ") + ")"
		} else {
			out.Text += " (an architect with mode " + d.approverModesText() + " over the node)"
		}
	case MergeAsk:
		out.Required = true
		out.Written = true
		out.Approvers = []string{"the owner"}
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

// WrittenApprovalNote is the note only agent-net can write for an approved
// action. It names one action: a different commit needs a new approval.
func WrittenApprovalNote(owner, action string, at time.Time) string {
	return fmt.Sprintf("%s approved: %s, %s", owner, action, at.UTC().Format(time.RFC3339))
}
