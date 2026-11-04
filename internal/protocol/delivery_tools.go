package protocol

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
}

// StepClaimArgs are the arguments of agent_step_claim.
type StepClaimArgs struct {
	Change string `json:"change"`
	StepRef
	Commit string `json:"commit"`
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
}

// OpChangesOwnerFind is the owner's read of the same data, for the TUI: it
// takes ChangesFindArgs and answers ChangesFindResult, for every change of the
// network and without a session. Only the owner's command line or TUI may call
// it; it is read only.
const OpChangesOwnerFind = "changes_owner_find"

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
}

// ChangesFindResult is the result of agent_changes_find. Total counts the
// items of this page; NextCursor is set when there are more.
type ChangesFindResult struct {
	Items      []ChangeView `json:"items"`
	Total      int          `json:"total"`
	NextCursor string       `json:"next_cursor,omitempty"`
	Note       string       `json:"note,omitempty"`
}
