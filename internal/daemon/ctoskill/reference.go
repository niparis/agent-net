package ctoskill

import (
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/authority"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// Reference returns the generated contents of reference.md. Every value comes
// from the code lists so that the skill cannot drift from what agent-net
// accepts.
func Reference() string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	w("# agent-net options reference (generated from the code of this agent-net version)\n\n")
	w("This file is generated from the code lists. Do not edit it by hand. Run `go generate ./internal/daemon/ctoskill`, or `go test ./internal/daemon/ctoskill -run TestReferenceMatchesCode -update`.\n\n")

	w("## Node settings\n\n")
	w("Set with `graph_change` action `settings_set`, or `settings_change` area `values_write`, on a node.\n\n")
	w("| setting | values | default (growing) | differs by maturity |\n|---|---|---|---|\n")
	growing := protocol.DefaultsForMaturity(protocol.MaturityGrowing)
	for _, row := range []struct {
		name   string
		values string
		def    string
	}{
		{protocol.SettingTestsBeforeMerge, settingValues("delivery.tests_before_merge"), growing.TestsBeforeMerge},
		{protocol.SettingMergeAuthority, settingValues("delivery.merge_authority"), growing.MergeAuthority},
		{protocol.SettingWorkerSandbox, strings.Join(protocol.WorkersLevels, ", "), growing.WorkerSandbox},
		{protocol.SettingJudgeConsumers, settingValues("charter.judge_consumers"), growing.JudgeConsumers},
		{protocol.SettingDeployRule, settingValues("delivery.deploy_rule"), growing.DeployRule},
		{protocol.SettingAutonomy, strings.Join([]string{protocol.AutonomyOff, protocol.AutonomyWake}, ", "), protocol.NodeAutonomy(nil)},
		{protocol.SettingWakeOnMessage, strings.Join([]string{protocol.WakeOnMessageOn, protocol.WakeOnMessageOff}, ", "), protocol.NodeWakeOnMessage(nil)},
		{protocol.AttrWorkMode, strings.Join([]string{protocol.WorkModeOnRequest, protocol.WorkModeWorkersCode}, ", "), protocol.WorkModeOnRequest},
	} {
		w("| %s | %s | %s | %s |\n", row.name, row.values, row.def, maturityDiff(row.name))
	}
	w("| loop_every | a duration above zero, such as 4h | empty (no loop) | same at every maturity |\n")
	w("| loop_prompt | text sent on each tick | empty | same at every maturity |\n")
	w("| loop_mode | %s, %s | empty (always) | same at every maturity |\n", protocol.LoopModeAlways, protocol.LoopModeOnChange)
	w("| loop_plan | a path inside the session folder, such as PLAN.md | empty | same at every maturity |\n\n")

	w("Maturity levels: %s.\n\n", strings.Join(protocol.MaturityLevels, ", "))
	w("A stored value may carry an explanation after ` - ` when it is read. `charter_set` takes the bare level and stores the explanation in `maturity_note`. The maturity defaults above come from the settings recipe, so they change with it.\n\n")

	w("## Other repo settings\n\n")
	w("| setting | default | meaning |\n|---|---|---|\n")
	for _, key := range []string{
		"max_agents",
		"workers.isolation_minimum",
		"delivery.ci_mode",
		"merger.test_timeout",
		"workers.max_age",
		"workers.idle_timeout",
		"workers.queue_timeout",
		"workers.auto_continue",
		"workers.auto_continue_max",
		"sessions.compaction_threshold_percent",
	} {
		def, ok := settings.DefinitionFor(key)
		if !ok {
			continue
		}
		meaning := def.Description
		if len(def.Values) > 0 {
			meaning += " Values: " + strings.Join(def.Values, ", ") + "."
		}
		w("| %s | %s | %s |\n", key, nodeValue(def.Default.Value), meaning)
	}
	w("\n")

	w("## Team\n\n")
	w("A team groups repos and agents that share a lead, purpose or rules. Create it with `team_create`, add members with `team_member_add`, and set its lead with `lead_set` ref team. `team_charter` sets purpose, refuses, escalate, rules, asks_to, work_mode, lead_merges, mode and held_back; `team_settings_set` sets asks_to, lead_merges and the team-level node settings.\n\n")
	w("| setting | values | default | meaning |\n|---|---|---|---|\n")
	w("| work_mode | %s, %s | %s | Who writes code. |\n", protocol.WorkModeOnRequest, protocol.WorkModeWorkersCode, protocol.WorkModeOnRequest)
	w("| asks_to | %s, %s | %s | Who answers a member's ask to the owner. |\n", protocol.AsksToOwner, protocol.AsksToLead, protocol.AsksToOwner)
	w("| lead_merges | %s, %s | %s | The owner's grant that the team lead merges the team's work. |\n", protocol.LeadMergesOn, protocol.LeadMergesOff, protocol.LeadMergesOff)
	w("| mode | %s | %s | How an order from the lead binds a member. %s. |\n", strings.Join(protocol.AuthorityModes, ", "), protocol.DefaultAuthorityMode, authorityModes())
	w("| held_back | free text | empty | What this team may not do. |\n\n")
	w("`held_back` narrows authority; rules text never grants it. These never delegate: %s.\n\n", protocol.AlwaysHeldBack)

	w("A team's charter fields are inherited by its members: a field of the same name on a repo or agent replaces the team's. Put a team-wide rule in `rules`, not in `purpose`, because a repo purpose replaces the team purpose.\n\n")

	w("## Leads and the tree\n\n")
	w("The repo lead is the repo's owner agent unless `lead_set` names another. The team lead is the team's owner agent. `lead_session` pins one session. A message from outside to a non-lead session is redirected to a live session of the lead agent; `repo:X` and `team:X` address the lead.\n\n")
	w("`reports_to` is the org tree: each Agent has at most one parent, the tree has no cycle, and the top is `%s`. Mergers are agents named `<project>-merger` under the repo's top agent. Lead level comes from a `lead_set` grant, from being a team lead, or from being chief of staff (`%s`). `started_by` says `agent-net` or `owner`.\n\n", protocol.OrgRoot, strings.Join([]string{protocol.LeadLevelGrant, protocol.LeadLevelTeamLead, protocol.LeadLevelChief}, "`, `"))

	w("## Delegation and workers\n\n")
	w("`session_change` action `policy`, with `scope` `session` or `project`:\n\n")
	w("- `delegate`: %s, %s or %s (empty means no mode)\n", protocol.DelegateOff, protocol.DelegateNative, protocol.DelegateWorkers)
	w("- `coder_model` and `main_model`: loose model names, resolved on the machine of the session\n")
	w("- `workers`: %s\n\n", strings.Join(protocol.WorkersLevels, ", "))
	w("A worker start leaves harness, model, provider, effort and machine empty to let the picker choose. Naming one needs the owner's words in `owner_words`, or the start waits for the owner. The difficulty is judged automatically. `isolation` is `%s` or `%s`; `none` is for read-only or non-code work. `fixes` names a shipped change this work fixes with `fixes_severity` from 1 to 4, `continue` names an ended worker to continue, and `base` names the base branch. Worker harnesses: %s.\n\n", protocol.IsolationWorktree, protocol.IsolationNone, strings.Join(protocol.WorkerHarnesses, ", "))

	w("## Delivery\n\n")
	w("Steps: %s.\n\n", strings.Join(protocol.StepNames(), ", "))
	w("Results: %s. `%s` is a question's reply, not a step result.\n\n", strings.Join(protocol.StepResults, ", "), protocol.StepResultWaiting)
	w("Step owners (`delivery_owner_set`):\n\n")
	w("| step | owners |\n|---|---|\n")
	for _, step := range protocol.StepNames() {
		var owners []string
		for _, kind := range protocol.StepOwnerKinds {
			if !protocol.ValidStepOwnerKind(kind, step) {
				continue
			}
			if kind == protocol.OwnerNode {
				owners = append(owners, "node <name>")
				continue
			}
			owners = append(owners, "@"+kind)
		}
		w("| %s | %s |\n", step, strings.Join(owners, ", "))
	}
	w("\n")
	w("Review is required from difficulty `%s` or risk `%s`; evidence independence is %s.\n\n", settingDefault("delivery.pipeline.review.required_from"), settingDefault("delivery.pipeline.review.required_risk"), settingValues("delivery.pipeline.review.independent"))
	w("`session_change` action `merger_start` starts the project's merger and assigns it the test and merge steps; `merger_stop` returns them to the caller. A release after merge can also be assigned to `@merger` with `delivery_owner_set` step `release`, `after_merge` true.\n\n")
	w("A deploy step runs only when the deploy rule is not `brief`; with `brief` the caller deploys as briefed. Step reminders wait `%s` and a stuck step is reported after `%s`.\n\n", settingDefault("delivery.step_reminder"), settingDefault("delivery.step_stuck"))

	w("## Edges\n\n")
	w("Stored kinds for `edge_set`: %s. A `depends` edge goes from consumer to provider, and `*` is allowed as the consumer. A `deploys` edge goes from deployer to app. The delivery kinds go from the step owner node to the repo. `%s` is the org tree edge and `member_of` is team membership; neither is set with `edge_set`.\n\n", strings.Join(protocol.EdgeKinds, ", "), protocol.EdgeReportsTo)
	w("Rules: %s.\n\n", protocol.EdgeRulesText())

	w("## Scopes\n\n")
	w("A scope is authority the owner delegates to a role node. `scope_set` takes an agent in `node`, a kind, an over target and an optional note (`value` through `graph_change`). Since decision 0166 a session whose role holds the scope may do that action without asking the owner. A scope never covers deleting data, spending money or using secrets.\n\n")
	w("Kinds: %s. Over: %s.\n\n", strings.Join(protocol.ScopeKinds, ", "), scopeOvers())
	for _, row := range authority.Table {
		w("### %s - %s\n\n", row.Kind, row.Phrase)
		if len(row.Bash) > 0 {
			w("- Shell: %s\n", codeList(row.Bash))
		}
		if len(row.Tools) > 0 {
			w("- Tools: %s\n", codeList(row.Tools))
		}
		if len(row.Ops) > 0 {
			w("- cto_op: %s\n", codeList(row.Ops))
		}
		if row.AnyOp {
			w("- cto_op: any method except the owner-only actions\n")
		}
		w("\n")
	}
	w("The answer from the scope check is allow or no decision, never block: a call outside every scope falls back to the harness's own permission prompt. The Claude hook, Codex command approvals and OpenCode permission prompts answer through `tool_decide`. Pi has no permission prompt; it only reads the Delegated authority line.\n\n")

	w("## Charter\n\n")
	w("| field | levels | size and meaning |\n|---|---|---|\n")
	for _, guide := range protocol.CharterGuide() {
		var levels []string
		for _, level := range []string{"global", "repo", "team", "agent"} {
			if protocol.CharterAttributeAtLevel(level, guide.Name) {
				levels = append(levels, level)
			}
		}
		w("| %s | %s | %s. %s |\n", guide.Name, strings.Join(levels, ", "), guide.Size, guide.Meaning)
	}
	w("\n")
	w("A value holds at most %d bytes. Budget about 1200 characters for the whole charter because the project's agent reads it every turn. Neighbours see only the first %d characters of `accepts` and `refuses`, so put the main item first. `purpose` is one sentence up to 160 characters. `accepts`, `refuses` and `escalate` hold at most 6 items. A value that looks like an IP address, a token or an absolute path gets a warning; never put secrets in a charter. Put detail in the project's AGENTS.md.\n\n", protocol.CharterValueMaxBytes, protocol.NeighborFieldChars)

	w("## Permissions, tools and the rest\n\n")
	w("- `cto_op` method `permission_apply` with the owner's words in `owner_words`, or `agent_permission_propose` for the Desk.\n")
	w("- `session_change` action `trust` for a folder, and `mcp_servers` to approve or disable a folder's `.mcp.json` servers. Shared MCP servers use `mcp_change`.\n")
	w("- `bar_edit` adds, edits or removes a project's bar rules; one rule is one line of at most %d characters.\n", protocol.MaxBarRuleLen)
	w("- `agent_owner_judgment_record` stores a standing rule: text, area (`network`, `repo:X` or `team:X`) and action. Both must match for the rule to apply.\n")
	w("- Role models are settings: `cto.models`, `chief.model` and `merger.models`. Each is an ordered list of candidates, one per line as harness, provider, model and effort; the first one that can run now is used.\n")
	w("- Owner sessions use harness `claude` by default; `opencode`, `codex` and `pi` are also supported. An empty model leaves the choice to the harness, and agent-net sets no default owner-session model.\n")
	w("- Placement: `projects_layout` scans the owner's home folder for git repos and worktrees, within a bounded depth and count, and caches the result. `cto_placement_memory` saves the owner's placement answers.\n")

	return b.String()
}

func settingValues(key string) string {
	def, ok := settings.DefinitionFor(key)
	if !ok {
		return ""
	}
	return strings.Join(def.Values, ", ")
}

func settingDefault(key string) string {
	def, ok := settings.DefinitionFor(key)
	if !ok {
		return ""
	}
	return nodeValue(def.Default.Value)
}

// nodeValue is a setting default as a person reads it.
func nodeValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "empty"
	}
	return value
}

func codeList(items []string) string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = "`" + item + "`"
	}
	return strings.Join(out, ", ")
}

// maturityDiff names the levels whose value differs from growing.
func maturityDiff(name string) string {
	growing := protocol.DefaultsForMaturity(protocol.MaturityGrowing)
	var parts []string
	for _, level := range protocol.MaturityLevels {
		if level == protocol.MaturityGrowing {
			continue
		}
		value := maturityValue(protocol.DefaultsForMaturity(level), name)
		if value != maturityValue(growing, name) {
			parts = append(parts, level+" "+value)
		}
	}
	if len(parts) == 0 {
		return "same at every maturity"
	}
	return strings.Join(parts, "; ")
}

func maturityValue(d protocol.MaturityDefaults, name string) string {
	switch name {
	case protocol.SettingTestsBeforeMerge:
		return d.TestsBeforeMerge
	case protocol.SettingMergeAuthority:
		return d.MergeAuthority
	case protocol.SettingWorkerSandbox:
		return d.WorkerSandbox
	case protocol.SettingJudgeConsumers:
		return d.JudgeConsumers
	case protocol.SettingDeployRule:
		return d.DeployRule
	}
	return ""
}

func authorityModes() string {
	parts := make([]string, 0, len(protocol.AuthorityModes))
	for _, mode := range protocol.AuthorityModes {
		parts = append(parts, "`"+mode+"`: "+protocol.AuthorityModeMeaning(mode))
	}
	return strings.Join(parts, "; ")
}

func scopeOvers() string {
	parts := make([]string, 0, len(protocol.ScopeOverKinds))
	for _, kind := range protocol.ScopeOverKinds {
		if kind == protocol.ScopeOverAll {
			parts = append(parts, "`"+kind+"`")
			continue
		}
		parts = append(parts, "`"+kind+":X`")
	}
	return strings.Join(parts, ", ")
}
