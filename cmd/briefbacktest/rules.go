package main

import (
	"regexp"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// The six brief rules, in the order the live check reports them. Each key
// maps to one item of the worker brief format.
const (
	ruleGoal     = "goal"
	ruleFiles    = "files"
	ruleLimits   = "limits"
	ruleDone     = "done"
	ruleGraded   = "graded"
	ruleEvidence = "evidence"
)

var rules = []string{ruleGoal, ruleFiles, ruleLimits, ruleDone, ruleGraded, ruleEvidence}

// ruleItem is the item name that protocol.CheckWorkerBrief puts before the
// colon of each failure line.
var ruleItem = map[string]string{
	ruleGoal:     protocol.BriefItemGoal,
	ruleFiles:    protocol.BriefItemFiles,
	ruleLimits:   protocol.BriefItemLimits,
	ruleDone:     protocol.BriefItemDone,
	ruleGraded:   protocol.BriefItemGraded,
	ruleEvidence: protocol.BriefItemEvidence,
}

// strictRules runs the live text check on a task and returns the rules it
// fails. Lines that match no rule are returned as unmapped: the overlap check
// is not replayed, so an unmapped line is a sign that the check changed.
func strictRules(task string) (failed []string, unmapped []string) {
	return failedRules(protocol.CheckWorkerBrief(task))
}

// failedRules maps the failure lines of protocol.CheckWorkerBrief to rule keys.
func failedRules(lines []string) (failed []string, unmapped []string) {
	seen := map[string]bool{}
	for _, line := range lines {
		matched := false
		for _, rule := range rules {
			if strings.HasPrefix(line, ruleItem[rule]+":") {
				seen[rule] = true
				matched = true
				break
			}
		}
		if !matched {
			unmapped = append(unmapped, line)
		}
	}
	for _, rule := range rules {
		if seen[rule] {
			failed = append(failed, rule)
		}
	}
	return failed, unmapped
}

// Candidate looser matchers. They only ever release a brief that the live
// check refused: a rule the live check passes stays passed. They test whether
// the same content written in prose, with other headings or synonyms, still
// separates good briefs from bad ones.
var (
	looseGoalWords     = regexp.MustCompile(`(?i)\b(owner|lead|cto|asked|requested|request|message|user|operator)\b|\bm_[0-9a-z]{6,}\b|"[^"\n]{2,}"`)
	looseLimitsLine    = regexp.MustCompile(`(?i)^\W*(limits?|constraints?|scope|out of scope|boundaries|never|must not|do not)\b`)
	looseLimitsNeg     = regexp.MustCompile(`(?i)\b(do not|don't|never|must not)\b.*\b(touch|edit|change|modify|push|merge|commit|run|delete|install|write)`)
	looseDoneWords     = regexp.MustCompile(`(?i)(done when|done means|done if|done is|acceptance|finished when|complete when|complete means|definition of done|the task is done|success when|pass when)`)
	looseGradedWords   = regexp.MustCompile(`(?i)\b(graded?|grading|judged?|judge|reviewed by|checked by|verified by|scored|scoring|measured by)\b|how (it|this|you) (will be|is|are) (checked|judged|scored|reviewed)`)
	looseEvidenceWords = regexp.MustCompile(`(?i)\b(evidence|observed|found|logs?|reproduc\w*|verified|saw|measured|facts?)\b`)
	bulletLine         = regexp.MustCompile(`^\s*([-*+]|\d+[.)])\s+\S`)
)

// looseRules returns the rules that fail under the candidate matchers: the
// rules the live check fails and no looser matcher accepts. Files has no
// looser matcher: a brief must name a path or a file in both versions.
func looseRules(task string, strict []string) []string {
	var still []string
	for _, rule := range strict {
		if !looseSatisfied(rule, task) {
			still = append(still, rule)
		}
	}
	return still
}

func looseSatisfied(rule, task string) bool {
	switch rule {
	case ruleGoal:
		return looseGoalWords.MatchString(firstLines(task, 3))
	case ruleLimits:
		for _, line := range strings.Split(task, "\n") {
			if looseLimitsLine.MatchString(line) || looseLimitsNeg.MatchString(line) {
				return true
			}
		}
		return false
	case ruleDone:
		return looseDoneWords.MatchString(task)
	case ruleGraded:
		return looseGradedWords.MatchString(task)
	case ruleEvidence:
		bullets := 0
		for _, line := range strings.Split(task, "\n") {
			if bulletLine.MatchString(line) {
				bullets++
			}
		}
		return bullets >= 3 && looseEvidenceWords.MatchString(task)
	}
	return false
}

// firstLines returns the first n non-empty lines of text, joined.
func firstLines(text string, n int) string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
		if len(out) == n {
			break
		}
	}
	return strings.Join(out, "\n")
}
