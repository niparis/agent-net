package protocol

import (
	"errors"
	"fmt"
	"strings"
)

// The arguments and results of the delivery tools agent_change_submit,
// agent_step_report and agent_changes_find, as their catalog schemas say. The
// MCP server and the Pi and OpenCode plugins send the arguments as they are to
// the daemon methods OpChangeSubmit, OpStepReport and OpChangesFind. The
// daemon fills in what comes from the caller's session and sends the gateway
// frames of delivery_pipeline.go.

// ChangeSubmitArgs are the arguments of agent_change_submit. Commit defaults
// to the head of Branch, Project to the caller's project.
type ChangeSubmitArgs struct {
	Branch       string `json:"branch"`
	Commit       string `json:"commit,omitempty"`
	TargetBranch string `json:"target_branch,omitempty"`
	Project      string `json:"project,omitempty"`
	Change       string `json:"change,omitempty"`
	Title        string `json:"title,omitempty"`
	// Fixes names a shipped change this change fixes; FixesSeverity (1 to 4,
	// default 2) is the defect agent-net records on that change's worker.
	Fixes         string `json:"fixes,omitempty"`
	FixesSeverity int    `json:"fixes_severity,omitempty"`
}

// StepClaimArgs are the arguments of agent_step_claim.
type StepClaimArgs struct {
	Change string `json:"change"`
	StepRef
	Commit string `json:"commit"`
	// Reviewer is a session id. Set it to name that session, for example the
	// CTO or the chief of staff, as the one who reports this accept or review
	// step instead of you. Only the caller may name one. Naming ends with the
	// revision.
	Reviewer string `json:"reviewer,omitempty"`
}

// StepReportArgs are the arguments of agent_step_report. DoneBy defaults to
// the caller; owner means direct owner approval of a delegated step.
type StepReportArgs struct {
	Change string `json:"change"`
	StepRef
	Result string            `json:"result"`
	Commit string            `json:"commit"`
	Fields map[string]string `json:"fields,omitempty"`
	DoneBy string            `json:"done_by,omitempty"`
	Note   string            `json:"note,omitempty"`
	Until  string            `json:"until,omitempty"`
	// Friction lists what slowed this step down (decision 0170).
	Friction []FrictionEntry `json:"friction,omitempty"`
	// Reports is the batch form: several steps reported in one call, each a
	// report of its own. The fields above stay empty when it is set.
	Reports []StepReportItem `json:"reports,omitempty"`
}

// StepReportItem is one report of a batch.
type StepReportItem struct {
	Change string `json:"change"`
	StepRef
	Result string            `json:"result"`
	Commit string            `json:"commit"`
	Fields map[string]string `json:"fields,omitempty"`
	DoneBy string            `json:"done_by,omitempty"`
	Note   string            `json:"note,omitempty"`
	Until  string            `json:"until,omitempty"`
	// Friction lists what slowed this step down (decision 0170).
	Friction []FrictionEntry `json:"friction,omitempty"`
}

// Single is the item as the arguments of a single report.
func (i StepReportItem) Single() StepReportArgs {
	return StepReportArgs{Change: i.Change, StepRef: i.StepRef, Result: i.Result, Commit: i.Commit, Fields: i.Fields, DoneBy: i.DoneBy, Note: i.Note, Until: i.Until, Friction: i.Friction}
}

// IsBatch says the call uses the batch form. A call that sets both the batch
// and the fields of a single report is ambiguous and refused.
func (a StepReportArgs) IsBatch() (bool, error) {
	if len(a.Reports) == 0 {
		return false, nil
	}
	if a.Change != "" || a.Step != "" || a.Result != "" || a.Commit != "" || a.For != "" || a.AfterMerge || a.DoneBy != "" || a.Note != "" || a.Until != "" || len(a.Fields) > 0 || len(a.Friction) > 0 {
		return false, errors.New("send either one report (change, step, result, commit) or reports, not both")
	}
	return true, nil
}

// CTOStepReportArgs are the arguments of the CTO's step_report op. The CTO
// reports as itself: there is no done_by. The gateway accepts the report only
// for a step whose caller named the CTO as its reviewer.
type CTOStepReportArgs struct {
	Change string `json:"change"`
	StepRef
	Result string            `json:"result"`
	Commit string            `json:"commit"`
	Fields map[string]string `json:"fields,omitempty"`
	Note   string            `json:"note,omitempty"`
	// Friction lists what slowed this step down (decision 0170).
	Friction []FrictionEntry `json:"friction,omitempty"`
}

// DeliveryOwnerAnswerRequest is the CTO's cto_op delivery_owner_answer: it
// completes an @owner step, or an owner merge approval, on the owner's quoted
// words (decision 0196). Result is pass when the owner approves, fail when he
// refuses.
type DeliveryOwnerAnswerRequest struct {
	Change string `json:"change" jsonschema:"the change id"`
	StepRef
	Result     string            `json:"result" jsonschema:"pass when the owner approves, fail when he refuses"`
	Commit     string            `json:"commit" jsonschema:"the full commit hash the owner approved"`
	Fields     map[string]string `json:"fields,omitempty" jsonschema:"evidence fields for the step, such as the merge commit"`
	Note       string            `json:"note,omitempty" jsonschema:"a short note from the owner's answer"`
	OwnerWords string            `json:"owner_words" jsonschema:"a short quote of the owner's instruction in this chat"`
}

// DeliveryOwnerAnswerFrame is FrameDeliveryOwnerAnswer. Owner is the owner
// session the gateway checks, Session is the CTO session that reported, and
// WordsVerified says the daemon found the quote in the CTO chat.
type DeliveryOwnerAnswerFrame struct {
	Request       DeliveryOwnerAnswerRequest `json:"request"`
	Owner         string                     `json:"owner"`
	Session       string                     `json:"session,omitempty"`
	By            string                     `json:"by,omitempty"`
	WordsVerified bool                       `json:"words_verified,omitempty"`
}

// Validate checks an owner answer before the daemon sends it.
func (a DeliveryOwnerAnswerRequest) Validate() error {
	if err := validChangeID(a.Change); err != nil {
		return err
	}
	if err := a.StepRef.Validate(); err != nil {
		return err
	}
	if a.Result != StepResultPass && a.Result != StepResultFail {
		return errors.New("result must be pass or fail")
	}
	if !ValidCommit(a.Commit) {
		return errors.New("commit must be the full hash of the exact commit the owner approved")
	}
	if strings.TrimSpace(a.OwnerWords) == "" {
		return errors.New("owner_words is required: quote the owner's instruction from this chat")
	}
	if len(a.Note) > MaxEvidenceNoteBytes {
		return fmt.Errorf("note is longer than %d bytes", MaxEvidenceNoteBytes)
	}
	return nil
}

// OpChangesOwnerFind is the owner's read of the same data, for the TUI: it
// takes ChangesFindArgs and answers ChangesFindResult, for every change of the
// network and without a session. Only the owner's command line or TUI may call
// it; it is read only.
const OpChangesOwnerFind = "changes_owner_find"

// OpChangesOwnerReport is the owner's report of a To try check from the TUI:
// it takes ChangesOwnerReportArgs and answers StepReportResult. Only the
// owner's TUI may call it, and it only reports the verify step after merge.
const OpChangesOwnerReport = "changes_owner_report"

// ChangesOwnerReportArgs are the arguments of changes_owner_report. Result is
// pass when the owner saw the change work, fail when it did not.
type ChangesOwnerReportArgs struct {
	Change string `json:"change"`
	Result string `json:"result"`
	Note   string `json:"note,omitempty"`
}

// Validate checks a report before the daemon resolves the change.
func (a ChangesOwnerReportArgs) Validate() error {
	if err := validChangeID(a.Change); err != nil {
		return err
	}
	if a.Result != StepResultPass && a.Result != StepResultFail {
		return errors.New("result must be pass or fail")
	}
	if a.Result == StepResultFail && strings.TrimSpace(a.Note) == "" {
		return errors.New("a failed check needs a note: say what is wrong")
	}
	if len(a.Note) > MaxEvidenceNoteBytes {
		return fmt.Errorf("note is longer than %d bytes", MaxEvidenceNoteBytes)
	}
	return nil
}

// Values of ChangesFindArgs.Caller.
const (
	ChangesCallerMe     = "me"
	ChangesCallerAnyone = "anyone"
)

// ChangesFindArgs are the arguments of agent_changes_find. Change reads one
// change in full; the other fields filter and page the list.
type ChangesFindArgs struct {
	Change  string `json:"change,omitempty"`
	Project string `json:"project,omitempty"`
	State   string `json:"state,omitempty"`
	Caller  string `json:"caller,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	Cursor  string `json:"cursor,omitempty"`
	// IfVersion is the changes topic version the caller already holds. An
	// unchanged topic answers NotModified and no rows.
	IfVersion uint64 `json:"if_version,omitempty"`
	// IfETag and WantETag work as in QueryAgents.
	IfETag   string `json:"if_etag,omitempty"`
	WantETag bool   `json:"want_etag,omitempty"`
	// Lite asks for list rows only: long evidence notes and fields are left
	// out, and the item is read in full when it is opened. The TUI sets it.
	Lite bool `json:"lite,omitempty"`
}

// ChangesFindResult is the result of agent_changes_find. Total counts the
// items of this page; NextCursor is set when there are more.
type ChangesFindResult struct {
	Items      []ChangeView `json:"items"`
	Total      int          `json:"total"`
	NextCursor string       `json:"next_cursor,omitempty"`
	Note       string       `json:"note,omitempty"`
	// NotModified means the caller's IfVersion or IfETag already matches, so
	// Items holds no rows and the caller keeps its copy. ETag is the hash of
	// this page, set when the caller asked for it.
	NotModified bool   `json:"not_modified,omitempty"`
	ETag        string `json:"etag,omitempty"`
}
