package protocol

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The delivery pipeline (decision 0056, docs/design/delivery-pipeline.md).
//
// Git holds the work: a change is a branch and its head commit. agent-net
// decides who does each step of a repo's pipeline and keeps the evidence of
// each step, keyed by the exact commit it was done on. A new head commit makes
// a new revision of the change, and evidence of an older revision no longer
// counts.
//
// This file is the shared contract: the types, the pure rules on them, and the
// frames. It stores nothing and routes nothing.

// Steps, in their usual order. A pipeline uses a subset of them.
const (
	StepCheck   = "check"
	StepReview  = "review"
	StepTest    = "test"
	StepApprove = "approve"
	StepMerge   = "merge"
	StepRelease = "release"
	StepDeploy  = "deploy"
	StepVerify  = "verify"
)

var stepOrder = []string{StepCheck, StepReview, StepTest, StepApprove, StepMerge, StepRelease, StepDeploy, StepVerify}

// StepNames lists every step agent-net knows how to route, in their usual
// order.
func StepNames() []string { return append([]string(nil), stepOrder...) }

// ValidStep reports whether step is one of StepNames.
func ValidStep(step string) bool {
	for _, s := range stepOrder {
		if s == step {
			return true
		}
	}
	return false
}

// The result a step owner reports.
const (
	StepResultPass       = "pass"
	StepResultFail       = "fail"
	StepResultNeedsOwner = "needs_owner"
)

// Kinds of step owner. OwnerPerson is the owner of the network (the person):
// the step becomes an Inbox approval. OwnerDelegate and OwnerNone are only for
// approve: delegate is whoever governs the node in the graph in an allowed mode
// (delivery.delegated_approver_modes), or the owner, resolved when the step
// starts; none leaves the approval out.
const (
	OwnerCaller   = "caller"
	OwnerNode     = "node"
	OwnerGitHub   = "github"
	OwnerPerson   = "owner"
	OwnerDelegate = "delegate"
	OwnerNone     = "none"
)

// How independent a step's evidence must be from the change's author. The same
// words are the values of the independent evidence field, which agent-net
// sets: none (same session), session (another session, same model family) or
// family (another model family, a person, or GitHub).
const (
	IndependentNone    = "none"
	IndependentSession = "session"
	IndependentFamily  = "family"
)

// Where a failed step sends the change. Empty means OnFailCaller.
const (
	OnFailCaller = "caller"
	OnFailAuthor = "author"
)

// Evidence field names. A pipeline requirement names fields and the exact
// values it needs. Field values are strings.
const (
	FieldTestsAdded   = "tests_added"   // test: yes or no
	FieldSuite        = "suite"         // test: changed (the changed packages) or full
	FieldFindingsOpen = "findings_open" // review: count of blocker findings not fixed
	FieldIndependent  = "independent"   // set by agent-net, never by the reporter
	FieldRun          = "run"           // the command run, or a CI run link
	FieldMergedCommit = "merged_commit" // merge: the merged commit on the target branch
	FieldTag          = "tag"           // release: the tag
	FieldTarget       = "target"        // deploy: where it was deployed
)

// Values of the suite and tests_added fields. A CI run of the whole suite
// reports suite full, with the run link in run.
const (
	SuiteChanged = "changed"
	SuiteFull    = "full"
	FieldYes     = "yes"
	FieldNo      = "no"
)

// Who did a step, when it was not a session: EvidenceByOwner for the person,
// EvidenceByGitHub for a result read from GitHub.
const (
	EvidenceByOwner  = "owner"
	EvidenceByGitHub = "github"
)

// Kinds of change origin.
const (
	OriginWorker  = "worker"
	OriginSession = "session"
	OriginPerson  = "person"
)

// Change states. A change is open until its merge step passes, then merged
// while the after-merge steps run, then done. Stopped means withdrawn or given
// up; a failed step does not stop a change, it waits for a fix.
const (
	ChangeOpen    = "open"
	ChangeMerged  = "merged"
	ChangeDone    = "done"
	ChangeStopped = "stopped"
)

// ChangeListActive is the change.list state filter for open and merged
// changes; it is the default. ChangeListAny lists every state.
const (
	ChangeListActive = "active"
	ChangeListAny    = "any"
)

// ChangeIDPrefix starts every change ID. The gateway assigns the IDs.
const ChangeIDPrefix = "ch_"

// ShorthandCustom is what a step 6 delivery setting reads when the pipeline
// says something that setting cannot.
const ShorthandCustom = "custom"

// Limits on what a report may carry. The gateway refuses more.
const (
	MaxEvidenceFields     = 16
	MaxEvidenceFieldBytes = 200
	MaxEvidenceNoteBytes  = 8 * 1024
	MaxBranchBytes        = 255
	MaxChangeTitleBytes   = 200
	maxNameBytes          = 120
)

// StepRef names one step of a pipeline. For is set only for approve and says
// what it approves: merge or deploy. A pipeline holds each step once, and
// approve once for each For.
type StepRef struct {
	Step string `json:"step"`
	For  string `json:"for,omitempty"`
}

func (r StepRef) String() string {
	if r.For != "" {
		return r.Step + " for " + r.For
	}
	return r.Step
}

// Validate checks the step name and For.
func (r StepRef) Validate() error {
	if !ValidStep(r.Step) {
		return fmt.Errorf("unknown step %q; steps are %s", r.Step, strings.Join(stepOrder, ", "))
	}
	if r.Step == StepApprove {
		if r.For != StepMerge && r.For != StepDeploy {
			return errors.New("approve needs for: merge or deploy")
		}
	} else if r.For != "" {
		return fmt.Errorf("%s takes no for; only approve does", r.Step)
	}
	return nil
}

// StepOwner is who does a step. Node is the node name, set only for kind node.
type StepOwner struct {
	Kind string `json:"kind"`
	Node string `json:"node,omitempty"`
}

func (o StepOwner) String() string {
	if o.Kind == OwnerNode {
		return "node " + o.Node
	}
	return o.Kind
}

func (o StepOwner) validateFor(step string) error {
	switch o.Kind {
	case OwnerNode:
		if strings.TrimSpace(o.Node) == "" {
			return errors.New("owner node needs a node name")
		}
		if len(o.Node) > maxNameBytes {
			return fmt.Errorf("owner node name is longer than %d bytes", maxNameBytes)
		}
		return nil
	case OwnerCaller, OwnerPerson:
	case OwnerGitHub:
		if step != StepTest && step != StepMerge {
			return fmt.Errorf("owner github is only for test and merge, not %s", step)
		}
	case OwnerDelegate, OwnerNone:
		if step != StepApprove {
			return fmt.Errorf("owner %s is only for approve, not %s", o.Kind, step)
		}
	default:
		return fmt.Errorf("unknown owner kind %q; kinds are caller, node, github, owner, delegate, none", o.Kind)
	}
	if o.Node != "" {
		return fmt.Errorf("owner %s takes no node name", o.Kind)
	}
	return nil
}

// Requirement is one condition of a step: the named step has passed on the
// commit it runs on, with each field equal to the value given.
type Requirement struct {
	StepRef
	Fields map[string]string `json:"fields,omitempty"`
}

// String is the rule text: test(suite=full, tests_added=yes).
func (r Requirement) String() string {
	if len(r.Fields) == 0 {
		return r.StepRef.String()
	}
	parts := make([]string, 0, len(r.Fields))
	for _, k := range sortedKeys(r.Fields) {
		parts = append(parts, k+"="+r.Fields[k])
	}
	return r.StepRef.String() + "(" + strings.Join(parts, ", ") + ")"
}

// PipelineStep is one step of a pipeline with its owner and rules.
//
// Requires lists steps that must have passed first, each listed earlier in
// the pipeline. Independent is none, session or family (empty is none).
// OnFail is caller or author (empty is caller). AfterMerge is true exactly for
// the steps listed after merge: they run on the merged commit. RequiredFrom is
// a difficulty (DifficultyLevels): below it the step is left out unless it
// ran anyway; empty means always.
type PipelineStep struct {
	StepRef
	Owner        StepOwner     `json:"owner"`
	Requires     []Requirement `json:"requires,omitempty"`
	Independent  string        `json:"independent,omitempty"`
	OnFail       string        `json:"on_fail,omitempty"`
	AfterMerge   bool          `json:"after_merge,omitempty"`
	RequiredFrom string        `json:"required_from,omitempty"`
}

// Pipeline is a repo's ordered delivery steps (the delivery.pipeline setting).
// Steps without a requirement between them may run in parallel.
type Pipeline struct {
	Steps []PipelineStep `json:"steps"`
}

// ChangeOrigin is where a change came from. WorkerID and SessionID name the
// worker run and its session; Model and ModelFamily are the author's, used to
// judge independence.
type ChangeOrigin struct {
	Kind        string `json:"kind"`
	WorkerID    string `json:"worker_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	Person      string `json:"person,omitempty"`
	Model       string `json:"model,omitempty"`
	ModelFamily string `json:"model_family,omitempty"`
}

// Change is one unit of delivery: a repo, a branch and its head commit. There
// is at most one open change per repo and branch: a later submit for the same
// branch is the same change, with a new revision when the head moved.
//
// Repo is the project name, as the settings layer and the graph name it.
// Caller is the session that gets the final notice. Difficulty is the judge's
// label for the worker run, empty when unknown. Times are Unix seconds.
//
// Change is a record, not a frame: never embed it anonymously in a request,
// because its id would clash with the frame's id.
type Change struct {
	ID           string       `json:"id"`
	Repo         string       `json:"repo"`
	Branch       string       `json:"branch"`
	TargetBranch string       `json:"target_branch,omitempty"`
	HeadCommit   string       `json:"head_commit"`
	Revision     int          `json:"revision"`
	Origin       ChangeOrigin `json:"origin"`
	Caller       string       `json:"caller_session_id,omitempty"`
	Title        string       `json:"title,omitempty"`
	Difficulty   string       `json:"difficulty,omitempty"`
	State        string       `json:"state"`
	MergedCommit string       `json:"merged_commit,omitempty"`
	CreatedAt    int64        `json:"created_at"`
	UpdatedAt    int64        `json:"updated_at,omitempty"`
}

// Revision is one head commit of a change. Number starts at 1.
type Revision struct {
	ChangeID string `json:"change_id"`
	Number   int    `json:"number"`
	Commit   string `json:"commit"`
	At       int64  `json:"at"`
}

// Evidence is one step result for one revision of a change, on one exact
// commit: the head for steps before merge, the merged commit after it.
//
// By is who did the work: a session ID, EvidenceByOwner or EvidenceByGitHub.
// ReportedBy is the session that reported it, when another session (a worker
// the reporter started) did the work. ByModel and ByModelFamily belong to By.
type Evidence struct {
	ChangeID string `json:"change_id"`
	Revision int    `json:"revision"`
	StepRef
	Result        string            `json:"result"`
	Commit        string            `json:"commit"`
	Fields        map[string]string `json:"fields,omitempty"`
	By            string            `json:"by"`
	ReportedBy    string            `json:"reported_by,omitempty"`
	ByModel       string            `json:"by_model,omitempty"`
	ByModelFamily string            `json:"by_model_family,omitempty"`
	Note          string            `json:"note,omitempty"`
	At            int64             `json:"at"`
}

// States of one step of one change, from Pipeline.Status.
const (
	StepStateSkipped    = "skipped"     // left out: owner none, or below required_from and not run
	StepStateWaiting    = "waiting"     // a requirement does not hold yet
	StepStateReady      = "ready"       // may start now
	StepStatePassed     = "passed"      // its latest counted result is pass
	StepStateFailed     = "failed"      // its latest counted result is fail
	StepStateNeedsOwner = "needs_owner" // its latest counted result is needs_owner
)

// StepStatus is where one step of a change stands. Missing lists what a
// waiting step waits for. Note says why a step is left out, or why a pass did
// not count. Evidence is the latest counted result. Ready does not mean
// nobody works on it: the gateway knows which step tasks are out.
type StepStatus struct {
	StepRef
	Owner    StepOwner `json:"owner"`
	State    string    `json:"state"`
	Missing  []string  `json:"missing,omitempty"`
	Note     string    `json:"note,omitempty"`
	Evidence *Evidence `json:"evidence,omitempty"`
}

var (
	fieldNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
	commitPattern    = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
)

// ValidCommit reports whether s is a full commit hash: 40 (SHA-1) or 64
// (SHA-256) lowercase hex characters. A short hash or a ref name is refused,
// so evidence always names exactly one commit.
func ValidCommit(s string) bool { return commitPattern.MatchString(s) }

// ModelFamily is the family used to judge independence: the model's vendor,
// as ModelVendor gives it ("anthropic", "deepseek", "openai").
func ModelFamily(harness, model string) string { return ModelVendor(harness, model) }

// IndependenceOf is the value agent-net writes into the independent field of
// evidence done by by (a session ID, EvidenceByOwner or EvidenceByGitHub) with
// model family byFamily, for a change with this origin.
func IndependenceOf(author ChangeOrigin, by, byFamily string) string {
	if by == EvidenceByOwner || by == EvidenceByGitHub || author.Kind == OriginPerson {
		return IndependentFamily
	}
	if by == "" || by == author.SessionID {
		return IndependentNone
	}
	if byFamily != "" && author.ModelFamily != "" && byFamily != author.ModelFamily {
		return IndependentFamily
	}
	return IndependentSession
}

func independentEnough(rule, got string) bool {
	switch rule {
	case IndependentSession:
		return got == IndependentSession || got == IndependentFamily
	case IndependentFamily:
		return got == IndependentFamily
	}
	return true
}

func difficultyRank(level string) int {
	for i, l := range DifficultyLevels {
		if l == level {
			return i
		}
	}
	return -1
}

func validDifficulty(level string) error {
	if difficultyRank(level) < 0 {
		return fmt.Errorf("must be one of %s", strings.Join(DifficultyLevels, ", "))
	}
	return nil
}

func validText(name, s string, max int) error {
	if len(s) > max {
		return fmt.Errorf("%s is longer than %d bytes", name, max)
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s holds a control character", name)
		}
	}
	return nil
}

func validBranch(name, b string) error {
	if b == "" {
		return fmt.Errorf("%s is empty", name)
	}
	if len(b) > MaxBranchBytes {
		return fmt.Errorf("%s is longer than %d bytes", name, MaxBranchBytes)
	}
	if strings.HasPrefix(b, "-") || strings.HasPrefix(b, "/") || strings.HasSuffix(b, "/") || strings.HasSuffix(b, ".lock") || strings.Contains(b, "..") || strings.Contains(b, "@{") {
		return fmt.Errorf("%s %q is not a valid branch name", name, b)
	}
	for _, r := range b {
		if r <= 0x20 || r == 0x7f || strings.ContainsRune(`~^:?*[\`, r) {
			return fmt.Errorf("%s %q is not a valid branch name", name, b)
		}
	}
	return nil
}

// validField checks one evidence field or requirement condition.
func validField(name, value string) error {
	if !fieldNamePattern.MatchString(name) {
		return fmt.Errorf("field name %q must be lowercase letters, digits and _ (at most 40)", name)
	}
	if err := validText(name, value, MaxEvidenceFieldBytes); err != nil {
		return err
	}
	switch name {
	case FieldTestsAdded:
		if value != FieldYes && value != FieldNo {
			return errors.New("tests_added must be yes or no")
		}
	case FieldSuite:
		if value != SuiteChanged && value != SuiteFull {
			return errors.New("suite must be changed or full")
		}
	case FieldFindingsOpen:
		if value == "" || strings.Trim(value, "0123456789") != "" {
			return errors.New("findings_open must be a count: 0 or more")
		}
	case FieldIndependent:
		if value != IndependentNone && value != IndependentSession && value != IndependentFamily {
			return errors.New("independent must be none, session or family")
		}
	case FieldMergedCommit:
		if !ValidCommit(value) {
			return errors.New("merged_commit must be a full commit hash")
		}
	}
	return nil
}

func validFields(fields map[string]string) error {
	if len(fields) > MaxEvidenceFields {
		return fmt.Errorf("at most %d fields", MaxEvidenceFields)
	}
	for _, k := range sortedKeys(fields) {
		if err := validField(k, fields[k]); err != nil {
			return err
		}
	}
	return nil
}

func validResult(r string) error {
	if r != StepResultPass && r != StepResultFail && r != StepResultNeedsOwner {
		return fmt.Errorf("result must be pass, fail or needs_owner, not %q", r)
	}
	return nil
}

func validChangeID(id string) error {
	if !strings.HasPrefix(id, ChangeIDPrefix) || len(id) == len(ChangeIDPrefix) || len(id) > maxNameBytes {
		return fmt.Errorf("change id must start with %q", ChangeIDPrefix)
	}
	return nil
}

// Validate checks an origin.
func (o ChangeOrigin) Validate() error {
	switch o.Kind {
	case OriginWorker:
		if o.WorkerID == "" {
			return errors.New("a worker origin needs worker_id")
		}
	case OriginSession:
		if o.SessionID == "" {
			return errors.New("a session origin needs session_id")
		}
	case OriginPerson:
		if o.Person == "" {
			return errors.New("a person origin needs person")
		}
	default:
		return fmt.Errorf("origin kind must be worker, session or person, not %q", o.Kind)
	}
	return validTexts(maxNameBytes, "worker_id", o.WorkerID, "session_id", o.SessionID, "person", o.Person, "model", o.Model, "model_family", o.ModelFamily)
}

// validTexts checks name, value pairs in order with validText.
func validTexts(max int, pairs ...string) error {
	for i := 0; i+1 < len(pairs); i += 2 {
		if err := validText(pairs[i], pairs[i+1], max); err != nil {
			return err
		}
	}
	return nil
}

// Validate checks a stored change.
func (c Change) Validate() error {
	if err := validChangeID(c.ID); err != nil {
		return err
	}
	if err := c.validateWork(); err != nil {
		return err
	}
	if c.Revision < 1 {
		return errors.New("revision must be 1 or more")
	}
	switch c.State {
	case ChangeOpen, ChangeMerged, ChangeDone, ChangeStopped:
	default:
		return fmt.Errorf("state must be open, merged, done or stopped, not %q", c.State)
	}
	if c.MergedCommit != "" && !ValidCommit(c.MergedCommit) {
		return errors.New("merged_commit must be a full commit hash")
	}
	return nil
}

func (c Change) validateWork() error {
	if strings.TrimSpace(c.Repo) == "" {
		return errors.New("repo is empty")
	}
	if err := validText("repo", c.Repo, maxNameBytes); err != nil {
		return err
	}
	if err := validBranch("branch", c.Branch); err != nil {
		return err
	}
	if c.TargetBranch != "" {
		if err := validBranch("target_branch", c.TargetBranch); err != nil {
			return err
		}
		if c.Branch == c.TargetBranch {
			return fmt.Errorf("branch %s is the target branch: a change needs its own branch", c.Branch)
		}
	}
	if !ValidCommit(c.HeadCommit) {
		return errors.New("head_commit must be a full commit hash")
	}
	if err := c.Origin.Validate(); err != nil {
		return err
	}
	if c.Difficulty != "" {
		if err := validDifficulty(c.Difficulty); err != nil {
			return fmt.Errorf("difficulty %w", err)
		}
	}
	return validText("title", c.Title, MaxChangeTitleBytes)
}

// WithHead returns the change with a new head commit. A different commit makes
// the next revision (bumped is true); the same commit changes nothing. Only an
// open change takes a new head.
func (c Change) WithHead(commit string, at int64) (out Change, bumped bool, err error) {
	if !ValidCommit(commit) {
		return c, false, errors.New("head commit must be a full commit hash")
	}
	if c.State != ChangeOpen {
		return c, false, fmt.Errorf("change %s is %s and takes no new commits; submit a new change", c.ID, c.State)
	}
	if commit == c.HeadCommit {
		return c, false, nil
	}
	c.HeadCommit = commit
	c.Revision++
	c.UpdatedAt = at
	return c, true, nil
}

// Validate checks one piece of evidence as the gateway stores it.
func (e Evidence) Validate() error {
	if err := validChangeID(e.ChangeID); err != nil {
		return err
	}
	if e.Revision < 1 {
		return errors.New("revision must be 1 or more")
	}
	if err := e.StepRef.Validate(); err != nil {
		return err
	}
	if err := validResult(e.Result); err != nil {
		return err
	}
	if !ValidCommit(e.Commit) {
		return errors.New("commit must be a full commit hash")
	}
	if err := validFields(e.Fields); err != nil {
		return err
	}
	if e.By == "" {
		return errors.New("by is empty")
	}
	if err := validTexts(maxNameBytes, "by", e.By, "reported_by", e.ReportedBy, "by_model", e.ByModel, "by_model_family", e.ByModelFamily); err != nil {
		return err
	}
	if len(e.Note) > MaxEvidenceNoteBytes {
		return fmt.Errorf("note is longer than %d bytes", MaxEvidenceNoteBytes)
	}
	return nil
}

// Find returns the pipeline step ref names.
func (p Pipeline) Find(ref StepRef) (PipelineStep, bool) {
	for _, s := range p.Steps {
		if s.StepRef == ref {
			return s, true
		}
	}
	return PipelineStep{}, false
}

// Validate checks a pipeline: known steps, each once, valid owners and rule
// values, requirements only on earlier steps, after_merge exactly for the
// steps after merge, and each approve required by the step it approves.
func (p Pipeline) Validate() error {
	if len(p.Steps) == 0 {
		return errors.New("pipeline has no steps")
	}
	seen := map[StepRef]int{}
	mergeAt := -1
	for i, s := range p.Steps {
		if err := s.StepRef.Validate(); err != nil {
			return err
		}
		if _, dup := seen[s.StepRef]; dup {
			return fmt.Errorf("%s appears twice", s.StepRef)
		}
		if err := s.Owner.validateFor(s.Step); err != nil {
			return fmt.Errorf("%s: %w", s.StepRef, err)
		}
		switch s.Independent {
		case "", IndependentNone, IndependentSession, IndependentFamily:
		default:
			return fmt.Errorf("%s: independent must be none, session or family", s.StepRef)
		}
		switch s.OnFail {
		case "", OnFailCaller, OnFailAuthor:
		default:
			return fmt.Errorf("%s: on_fail must be caller or author", s.StepRef)
		}
		if s.RequiredFrom != "" {
			if err := validDifficulty(s.RequiredFrom); err != nil {
				return fmt.Errorf("%s: required_from %w", s.StepRef, err)
			}
		}
		for _, r := range s.Requires {
			if r.StepRef == s.StepRef {
				return fmt.Errorf("%s requires itself", s.StepRef)
			}
			if _, ok := seen[r.StepRef]; !ok {
				return fmt.Errorf("%s requires %s, which is not earlier in the pipeline", s.StepRef, r.StepRef)
			}
			for _, k := range sortedKeys(r.Fields) {
				if err := validField(k, r.Fields[k]); err != nil {
					return fmt.Errorf("%s requires %s: %w", s.StepRef, r.StepRef, err)
				}
			}
		}
		if s.Step == StepMerge {
			if s.AfterMerge {
				return errors.New("merge cannot run after merge")
			}
			mergeAt = i
		} else if s.AfterMerge && mergeAt < 0 {
			if _, ok := p.Find(StepRef{Step: StepMerge}); ok {
				return fmt.Errorf("%s has after_merge but comes before merge", s.StepRef)
			}
			return fmt.Errorf("%s runs after merge, but the pipeline has no merge step", s.StepRef)
		} else if !s.AfterMerge && mergeAt >= 0 {
			return fmt.Errorf("%s comes after merge, so it needs after_merge", s.StepRef)
		}
		seen[s.StepRef] = i
	}
	for _, target := range []string{StepMerge, StepDeploy} {
		approve := StepRef{Step: StepApprove, For: target}
		if _, ok := p.Find(approve); !ok {
			continue
		}
		s, ok := p.Find(StepRef{Step: target})
		if !ok {
			return fmt.Errorf("%s needs a %s step", approve, target)
		}
		required := false
		for _, r := range s.Requires {
			required = required || r.StepRef == approve
		}
		if !required {
			return fmt.Errorf("%s must require %s", target, approve)
		}
	}
	return nil
}

// leftOut says why a step is left out of this change, or "" when it is not.
// A step below its required_from still counts when it ran (has evidence).
func leftOut(s PipelineStep, c Change, latest *Evidence) string {
	if s.Owner.Kind == OwnerNone {
		return "left out: owner none"
	}
	if s.RequiredFrom != "" && latest == nil && c.Difficulty != "" && difficultyRank(c.Difficulty) >= 0 && difficultyRank(c.Difficulty) < difficultyRank(s.RequiredFrom) {
		return "left out: required from difficulty " + s.RequiredFrom + ", the change is " + c.Difficulty
	}
	return ""
}

func commitFor(s PipelineStep, c Change) string {
	if s.AfterMerge {
		return c.MergedCommit
	}
	return c.HeadCommit
}

func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

// latest returns the step's latest counted evidence on this change's current
// revision and the commit the step runs on, and a note when a newer pass did
// not count because it was not independent enough. The latest is the one with
// the highest At; on a tie, the later in the list.
func latest(s PipelineStep, c Change, evidence []Evidence) (*Evidence, string) {
	best, ignored := -1, -1
	newer := func(i, than int) bool { return than < 0 || evidence[i].At >= evidence[than].At }
	commit := commitFor(s, c)
	for i, e := range evidence {
		if e.ChangeID != c.ID || e.Revision != c.Revision || e.StepRef != s.StepRef || commit == "" || e.Commit != commit {
			continue
		}
		if e.Result == StepResultPass && !independentEnough(s.Independent, e.Fields[FieldIndependent]) {
			if newer(i, ignored) {
				ignored = i
			}
		} else if newer(i, best) {
			best = i
		}
	}
	note := ""
	if ignored >= 0 && (best < 0 || evidence[ignored].At > evidence[best].At || evidence[ignored].At == evidence[best].At && ignored > best) {
		e := evidence[ignored]
		got := e.Fields[FieldIndependent]
		if got == "" {
			got = "not set"
		}
		note = fmt.Sprintf("the pass by %s does not count: %s needs independent=%s, it is %s", e.By, s.StepRef, s.Independent, got)
	}
	if best < 0 {
		return nil, note
	}
	return &evidence[best], note
}

// missing lists what keeps step s from starting on change c.
func (p Pipeline) missing(s PipelineStep, c Change, evidence []Evidence) []string {
	var out []string
	if s.AfterMerge && c.MergedCommit == "" {
		out = append(out, "the merged commit: merge has not passed")
	}
	for _, r := range s.Requires {
		t, _ := p.Find(r.StepRef)
		ev, _ := latest(t, c, evidence)
		if leftOut(t, c, ev) != "" {
			continue
		}
		if t.AfterMerge && c.MergedCommit == "" {
			out = append(out, r.String()+": waits for the merge")
			continue
		}
		on := shortCommit(commitFor(t, c))
		if ev == nil {
			out = append(out, fmt.Sprintf("%s: no pass on %s", r, on))
			continue
		}
		if ev.Result != StepResultPass {
			out = append(out, fmt.Sprintf("%s: %s on %s", r, ev.Result, on))
			continue
		}
		for _, k := range sortedKeys(r.Fields) {
			if got, ok := ev.Fields[k]; !ok {
				out = append(out, fmt.Sprintf("%s: %s is not set, needs %q", r, k, r.Fields[k]))
			} else if got != r.Fields[k] {
				out = append(out, fmt.Sprintf("%s: %s is %q, needs %q", r, k, got, r.Fields[k]))
			}
		}
	}
	return out
}

// Status says where each step of the pipeline stands for change c, in
// pipeline order. Only evidence of c's current revision on the commit each
// step runs on counts.
func (p Pipeline) Status(c Change, evidence []Evidence) []StepStatus {
	out := make([]StepStatus, 0, len(p.Steps))
	for _, s := range p.Steps {
		st := StepStatus{StepRef: s.StepRef, Owner: s.Owner}
		ev, note := latest(s, c, evidence)
		st.Note = note
		if why := leftOut(s, c, ev); why != "" {
			st.State, st.Note = StepStateSkipped, why
		} else if ev != nil {
			e := *ev
			st.Evidence = &e
			switch ev.Result {
			case StepResultPass:
				st.State = StepStatePassed
			case StepResultFail:
				st.State = StepStateFailed
			default:
				st.State = StepStateNeedsOwner
			}
		} else if st.Missing = p.missing(s, c, evidence); len(st.Missing) > 0 {
			st.State = StepStateWaiting
		} else {
			st.State = StepStateReady
		}
		out = append(out, st)
	}
	return out
}

// Ready reports whether the step's requirements hold for change c on the
// exact commit, and if not, what is missing. The merge and deploy checks call
// it at the moment they run.
func (p Pipeline) Ready(ref StepRef, c Change, evidence []Evidence) (bool, []string) {
	s, ok := p.Find(ref)
	if !ok {
		return false, []string{ref.String() + " is not in the pipeline"}
	}
	ev, _ := latest(s, c, evidence)
	if why := leftOut(s, c, ev); why != "" {
		return false, []string{ref.String() + " is " + why}
	}
	missing := p.missing(s, c, evidence)
	return len(missing) == 0, missing
}

// NextSteps returns the steps that may start now for change c, in pipeline
// order. Several may run in parallel. An open change offers only steps before
// merge, a merged change only steps after it, and a done or stopped change
// none. Steps already out as step tasks are the caller's to filter.
func (p Pipeline) NextSteps(c Change, evidence []Evidence) []StepRef {
	if c.State != ChangeOpen && c.State != ChangeMerged {
		return nil
	}
	var out []StepRef
	for i, st := range p.Status(c, evidence) {
		if st.State == StepStateReady && p.Steps[i].AfterMerge == (c.State == ChangeMerged) {
			out = append(out, st.StepRef)
		}
	}
	return out
}

// Accepts checks a step report before the gateway stores it: the evidence is
// valid, for this change, for a step of the pipeline that is not left out,
// on the change's current revision and on the commit that step runs on. It
// does not check Ready (a step may be reported before the merge check
// enforces anything) or independence (a pass that is not independent enough
// is kept, and Status says it does not count).
func (p Pipeline) Accepts(c Change, e Evidence) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if e.ChangeID != c.ID {
		return fmt.Errorf("the report is for change %s, not %s", e.ChangeID, c.ID)
	}
	s, ok := p.Find(e.StepRef)
	if !ok {
		return fmt.Errorf("change %s's pipeline has no step %s", c.ID, e.StepRef)
	}
	if s.Owner.Kind == OwnerNone {
		return fmt.Errorf("%s is left out of change %s's pipeline (owner none)", e.StepRef, c.ID)
	}
	switch {
	case c.State == ChangeOpen && s.AfterMerge:
		return fmt.Errorf("%s runs on the merged commit, and change %s is not merged", e.StepRef, c.ID)
	case c.State == ChangeMerged && !s.AfterMerge:
		return fmt.Errorf("change %s is merged: %s comes before merge", c.ID, e.StepRef)
	case c.State != ChangeOpen && c.State != ChangeMerged:
		return fmt.Errorf("change %s is %s", c.ID, c.State)
	}
	if e.Revision != c.Revision {
		return fmt.Errorf("revision %d is not the current revision %d of change %s", e.Revision, c.Revision, c.ID)
	}
	if want := commitFor(s, c); e.Commit != want {
		if s.AfterMerge {
			return fmt.Errorf("commit %s is not the merged commit %s of change %s", shortCommit(e.Commit), shortCommit(want), c.ID)
		}
		return fmt.Errorf("commit %s is not the head of change %s (head %s, revision %d): check the new head and report again", shortCommit(e.Commit), c.ID, shortCommit(want), c.Revision)
	}
	if e.Step == StepMerge && e.Result == StepResultPass && e.Fields[FieldMergedCommit] == "" {
		return errors.New("a merge pass needs merged_commit: the commit on the target branch")
	}
	return nil
}

// ReportFields lists the evidence fields the step should report: those other
// steps require of it, and merged_commit for merge. It goes into the step task.
func (p Pipeline) ReportFields(ref StepRef) []string {
	set := map[string]string{}
	if ref.Step == StepMerge {
		set[FieldMergedCommit] = ""
	}
	for _, s := range p.Steps {
		for _, r := range s.Requires {
			if r.StepRef == ref {
				for k := range r.Fields {
					set[k] = ""
				}
			}
		}
	}
	return sortedKeys(set)
}

// PipelineFromShorthand builds the pipeline the three step 6 delivery settings
// describe (the mapping table in the design). Every step is owned by the
// caller. The steps are check, review (from difficulty hard, on another model
// family), test, approve for merge, merge, then after merge approve for deploy
// and deploy. The settings say what merge requires and who approves; release
// and verify come from a repo's recipe.
func PipelineFromShorthand(tests, merge, deploy string) (Pipeline, error) {
	approver := map[string]string{MergeFull: OwnerNone, MergeDelegated: OwnerDelegate, MergeAsk: OwnerPerson}
	deployApprover := map[string]string{DeployBrief: OwnerNone, DeployAsk: OwnerDelegate, DeployOwner: OwnerPerson}
	if approver[merge] == "" {
		return Pipeline{}, fmt.Errorf("merge_authority must be full, delegated or ask, not %q", merge)
	}
	if deployApprover[deploy] == "" {
		return Pipeline{}, fmt.Errorf("deploy_rule must be brief, ask or owner, not %q", deploy)
	}
	caller := StepOwner{Kind: OwnerCaller}
	review := PipelineStep{StepRef: StepRef{Step: StepReview}, Owner: caller, Independent: IndependentFamily, RequiredFrom: "hard"}
	reviewReq := Requirement{StepRef: review.StepRef}
	var testReq *Requirement
	switch tests {
	case TestsOptional:
	case TestsSuite:
		testReq = &Requirement{StepRef: StepRef{Step: StepTest}, Fields: map[string]string{FieldSuite: SuiteFull}}
	case TestsSuiteNewTests, TestsSuiteNewTestsReview:
		testReq = &Requirement{StepRef: StepRef{Step: StepTest}, Fields: map[string]string{FieldSuite: SuiteFull, FieldTestsAdded: FieldYes}}
		if tests == TestsSuiteNewTestsReview {
			review.RequiredFrom = ""
			reviewReq.Fields = map[string]string{FieldFindingsOpen: "0"}
		}
	default:
		return Pipeline{}, fmt.Errorf("tests_before_merge must be optional, suite, suite-new-tests or suite-new-tests-review, not %q", tests)
	}
	approveMerge := StepRef{Step: StepApprove, For: StepMerge}
	approveDeploy := StepRef{Step: StepApprove, For: StepDeploy}
	requires := []Requirement{{StepRef: StepRef{Step: StepCheck}}, reviewReq}
	if testReq != nil {
		requires = append(requires, *testReq)
	}
	requires = append(requires, Requirement{StepRef: approveMerge})
	return Pipeline{Steps: []PipelineStep{
		{StepRef: StepRef{Step: StepCheck}, Owner: caller},
		review,
		{StepRef: StepRef{Step: StepTest}, Owner: caller},
		{StepRef: approveMerge, Owner: StepOwner{Kind: approver[merge]}},
		{StepRef: StepRef{Step: StepMerge}, Owner: caller, Requires: requires},
		{StepRef: approveDeploy, Owner: StepOwner{Kind: deployApprover[deploy]}, AfterMerge: true},
		{StepRef: StepRef{Step: StepDeploy}, Owner: caller, AfterMerge: true, Requires: []Requirement{{StepRef: approveDeploy}}},
	}}, nil
}

// PipelineForMaturity is the pipeline a maturity level gives today: the step 6
// settings of DefaultsForMaturity, through PipelineFromShorthand.
func PipelineForMaturity(level string) (Pipeline, error) {
	d := DefaultsForMaturity(level)
	return PipelineFromShorthand(d.TestsBeforeMerge, d.MergeAuthority, d.DeployRule)
}

// PipelineToday is the pipeline of today's behaviour, used when a repo sets
// nothing: every step owned by the caller, no approvals (merge authority full,
// deploy as the brief says), merge after the full suite. It is the default
// maturity level's pipeline. Each call returns a fresh copy.
func PipelineToday() Pipeline {
	p, err := PipelineFromShorthand(TestsSuite, MergeFull, DeployBrief)
	if err != nil {
		panic("protocol: today's pipeline: " + err.Error())
	}
	return p
}

// Shorthand reads the three step 6 delivery settings back from a pipeline:
// each is the value whose part of the pipeline matches exactly, or
// ShorthandCustom. A pipeline without a deploy step reads deploy_rule brief.
func (p Pipeline) Shorthand() (tests, merge, deploy string) {
	tests, merge, deploy = ShorthandCustom, ShorthandCustom, ShorthandCustom
	part := testsPart(p)
	for _, v := range []string{TestsOptional, TestsSuite, TestsSuiteNewTests, TestsSuiteNewTestsReview} {
		q, _ := PipelineFromShorthand(v, MergeFull, DeployBrief)
		if testsPart(q) == part {
			tests = v
		}
	}
	switch approverKind(p, StepMerge) {
	case OwnerNone:
		merge = MergeFull
	case OwnerDelegate:
		merge = MergeDelegated
	case OwnerPerson:
		merge = MergeAsk
	}
	switch approverKind(p, StepDeploy) {
	case OwnerNone:
		deploy = DeployBrief
	case OwnerDelegate:
		deploy = DeployAsk
	case OwnerPerson:
		deploy = DeployOwner
	}
	return tests, merge, deploy
}

// testsPart is the part of a pipeline tests_before_merge speaks about: what
// merge requires of test and review, and whether a required clean review runs
// for every change.
func testsPart(p Pipeline) string {
	m, ok := p.Find(StepRef{Step: StepMerge})
	if !ok {
		return "no merge"
	}
	test, review := "-", "-"
	for _, r := range m.Requires {
		switch r.StepRef {
		case StepRef{Step: StepTest}:
			test = r.String()
		case StepRef{Step: StepReview}:
			review = r.String()
			if _, clean := r.Fields[FieldFindingsOpen]; clean {
				s, _ := p.Find(r.StepRef)
				review += " from " + s.RequiredFrom
			}
		}
	}
	return test + " | " + review
}

// approverKind is the owner kind of the approve step for target: none when
// there is no such step.
func approverKind(p Pipeline, target string) string {
	s, ok := p.Find(StepRef{Step: StepApprove, For: target})
	if !ok {
		return OwnerNone
	}
	return s.Owner.Kind
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Gateway frames of the delivery pipeline. change.submit, change.get,
// change.list and step.report go from a daemon to the gateway; step.task goes
// from the gateway to the daemon of the step owner's session.
const (
	FrameChangeSubmit = "change.submit"
	FrameChangeGet    = "change.get"
	FrameChangeList   = "change.list"
	FrameStepReport   = "step.report"
	FrameStepTask     = "step.task"
)

// Daemon methods behind the MCP tools agent_change_submit, agent_step_report
// and agent_changes_find.
const (
	OpChangeSubmit = "change_submit"
	OpStepReport   = "step_report"
	OpChangesFind  = "changes_find"
)

// ChangeSubmitRequest is change.submit: the daemon sends it when a worker hands
// back with complete: yes (origin worker), or when an agent calls
// agent_change_submit (origin session). SessionID is the session it comes for:
// the worker's caller, or the submitting agent; the gateway checks it is on the
// sending machine. A submit for a branch with an open change returns that
// change, with a new revision when the head moved.
type ChangeSubmitRequest struct {
	SessionID    string       `json:"session_id"`
	Repo         string       `json:"repo"`
	Branch       string       `json:"branch"`
	TargetBranch string       `json:"target_branch,omitempty"`
	HeadCommit   string       `json:"head_commit"`
	Origin       ChangeOrigin `json:"origin"`
	Difficulty   string       `json:"difficulty,omitempty"`
	Title        string       `json:"title,omitempty"`
}

// Validate checks a submit before it is sent.
func (q ChangeSubmitRequest) Validate() error {
	if q.SessionID == "" {
		return errors.New("session_id is empty")
	}
	return Change{Repo: q.Repo, Branch: q.Branch, TargetBranch: q.TargetBranch, HeadCommit: q.HeadCommit, Origin: q.Origin, Difficulty: q.Difficulty, Title: q.Title}.validateWork()
}

// ChangeView is a change with where each of its steps stands. Pipeline,
// Revisions and Evidence (every revision) are filled only by change.get.
type ChangeView struct {
	Change    Change       `json:"change"`
	Steps     []StepStatus `json:"steps"`
	Next      []StepRef    `json:"next,omitempty"`
	Pipeline  *Pipeline    `json:"pipeline,omitempty"`
	Revisions []Revision   `json:"revisions,omitempty"`
	Evidence  []Evidence   `json:"evidence,omitempty"`
}

// ChangeSubmitResult answers change.submit.
type ChangeSubmitResult struct {
	Change ChangeView `json:"change"`
	// Bumped is true when the submit made a new revision of an open change.
	Bumped bool `json:"bumped,omitempty"`
}

// ChangeGetRequest is change.get: one change in full.
type ChangeGetRequest struct {
	ChangeID string `json:"change_id"`
}

// ChangeListRequest is change.list. State is a change state, ChangeListActive
// (the default when empty) or ChangeListAny. Caller keeps the changes whose
// caller is that session. After is the last change ID of the previous page.
type ChangeListRequest struct {
	Repo   string `json:"repo,omitempty"`
	State  string `json:"state,omitempty"`
	Caller string `json:"caller_session_id,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	After  string `json:"after,omitempty"`
}

// ChangeListResult answers change.list, newest change first. Next is the
// After of the next page, empty on the last.
type ChangeListResult struct {
	Changes []ChangeView `json:"changes"`
	Next    string       `json:"next,omitempty"`
}

// StepReportRequest is step.report: a step owner reports a result. SessionID
// is the reporting session (the daemon fills it). DoneBy, when set, is a
// worker the reporter started that did the work; it becomes the evidence's By
// and decides independence. The gateway fills the revision, the model and the
// independent field, then checks the report with Pipeline.Accepts.
type StepReportRequest struct {
	SessionID string `json:"session_id"`
	ChangeID  string `json:"change_id"`
	StepRef
	Result string            `json:"result"`
	Commit string            `json:"commit"`
	Fields map[string]string `json:"fields,omitempty"`
	DoneBy string            `json:"done_by,omitempty"`
	Note   string            `json:"note,omitempty"`
}

// Validate checks a report before it is sent.
func (q StepReportRequest) Validate() error {
	if q.SessionID == "" {
		return errors.New("session_id is empty")
	}
	if err := validChangeID(q.ChangeID); err != nil {
		return err
	}
	if err := q.StepRef.Validate(); err != nil {
		return err
	}
	if err := validResult(q.Result); err != nil {
		return err
	}
	if !ValidCommit(q.Commit) {
		return errors.New("commit must be the full hash of the exact commit you checked")
	}
	if _, set := q.Fields[FieldIndependent]; set {
		return errors.New("independent is set by agent-net, not by the reporter")
	}
	if err := validFields(q.Fields); err != nil {
		return err
	}
	if err := validText("done_by", q.DoneBy, maxNameBytes); err != nil {
		return err
	}
	if len(q.Note) > MaxEvidenceNoteBytes {
		return fmt.Errorf("note is longer than %d bytes", MaxEvidenceNoteBytes)
	}
	return nil
}

// StepReportResult answers step.report with the stored evidence and the
// change after it.
type StepReportResult struct {
	Evidence Evidence   `json:"evidence"`
	Change   ChangeView `json:"change"`
}

// StepTask is step.task: the gateway gives one step of one change to the step
// owner's session, in the same form as a worker notice. Commit is the commit to
// work on (the head, or the merged commit after merge). Evidence is the counted
// evidence so far on this revision. ReportFields are the fields to report.
type StepTask struct {
	TaskID    string `json:"task_id"`
	SessionID string `json:"session_id"`
	Change    Change `json:"change"`
	StepRef
	Owner        StepOwner  `json:"owner"`
	Commit       string     `json:"commit"`
	DiffStat     string     `json:"diff_stat,omitempty"`
	Evidence     []Evidence `json:"evidence,omitempty"`
	ReportFields []string   `json:"report_fields,omitempty"`
}
