package protocol

import (
	"fmt"
	"regexp"
	"strings"
)

// Delegation policy (decision 0014). The owner sets, per session or per
// project, whether a session does its coding itself or hands it to a coder:
// a native subagent of its harness, or a worker that agent-net starts. The
// gateway stores it. Every turn, the harness adapter asks the daemon for the
// effective policy and puts one short text into what the model sees.

// Delegate modes.
const (
	DelegateOff     = "off"     // do the work yourself, no subagents or workers
	DelegateNative  = "native"  // delegate code changes to a native subagent of the harness
	DelegateWorkers = "workers" // delegate code changes to a worker started with agent_start
)

// Permission levels of unattended workers (decision 0018). The owner sets one
// per project; the default is WorkersSandboxed.
const (
	WorkersSandboxed = "sandboxed"  // writes only in the worktree, the temp folder and the main repository's git dir; network on
	WorkersNoNetwork = "no-network" // the same writes, network off
	WorkersReadOnly  = "read-only"  // no writes at all; network on
)

// WorkersLevels lists the levels in the order a form shows them.
var WorkersLevels = []string{WorkersSandboxed, WorkersNoNetwork, WorkersReadOnly}

// ValidWorkerPermissions tells whether s is a level.
func ValidWorkerPermissions(s string) bool {
	for _, l := range WorkersLevels {
		if l == s {
			return true
		}
	}
	return false
}

// WorkerPermissionsOrDefault maps "" (unset, or an older record) to the default.
func WorkerPermissionsOrDefault(s string) string {
	if s == "" {
		return WorkersSandboxed
	}
	return s
}

// Scopes of a policy.
const (
	ScopeSession = "session" // only this session
	ScopeProject = "project" // every session of the project on this machine, unless it has its own
)

// MethodPolicy is the local method of an adapter that asks for its own
// session's policy (OpSessionPolicy, the owner's method, is in operator.go).
// It is not an operator method.
const MethodPolicy = "policy"

// CoderAgentPrefix starts the name of the subagent that agent-net defines.
const CoderAgentPrefix = "agent-net-coder-"

// MaxModelName is the longest model name a policy holds.
const MaxModelName = 100

var modelNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+ -]*$`)

// DelegationPolicy is what the owner sets. A model name is loose
// (decision 0002): it is resolved against the machine of the session.
type DelegationPolicy struct {
	Delegate   string `json:"delegate"` // "" means no mode; else a Delegate* mode
	CoderModel string `json:"coder_model,omitempty"`
	MainModel  string `json:"main_model,omitempty"`
	// Workers is the permission level of unattended workers started in the
	// project (decision 0018). "" means the default, WorkersSandboxed. It is a
	// project setting: the gateway refuses it at session scope.
	Workers string `json:"workers,omitempty"`
}

// Clean trims the policy and checks it. The model names go into text the
// model reads, so they take only the characters of model names.
func (p DelegationPolicy) Clean() (DelegationPolicy, error) {
	for _, v := range []string{p.Delegate, p.CoderModel, p.MainModel, p.Workers} {
		if strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return p, fmt.Errorf("a policy field holds a control character")
		}
	}
	p.Delegate = strings.ToLower(strings.TrimSpace(p.Delegate))
	p.CoderModel = strings.Join(strings.Fields(p.CoderModel), " ")
	p.MainModel = strings.Join(strings.Fields(p.MainModel), " ")
	p.Workers = strings.ToLower(strings.TrimSpace(p.Workers))
	if p.Workers != "" && !ValidWorkerPermissions(p.Workers) {
		return p, fmt.Errorf("workers must be %s, %s or %s", WorkersSandboxed, WorkersNoNetwork, WorkersReadOnly)
	}
	switch p.Delegate {
	case "", DelegateOff, DelegateNative, DelegateWorkers:
	default:
		return p, fmt.Errorf("delegate must be %s, %s or %s", DelegateOff, DelegateNative, DelegateWorkers)
	}
	for _, m := range []struct{ what, v string }{{"coder model", p.CoderModel}, {"main model", p.MainModel}} {
		if m.v != "" && (len(m.v) > MaxModelName || !modelNameRE.MatchString(m.v)) {
			return p, fmt.Errorf("%s %q: use letters, digits and . _ : / @ + - only (at most %d characters)", m.what, m.v, MaxModelName)
		}
	}
	if (p.Delegate == DelegateNative || p.Delegate == DelegateWorkers) && p.CoderModel == "" {
		return p, fmt.Errorf("delegate %s needs a coder model", p.Delegate)
	}
	if p.Delegate == DelegateOff {
		p.CoderModel = ""
	}
	return p, nil
}

// Empty is true when the policy says nothing.
func (p DelegationPolicy) Empty() bool { return p == DelegationPolicy{} }

// DelegationView is the effective policy of one session, as the gateway
// answers it. Scope says where it comes from: the session's own record or its
// project's default. The resolved fields are the loose names matched against
// the model list of the session's machine and harness; an error says why not.
type DelegationView struct {
	DelegationPolicy
	Scope         string `json:"scope"`
	CoderResolved string `json:"coder_resolved,omitempty"`
	CoderError    string `json:"coder_error,omitempty"`
	MainResolved  string `json:"main_resolved,omitempty"`
	MainError     string `json:"main_error,omitempty"`
	// ModelList is true when the machine reports a model list for the harness,
	// so a name that resolved was checked. Without one the name passes as typed.
	ModelList bool `json:"model_list,omitempty"`
}

// Coder is the model the coder uses: resolved, else as typed.
func (v DelegationView) Coder() string { return firstString(v.CoderResolved, v.CoderModel) }

// Main is the model the main session should use: resolved, else as typed.
func (v DelegationView) Main() string { return firstString(v.MainResolved, v.MainModel) }

func firstString(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// SessionPolicyRequest is OpSessionPolicy. An empty policy clears it at that
// scope. DryRun checks and resolves without saving.
type SessionPolicyRequest struct {
	SessionID string           `json:"session_id"`
	Policy    DelegationPolicy `json:"policy"`
	Scope     string           `json:"scope"`
	DryRun    bool             `json:"dry_run,omitempty"`
}

// SessionPolicyResult answers OpSessionPolicy: the policy as it would be (or
// now is) for that session, with the models resolved. Effective is nil when
// the session ends up with no policy.
type SessionPolicyResult struct {
	SessionID string          `json:"session_id"`
	Saved     bool            `json:"saved"`
	Effective *DelegationView `json:"effective,omitempty"`
}

// SessionPolicy is the gateway frame "session.policy" (same fields).
type SessionPolicy = SessionPolicyRequest

// PolicyGet is the gateway frame "policy.get": the effective policy of one
// session. The answer is PolicyGot.
type PolicyGet struct {
	SessionID string `json:"session_id"`
}

// PolicyGot answers "policy.get". Delegation is nil when the session has no
// policy.
type PolicyGot struct {
	Harness    string          `json:"harness"`
	Delegation *DelegationView `json:"delegation,omitempty"`
}

// PolicyRequest is MethodPolicy. A plugin is bound to its session and leaves
// the fields empty. The Claude Code hook (the command line) names the session.
type PolicyRequest struct {
	Harness  string `json:"harness,omitempty"`
	NativeID string `json:"native_id,omitempty"`
}

// PolicyResult answers MethodPolicy. Text is empty when the session has no
// policy. AgentID and Coder are set when a native subagent is to be defined
// for the coder (the adapter registers it). Main is the model the session
// should switch to, when the harness can do it.
type PolicyResult struct {
	Text    string `json:"text,omitempty"`
	Mode    string `json:"mode,omitempty"` // the effective mode after harness fallbacks
	AgentID string `json:"agent_id,omitempty"`
	Coder   string `json:"coder,omitempty"`
	Main    string `json:"main,omitempty"`
}

// PolicySupport says what a harness does with a policy.
type PolicySupport struct {
	Applied    bool   // the policy text reaches the model every turn
	Native     bool   // native subagents exist
	MainModel  bool   // the session's model can be set from outside
	Note       string // for the owner, shown in the TUI
	NativeNote string
	MainNote   string
}

// SupportFor returns what a harness supports.
func SupportFor(harness string) PolicySupport {
	switch harness {
	case "opencode":
		return PolicySupport{Applied: true, Native: true, MainModel: true}
	case "claude":
		return PolicySupport{Applied: true, Native: true,
			Note:     "needs the Claude Code hook: agent-net setup --claude-policy-hook",
			MainNote: "Claude Code cannot be switched from outside: switch the model yourself once"}
	case "pi":
		return PolicySupport{Applied: true, MainModel: true,
			NativeNote: "Pi has no subagents: native uses workers"}
	}
	return PolicySupport{Note: "no hook for " + harness + " yet: the policy is not applied"}
}

// EffectiveMode is the mode a session of this harness really gets: native
// falls back to workers where the harness has no native subagents.
func EffectiveMode(harness, mode string) string {
	if mode == DelegateNative && !SupportFor(harness).Native {
		return DelegateWorkers
	}
	return mode
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

// CoderAgentID is the name of the subagent for a coder model, for example
// agent-net-coder-deepseek-deepseek-v4-1-flash.
func CoderAgentID(model string) string {
	s := strings.Trim(slugRE.ReplaceAllString(strings.ToLower(model), "-"), "-")
	if len(s) > 48 {
		s = strings.Trim(s[:48], "-")
	}
	return CoderAgentPrefix + s
}

// CoderAgentPrompt is the prompt of the native coder subagent.
const CoderAgentPrompt = "You implement the brief you are given, in the working tree you are in. " +
	"Make the change, run the tests, and fix what fails. Then report what you changed (the diff in short), the test result, and anything you could not finish. " +
	"Ask a question only if you are blocked."

// CoderAgentDescription is the description of the native coder subagent.
const CoderAgentDescription = "Implements a coding brief: edits the code, runs the tests and reports the diff. Defined by agent-net for your user's delegation policy."

// PolicyLead starts every policy text. It carries the agent-net marker, so a
// copy of the text in a message body is broken by NeutralizeMarkers.
const PolicyLead = "[agent-net] Your user's standing policy for this session, set in agent-net: "

// PolicyText is the text that goes to the model every turn. The model names
// are checked by Clean, so they hold nothing but name characters. agentID is
// the native subagent's name (used for native mode).
func PolicyText(v DelegationView, harness, agentID string) string {
	coder := v.Coder()
	switch EffectiveMode(harness, v.Delegate) {
	case DelegateOff:
		return PolicyLead + "do the code changes yourself. Do not start subagents or workers for them."
	case DelegateNative:
		return PolicyLead + fmt.Sprintf("delegate all code changes to the `%s` subagent (model %s). "+
			"You plan, write the brief, review its diff and run the tests. "+
			"Do not edit code yourself unless the subagent fails twice.", agentID, coder)
	case DelegateWorkers:
		return PolicyLead + fmt.Sprintf("delegate all code changes to a worker. Start one with agent_start in this project with model %q "+
			"(it gets its own git worktree by default), give it a brief, and wait for its report. "+
			"You plan, review its diff and run the tests. "+
			"Do not edit code yourself unless the worker fails twice.", coder)
	}
	return ""
}
