package protocol

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// The model track record and the playbook (decision 0022, step 5b). The
// gateway counts the runs its machines report, per harness and model, and the
// owner can write a note about a model. Both are shown where a model is
// chosen: in agent_nodes and in the agent_start result.

// TrackRecordDays is how long the gateway keeps worker runs and counts them.
const TrackRecordDays = 30

// Outcomes of a worker run in the track record.
const (
	OutcomeComplete = "complete"
	OutcomePartial  = "partial"
	OutcomeFailed   = "failed"
	OutcomeTimedOut = "timed_out"
)

// WorkerRun is one ended worker as the gateway counts it.
type WorkerRun struct {
	NodeID    string `json:"node_id"`
	SessionID string `json:"session_id,omitempty"`
	Harness   string `json:"harness"`
	Model     string `json:"model,omitempty"`
	Outcome   string `json:"outcome"`
	// StartedAt and EndedAt are unix seconds.
	StartedAt int64 `json:"started_at,omitempty"`
	EndedAt   int64 `json:"ended_at"`
	// RunSeconds is how long the worker ran. 0 when the start is unknown.
	RunSeconds int64 `json:"run_seconds,omitempty"`
}

// TrackRecord is what the gateway learned about one harness and model.
type TrackRecord struct {
	Harness          string `json:"harness"`
	Model            string `json:"model,omitempty"`
	Runs             int    `json:"runs"`
	Complete         int    `json:"complete"`
	Partial          int    `json:"partial"`
	Failed           int    `json:"failed"`
	TimedOut         int    `json:"timed_out"`
	MedianRunSeconds int64  `json:"median_run_seconds,omitempty"`
	// Resumable is true when a harness can open an ended session again.
	Resumable bool `json:"resumable"`
}

// WorkerOutcome maps why a worker ended and its hand-back to an outcome. A
// worker that says complete:false counts as partial, whatever its status.
func WorkerOutcome(reason, status string, complete *bool) string {
	if complete != nil && !*complete {
		return OutcomePartial
	}
	switch status {
	case StatusDone:
		return OutcomeComplete
	case StatusBlocked, StatusFailed:
		return OutcomeFailed
	}
	switch reason {
	case StopHandback:
		// A daemon from before the complete field sends no status: its
		// hand-back counted as complete.
		return OutcomeComplete
	case StopIdle, StopMaxAge:
		return OutcomeTimedOut
	}
	return OutcomeFailed
}

// HarnessCanResume tells whether a harness can open an ended session again.
// Pi cannot: its rpc mode has no resume.
func HarnessCanResume(harness string) bool { return harness != "pi" }

// Line writes the track record as the design shows it, for example
// "terra · pi: 4 runs: 1 complete, 3 partial (median 9 min) · cannot resume".
func (t *TrackRecord) Line() string {
	if t == nil {
		return ""
	}
	model := strings.TrimSpace(t.Model)
	if model == "" {
		model = "default"
	}
	var parts []string
	for _, n := range []struct {
		count int
		word  string
	}{{t.Complete, "complete"}, {t.Partial, "partial"}, {t.Failed, "failed"}, {t.TimedOut, "timed out"}} {
		if n.count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n.count, n.word))
		}
	}
	label := model
	if t.Harness != "" {
		label += " · " + t.Harness
	}
	var b strings.Builder
	b.WriteString(label + ": ")
	if t.Runs == 0 {
		b.WriteString("no runs yet")
	} else {
		word := "runs"
		if t.Runs == 1 {
			word = "run"
		}
		b.WriteString(fmt.Sprintf("%d %s", t.Runs, word))
		if len(parts) > 0 {
			b.WriteString(": " + strings.Join(parts, ", "))
		}
		if t.MedianRunSeconds > 0 {
			b.WriteString(" (median " + shortDuration(time.Duration(t.MedianRunSeconds)*time.Second) + ")")
		}
	}
	if t.Resumable {
		b.WriteString(" · resumes")
	} else {
		b.WriteString(" · cannot resume")
	}
	return b.String()
}

// shortDuration writes a run time as the track record shows it: "45 s",
// "2 min", "1 h".
func shortDuration(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%d h", int((d+30*time.Minute)/time.Hour))
	case d >= time.Minute:
		return fmt.Sprintf("%d min", int((d+30*time.Second)/time.Minute))
	}
	return fmt.Sprintf("%d s", int((d+500*time.Millisecond)/time.Second))
}

// PlaybookNoteMaxBytes is the longest playbook note the owner may write.
const PlaybookNoteMaxBytes = 1024

// PlaybookAutoMaxBytes bounds a note the gateway builds from findings.
const PlaybookAutoMaxBytes = 2048

// How a playbook note was made.
const (
	PlaybookSourceAuto  = "auto"
	PlaybookSourceOwner = "owner"
	PlaybookSourceCTO   = "cto"
)

// PlaybookNote is one note of the playbook. A model note has SubjectKind model
// (or model_harness, with Harness); a harness, project, mcp or machine note
// names its subject directly. An automatic note carries its support count and
// the track record line behind it.
type PlaybookNote struct {
	SubjectKind string `json:"subject_kind"`
	Subject     string `json:"subject"`
	Harness     string `json:"harness,omitempty"`
	Note        string `json:"note"`
	Source      string `json:"source,omitempty"`
	Support     int    `json:"support,omitempty"`
	TrackLine   string `json:"track_line,omitempty"`
	Pinned      bool   `json:"pinned,omitempty"`
	CreatedAt   int64  `json:"created_at,omitempty"`
	UpdatedAt   int64  `json:"updated_at,omitempty"`
}

// PlaybookSetRequest is OpPlaybookSet. An empty note removes the entry.
// Pin, when set, also pins (or unpins) the note.
type PlaybookSetRequest struct {
	Model   string `json:"model"`
	Harness string `json:"harness,omitempty"`
	Note    string `json:"note"`
	Pin     *bool  `json:"pin,omitempty"`
}

// PlaybookGetRequest is OpPlaybookGet.
type PlaybookGetRequest struct {
	Model   string `json:"model"`
	Harness string `json:"harness,omitempty"`
}

// PlaybookGetResult answers OpPlaybookGet. No note means nothing is stored.
type PlaybookGetResult struct {
	Note *PlaybookNote `json:"note,omitempty"`
}

// CleanPlaybook trims and checks the key and the note. The note keeps its
// newlines, loses control characters, and is at most PlaybookNoteMaxBytes.
func CleanPlaybook(model, harness, note string) (string, string, string, error) {
	model = strings.TrimSpace(model)
	harness = strings.TrimSpace(harness)
	if model == "" {
		return "", "", "", fmt.Errorf("a playbook note needs a model")
	}
	if len(model) > FindingSubjectMaxBytes || len(harness) > 40 {
		return "", "", "", fmt.Errorf("the model or harness name is too long")
	}
	for _, s := range []string{model, harness} {
		if hasControl(s) {
			return "", "", "", fmt.Errorf("the model and harness names have no control characters")
		}
	}
	note = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, note)
	note = strings.TrimSpace(note)
	if len(note) > PlaybookNoteMaxBytes {
		return "", "", "", fmt.Errorf("a playbook note is at most %d bytes", PlaybookNoteMaxBytes)
	}
	return model, harness, note, nil
}

// CleanPlaybookText removes control characters other than newlines and tabs,
// trims the text and cuts it to max bytes.
func CleanPlaybookText(note string, max int) string {
	note = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, note)
	note = strings.TrimSpace(note)
	if len(note) > max {
		note = strings.ToValidUTF8(note[:max], "")
	}
	return note
}

// Line writes the playbook note for a start result or a machine list.
func (p *PlaybookNote) Line() string {
	if p == nil || strings.TrimSpace(p.Note) == "" {
		return ""
	}
	head := "playbook"
	if p.Subject != "" {
		head += " [" + p.Subject
		if p.Harness != "" {
			head += " · " + p.Harness
		}
		head += "]"
	}
	if p.Support > 0 {
		head += fmt.Sprintf(" %d findings", p.Support)
	}
	if p.Pinned {
		head += ", pinned"
	}
	return head + ": " + strings.TrimSpace(p.Note)
}
