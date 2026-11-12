package protocol

import (
	"fmt"
	"strings"
)

// The bar is a project's own list of short rules. Workers read it in their
// brief and check their diff against it; the accept and review steps read the
// same list. It lives in the gateway, so a rule the owner or the project's
// lead writes once is seen by every worker and reviewer of that project.

// Where a bar rule came from.
const (
	BarSourceSeeded = "seeded" // drafted from AGENTS.md when the project had no bar
	BarSourceOwner  = "owner"  // the owner, or the CTO for the owner
	BarSourceLead   = "lead"   // the project's lead
)

// MaxBarRuleLen is the longest rule, in characters. A rule is one line.
const MaxBarRuleLen = 400

// BarRule is one rule of a project's bar. IDs are B1, B2, ... and are never
// reused after a rule is removed.
type BarRule struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	CreatedAt int64  `json:"created_at,omitempty"`
	Source    string `json:"source,omitempty"`
}

// BarEdit changes the text of one rule.
type BarEdit struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// BarEditRequest is agent_bar_edit. One call may add, edit and remove rules,
// so a lead seeds a whole bar with one call. Project names the project for the
// owner and the CTO; a lead always edits its own project.
type BarEditRequest struct {
	Project string    `json:"project,omitempty"`
	Add     []string  `json:"add,omitempty"`
	Edit    []BarEdit `json:"edit,omitempty"`
	Remove  []string  `json:"remove,omitempty"`
}

// BarEditFrame is the daemon frame "bar.edit". The daemon adds the caller.
type BarEditFrame struct {
	BarEditRequest
	SessionID string `json:"session_id"`
}

// BarEditResult answers agent_bar_edit with the bar after the change.
type BarEditResult struct {
	Project string    `json:"project"`
	Rules   []BarRule `json:"rules"`
}

// CleanBarRule trims a rule to one line and checks its length.
func CleanBarRule(text string) (string, error) {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "", fmt.Errorf("a bar rule is empty")
	}
	if n := len([]rune(text)); n > MaxBarRuleLen {
		return "", fmt.Errorf("a bar rule is %d characters; the limit is %d", n, MaxBarRuleLen)
	}
	return text, nil
}

// BarBriefSection is the section of the worker brief that lists the bar. It is
// empty when the project has no rules.
func BarBriefSection(rules []BarRule) string {
	if len(rules) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Project rules (check your diff against each before you hand back; list each as met or not applicable in your report):")
	for _, r := range rules {
		b.WriteString("\n- " + r.ID + ": " + NeutralizeMarkers(r.Text))
	}
	return b.String()
}

// BarStepSection is the part of an accept or review task that lists the bar.
// It is empty when the project has no rules.
func BarStepSection(rules []BarRule) string {
	if len(rules) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Project rules (check the diff against each):")
	for _, r := range rules {
		b.WriteString("\n" + r.ID + ": " + NeutralizeMarkers(r.Text))
	}
	return b.String()
}
