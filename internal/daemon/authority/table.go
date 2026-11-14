package authority

import "regexp"

// Row is what one scope kind allows. It is data: the decision method and the
// "Delegated authority" text both read the table and nothing else.
type Row struct {
	// Kind is the scope kind (protocol.ScopeMerge and so on).
	Kind string
	// Phrase finishes "you may ..." in the text a session reads.
	Phrase string
	// Bash holds patterns for one whole simple shell command. A command is
	// allowed only inside the scope's area: the repo of the folder it runs in.
	Bash []string
	// Tools are agent-net tools allowed with the scope, whatever the area:
	// the tool's own checks still apply when it runs.
	Tools []string
	// Ops are cto_op methods allowed with the scope.
	Ops []string
	// AnyOp allows every cto_op method except the owner-only actions.
	AnyOp bool
}

// Table is the scope table of decision 0165, in display order. The deploy row
// holds the repo's deploy script only: the rest of a repo's deploy rule is
// prose, not commands the daemon can match.
var Table = []Row{
	{
		Kind: "review", Phrase: "review and accept changes",
		Bash: []string{
			`git (status|log|diff|show|blame|ls-files|ls-tree|rev-parse|rev-list|describe|shortlog|grep|show-ref|merge-base|cat-file)( .*)?`,
			`git branch( (--list|--show-current|-a|-r|-v|-vv))*`,
			`(ls|cat|head|tail|wc|grep|rg|pwd|stat|file)( .*)?`,
			`gh pr (view|diff|checks|list|status)( .*)?`,
		},
		Tools: []string{"agent_step_report"},
	},
	{
		Kind: "merge", Phrase: "merge",
		Bash: []string{
			`git (merge|rebase|commit)( .*)?`,
			`git push( (-u|--set-upstream))? [A-Za-z0-9._-]+ (main|master|HEAD:main|HEAD:master)`,
			`gh pr merge( .*)?`,
		},
	},
	{
		Kind: "release", Phrase: "release",
		Bash: []string{
			`git tag( .*)?`,
			`git push( [A-Za-z0-9._-]+)? (--tags|refs/tags/\S+|v[0-9]+(\.[0-9]+)+\S*)`,
			`gh release (create|upload|view|list)( .*)?`,
			`(\./)?scripts/(release|dist)[A-Za-z0-9._-]*\.sh( .*)?`,
		},
	},
	{
		Kind: "deploy", Phrase: "deploy",
		Bash: []string{`(\./)?scripts/deploy[A-Za-z0-9._-]*\.sh( .*)?`},
	},
	{
		Kind: "asks", Phrase: "sort and answer asks",
		Tools: []string{"agent_ask_sort", "agent_ask_intake"},
	},
	{
		Kind: "permissions", Phrase: "apply approved permissions",
		Ops: []string{"permission_apply", "permission_undo"},
	},
	{
		Kind: "admin", Phrase: "run admin tools",
		Tools: []string{"agent_bar_edit", "agent_charter_draft", "agent_project_add", "agent_worker_profile_add", "agent_worker_profile_remove"},
		AnyOp: true,
	},
}

// bashRes holds the compiled Bash patterns of Table, anchored to the whole
// command.
var bashRes = func() map[string][]*regexp.Regexp {
	out := map[string][]*regexp.Regexp{}
	for _, row := range Table {
		for _, p := range row.Bash {
			out[row.Kind] = append(out[row.Kind], regexp.MustCompile(`^`+p+`$`))
		}
	}
	return out
}()

func (r Row) hasTool(name string) bool {
	for _, t := range r.Tools {
		if t == name {
			return true
		}
	}
	return false
}

func (r Row) hasOp(method string) bool {
	if r.AnyOp {
		return true
	}
	for _, o := range r.Ops {
		if o == method {
			return true
		}
	}
	return false
}
