package protocol

import (
	"errors"
	"strings"
	"time"
)

// The CTO intake queue (decision 0152). System-health alerts go here, not to
// the owner. One entry per kind and subject: a repeat raises its count and
// last seen time. The CTO reads the open entries at the start of each owner
// conversation with cto_intake_find.

// Intake kinds.
const (
	// IntakeMachineSilent: a machine stayed offline without a sleep report
	// for 15 minutes. Subject: the machine.
	IntakeMachineSilent = "machine_silent"
	// IntakeHarnessUpdate: a harness update failed. Subject: machine/harness.
	IntakeHarnessUpdate = "harness_update_failed"
	// IntakeCompaction: an automatic compaction failed. Subject:
	// machine/harness.
	IntakeCompaction = "compaction_failed"
	// IntakeMCPRepair: the daemon could not repair an MCP server entry.
	// Subject: machine/server.
	IntakeMCPRepair = "mcp_repair_failed"
	// IntakeStruggle: several workers struggled on one model. Subject: the
	// model.
	IntakeStruggle = "struggle"
	// IntakeMergeQueue: the merger's train tip failed its test run K times in
	// a row. Subject: the project.
	IntakeMergeQueue = "merge_queue_failing"
	// IntakeQuota: a provider reached the worker floor or ran out. Subject:
	// the provider.
	IntakeQuota = "quota"
	// IntakePickerOverride: a held picker override (decision 0151). Digest
	// only: the chief confirms it first.
	IntakePickerOverride = "picker_override"
	// IntakeClaudeMouse: the Claude Code mouse notice. Digest only.
	IntakeClaudeMouse = "claude_mouse"
	// IntakePhonePaired: a phone paired by Tailscale sign-in. Digest only.
	IntakePhonePaired = "phone_paired"
)

// Intake severities. An entry starts as an alert or a digest line by its
// kind, and becomes a wake when its wake rule holds.
const (
	IntakeSeverityWake   = 1
	IntakeSeverityAlert  = 2
	IntakeSeverityDigest = 3
)

// Intake thresholds of decision 0152. They are code constants, as 0152 fixes
// them.
const (
	// IntakeRepeatWake: an alert that repeats for longer than this wakes the
	// CTO.
	IntakeRepeatWake = time.Hour
	// MergeQueueFailRuns is K: failed test runs in a row on the train tip.
	MergeQueueFailRuns = 3
	// StruggleWorkers is how many distinct workers must struggle on one model
	// within StruggleWindow before it is a pattern across workers.
	StruggleWorkers = 3
	StruggleWindow  = 6 * time.Hour
)

// DaemonIntakeKind says whether a daemon may report this kind. The gateway
// detects the others itself.
func DaemonIntakeKind(kind string) bool {
	switch kind {
	case IntakeHarnessUpdate, IntakeCompaction, IntakeMCPRepair, IntakeClaudeMouse:
		return true
	}
	return false
}

// IntakeReport is what a daemon sends with intake.report. The gateway adds the
// machine to the subject. Resolved closes the open entry of the kind and
// subject instead.
type IntakeReport struct {
	Kind     string `json:"kind"`
	Subject  string `json:"subject"`
	Text     string `json:"text"`
	Detail   string `json:"detail,omitempty"`
	Resolved bool   `json:"resolved,omitempty"`
}

// Validate checks a daemon report and names the field that is wrong.
func (r IntakeReport) Validate() error {
	if !DaemonIntakeKind(r.Kind) {
		return errors.New("kind must be harness_update_failed, compaction_failed, mcp_repair_failed or claude_mouse")
	}
	if strings.TrimSpace(r.Subject) == "" {
		return errors.New("subject is required: the harness or the server")
	}
	if !r.Resolved && strings.TrimSpace(r.Text) == "" {
		return errors.New("text is required")
	}
	if len(r.Text) > MaxBodyBytes || len(r.Detail) > MaxBodyBytes {
		return errors.New("text too long")
	}
	return nil
}

// IntakeEntry is one row of the CTO intake queue.
type IntakeEntry struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Subject   string    `json:"subject"`
	Severity  int       `json:"severity"`
	Text      string    `json:"text"`
	Detail    string    `json:"detail,omitempty"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Count     int       `json:"count"`
	// Alert is the chief's P1 "CTO alert" item, once the entry woke the CTO.
	Alert string `json:"alert,omitempty"`
	// Stuck is the chief's stuck-work item that links this entry (quota).
	Stuck string `json:"stuck,omitempty"`
	// Lead names the lead that owns the cause.
	Lead string `json:"lead,omitempty"`
}

// The CTO tool cto_intake_find and its gateway frame.
const FrameIntakeFind = "cto.intake"

// IntakeFindArgs are the tool's arguments.
type IntakeFindArgs struct {
	Limit int `json:"limit,omitempty"`
}

// IntakeFindResult is the open entries, newest first.
type IntakeFindResult struct {
	Entries []IntakeEntry `json:"entries"`
	Total   int           `json:"total"`
}
