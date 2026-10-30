package protocol

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Findings and the automatic playbook (decision 0022, knowledge). An agent
// records what it noticed about a model, a harness, a project, an MCP server or
// a machine. Findings are proposals: the gateway promotes a pattern to a
// playbook note on its own. They are never messages and never reach Needs you.

// Subject kinds of a finding.
const (
	SubjectModel        = "model"
	SubjectHarness      = "harness"
	SubjectModelHarness = "model_harness"
	SubjectProject      = "project"
	SubjectMCP          = "mcp"
	SubjectMachine      = "machine"
)

// Finding statuses.
const (
	FindingNew       = "new"
	FindingPromoted  = "promoted"
	FindingDismissed = "dismissed"
	FindingStale     = "stale"
)

// Finding limits.
const (
	FindingMaxBytes        = 500
	FindingSubjectMaxBytes = 200
	FindingEvidenceMax     = 20
	FindingEvidenceBytes   = 100
	FindingsPerSessionDay  = 3
	FindingStaleDays       = 90
	FindingWindowDays      = 30
	PlaybookStaleDays      = 60
)

// Finding is one thing an agent learned, as the gateway stores it.
type Finding struct {
	ID          int64    `json:"id,omitempty"`
	SubjectKind string   `json:"subject_kind"`
	Subject     string   `json:"subject"`
	Finding     string   `json:"finding"`
	Evidence    []string `json:"evidence,omitempty"`
	Reporter    string   `json:"reporter,omitempty"`
	NodeID      string   `json:"node_id,omitempty"`
	At          int64    `json:"at,omitempty"`
	Status      string   `json:"status,omitempty"`
}

// LearnRequest is agent_learn: one finding from one agent session.
type LearnRequest struct {
	SubjectKind string   `json:"subject_kind"`
	Subject     string   `json:"subject"`
	Finding     string   `json:"finding"`
	Evidence    []string `json:"evidence,omitempty"`
}

// LearnFrame is "learn.add", daemon to gateway: the finding and the session
// that reports it. The gateway checks the session belongs to the sending node.
type LearnFrame struct {
	LearnRequest
	Reporter string `json:"reporter_session"`
}

// LearnResult answers agent_learn. Accepted is false when the session reached
// its daily limit; Reason then says why.
type LearnResult struct {
	Accepted  bool   `json:"accepted"`
	ID        int64  `json:"id,omitempty"`
	Status    string `json:"status,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Remaining int    `json:"remaining"`
}

// CleanFinding checks a finding and returns it with cleaned text.
func CleanFinding(kind, subject, finding string, evidence []string) (ReportingFinding, error) {
	var out ReportingFinding
	kind = strings.TrimSpace(kind)
	switch kind {
	case SubjectModel, SubjectHarness, SubjectModelHarness, SubjectProject, SubjectMCP, SubjectMachine:
	default:
		return out, fmt.Errorf("subject_kind must be model, harness, model_harness, project, mcp or machine")
	}
	subject = strings.TrimSpace(subject)
	if subject == "" || len(subject) > FindingSubjectMaxBytes || hasControl(subject) {
		return out, fmt.Errorf("subject must be 1 to %d printable characters", FindingSubjectMaxBytes)
	}
	if kind == SubjectModelHarness {
		if _, _, ok := ParseModelHarness(subject); !ok {
			return out, fmt.Errorf("a model_harness subject is model@harness, for example terra@pi")
		}
	}
	finding = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, finding)
	finding = strings.TrimSpace(finding)
	if finding == "" {
		return out, fmt.Errorf("a finding needs one sentence")
	}
	if len(finding) > FindingMaxBytes {
		return out, fmt.Errorf("a finding is at most %d bytes", FindingMaxBytes)
	}
	cleanEvidence := make([]string, 0, min(len(evidence), FindingEvidenceMax))
	for _, e := range evidence {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if len(e) > FindingEvidenceBytes {
			e = strings.ToValidUTF8(e[:FindingEvidenceBytes], "")
		}
		e = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, e)
		if e != "" {
			cleanEvidence = append(cleanEvidence, e)
		}
		if len(cleanEvidence) == FindingEvidenceMax {
			break
		}
	}
	out = ReportingFinding{SubjectKind: kind, Subject: subject, Finding: finding, Evidence: cleanEvidence}
	return out, nil
}

// ReportingFinding is a cleaned finding before it has a reporter and a time.
type ReportingFinding struct {
	SubjectKind string
	Subject     string
	Finding     string
	Evidence    []string
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// ParseModelHarness reads a model_harness subject: "model@harness".
func ParseModelHarness(subject string) (model, harness string, ok bool) {
	i := strings.LastIndex(subject, "@")
	if i <= 0 || i == len(subject)-1 {
		return "", "", false
	}
	model, harness = strings.TrimSpace(subject[:i]), strings.TrimSpace(subject[i+1:])
	if model == "" || harness == "" || hasControl(model) || hasControl(harness) {
		return "", "", false
	}
	return model, harness, true
}

// NoteKey is the playbook key a finding supports.
func (f Finding) NoteKey() (kind, subject, harness string) {
	switch f.SubjectKind {
	case SubjectModelHarness:
		model, h, _ := ParseModelHarness(f.Subject)
		return SubjectModelHarness, model, h
	default:
		return f.SubjectKind, f.Subject, ""
	}
}

// Age is how long ago the finding was recorded.
func (f Finding) Age(now time.Time) time.Duration { return now.Sub(time.Unix(f.At, 0)) }
