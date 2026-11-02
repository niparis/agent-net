// Package difficulty validates evidence profiles and converts judge scores to
// the worker-picker difficulty labels.
package difficulty

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
)

// DimensionRubric defines the evidence to report and the anchor scores.
type DimensionRubric struct {
	Name     string
	Evidence string
	Anchors  [3]string
	// Examples are real tasks at scores 1, 3 and 5, to calibrate the judge.
	Examples [3]string
}

// Rubric is the six-dimensional rubric used by the judge.
var Rubric = []DimensionRubric{
	{"Reasoning", "The closest existing code it can copy, how the task differs, new logic needed", [3]string{"Mechanical, a copied pattern", "Some new logic", "New or subtle reasoning"}, [3]string{"keep the last good list when a poll fails", "an approval flow with a code typed in the TUI", "an OAuth 2.1 server with PKCE, refresh families and replay detection"}},
	{"Scope", "Components touched, the distinct kinds of change, which edits repeat", [3]string{"Local", "Several distinct changes", "Many independent changes"}, [3]string{"one file and its test, or one pattern repeated in many files", "a gateway table, an owner op and a TUI screen", "a public listener, OAuth, phone pairing, TUI and docs together"}},
	{"Coupling", "Contracts, shared state and invariants affected", [3]string{"Isolated", "Crosses established boundaries", "Strongly interconnected"}, [3]string{"a display plugin that only reads one command's output", "a new owner op through operator, daemon and gateway", "message delivery that every agent and every harness depends on"}},
	{"Uncertainty", "Open questions, unverified assumptions, what was inspected", [3]string{"Fully understood", "Some unknowns", "Basic unknowns"}, [3]string{"the brief names the cause and the fix", "the brief names the goal but the right place in the code must be found", "the brief asks to first find how several harnesses work before choosing an approach"}},
	{"Verification", "Existing tests, how to reproduce and check the behavior", [3]string{"Fast deterministic tests", "Integration tests needed", "Hard to reproduce or check"}, [3]string{"a fix covered by fast unit tests with a fake clock or fake process", "needs a multi-package test with a fake daemon and gateway", "needs a real external client (for example a hosted bot) to confirm"}},
	{"Risk", "Data changes, security, compatibility, reversibility", [3]string{"Easily reversed", "Real side effects", "Severe or irreversible"}, [3]string{"display or text only, undone by the next release", "a database migration or a change every worker start goes through", "a public endpoint that hands out credentials"}},
}

var analyzerRules = []string{
	"Count distinct changes, not files. 20 files changed by one known pattern is scope 2, not 5.",
	"Count real coupling, not technologies. Touching the database, the API and the UI is not coupling by itself.",
	"Every claim has evidence: a file and line range, or what was inspected when it claims something is absent. Facts and guesses are marked apart.",
	"A confidence for the whole profile. Poor inspection gives low confidence, not high uncertainty.",
}

// RubricText returns the rubric as plain text for a judge prompt.
func RubricText() string {
	var b strings.Builder
	b.WriteString("Start every dimension at 1. Raise a score only when the evidence names a concrete reason, and say which evidence in the reason. Scores 2 and 4 are in-between cases.\n")
	for _, d := range Rubric {
		fmt.Fprintf(&b, "\n%s\nEvidence: %s\n1: %s (for example: %s)\n3: %s (for example: %s)\n5: %s (for example: %s)\n",
			d.Name, d.Evidence, d.Anchors[0], d.Examples[0], d.Anchors[1], d.Examples[1], d.Anchors[2], d.Examples[2])
	}
	return strings.TrimSpace(b.String())
}

// AnalyzerRules returns the analyzer's rules as plain text.
func AnalyzerRules() string { return strings.Join(analyzerRules, "\n") }

type ReasoningProfile struct {
	ClosestPattern string   `json:"closest_pattern"`
	NewLogic       string   `json:"new_logic"`
	Evidence       []string `json:"evidence"`
}
type ScopeProfile struct {
	Components      []string `json:"components"`
	DistinctChanges []string `json:"distinct_changes"`
	RepeatedChanges []string `json:"repeated_changes"`
}
type CouplingProfile struct {
	Contracts  []string `json:"contracts"`
	Invariants []string `json:"invariants"`
	Evidence   []string `json:"evidence"`
}
type UncertaintyProfile struct {
	OpenQuestions []string `json:"open_questions"`
	Inspected     []string `json:"inspected"`
}
type VerificationProfile struct {
	Tests        []string `json:"tests"`
	NeededChecks []string `json:"needed_checks"`
	Gaps         []string `json:"gaps"`
}
type RiskProfile struct {
	Failures      []string `json:"failures"`
	Reversibility string   `json:"reversibility"`
	Evidence      []string `json:"evidence"`
}

// Profile is the analyzer output described in docs/design/task-difficulty.md.
type Profile struct {
	Reasoning    ReasoningProfile    `json:"reasoning"`
	Scope        ScopeProfile        `json:"scope"`
	Coupling     CouplingProfile     `json:"coupling"`
	Uncertainty  UncertaintyProfile  `json:"uncertainty"`
	Verification VerificationProfile `json:"verification"`
	Risk         RiskProfile         `json:"risk"`
	Confidence   string              `json:"confidence"`
}

// A file path with or without a line or line range, for example a.go, a.go:3 or a.go:3-9.
var evidenceLocation = regexp.MustCompile(`^[^\s:]+\.[A-Za-z0-9]+(?::[0-9]+(?:-[0-9]+)?(?:, ?[0-9]+(?:-[0-9]+)?)*)?$|^[^\s:]+/[^\s]*$`)

// ParseProfile decodes a complete profile, refusing extra and missing fields.
func ParseProfile(raw []byte) (Profile, error) {
	var p Profile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Profile{}, fmt.Errorf("decode profile: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return Profile{}, errors.New("profile must contain one JSON value")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return Profile{}, err
	}
	for _, key := range []string{"reasoning", "scope", "coupling", "uncertainty", "verification", "risk", "confidence"} {
		if _, ok := fields[key]; !ok {
			return Profile{}, fmt.Errorf("missing required field %q", key)
		}
	}
	required := map[string][]string{
		"reasoning":    {"closest_pattern", "new_logic", "evidence"},
		"scope":        {"components", "distinct_changes", "repeated_changes"},
		"coupling":     {"contracts", "invariants", "evidence"},
		"uncertainty":  {"open_questions", "inspected"},
		"verification": {"tests", "needed_checks", "gaps"},
		"risk":         {"failures", "reversibility", "evidence"},
	}
	nested := make(map[string]map[string]json.RawMessage)
	for section := range required {
		var sectionFields map[string]json.RawMessage
		if err := json.Unmarshal(fields[section], &sectionFields); err != nil {
			return Profile{}, fmt.Errorf("decode %s: %w", section, err)
		}
		nested[section] = sectionFields
	}
	for section, keys := range required {
		for _, key := range keys {
			if _, ok := nested[section][key]; !ok {
				return Profile{}, fmt.Errorf("missing required field %q.%q", section, key)
			}
		}
	}
	if p.Confidence != "low" && p.Confidence != "medium" && p.Confidence != "high" {
		return Profile{}, fmt.Errorf("confidence must be low, medium, or high")
	}
	for field, entries := range map[string][]string{
		"reasoning.evidence": p.Reasoning.Evidence,
		"coupling.evidence":  p.Coupling.Evidence,
		"risk.evidence":      p.Risk.Evidence,
	} {
		for i, entry := range entries {
			if strings.HasPrefix(entry, "inspected:") {
				if strings.TrimSpace(strings.TrimPrefix(entry, "inspected:")) == "" {
					return Profile{}, fmt.Errorf("%s[%d] has empty inspection description", field, i)
				}
			} else if !evidenceLocation.MatchString(entry) {
				return Profile{}, fmt.Errorf("%s[%d] must be path:line, path:start-end, or inspected: description", field, i)
			}
		}
	}
	// The inspected list names what the analyzer looked at: files, folders or
	// searches, with or without lines. Any non-empty entry is fine.
	for i, entry := range p.Uncertainty.Inspected {
		if strings.TrimSpace(entry) == "" {
			return Profile{}, fmt.Errorf("uncertainty.inspected[%d] is empty", i)
		}
	}
	return p, nil
}

// Scores contains the judge's six scores and its evidence-based explanations.
type Scores struct {
	Reasoning, Scope, Coupling, Uncertainty, Verification, Risk int
	Reasons                                                     map[string]string
	Confidence                                                  string `json:"confidence,omitempty"`
}

// ParseScores parses judge JSON or its INSUFFICIENT_EVIDENCE response.
func ParseScores(raw []byte) (Scores, []string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(strings.ToUpper(trimmed), "INSUFFICIENT_EVIDENCE") {
		lower := strings.ToLower(trimmed)
		var dims []string
		for _, name := range dimensionNames {
			if strings.Contains(lower, name) {
				dims = append(dims, name)
			}
		}
		if len(dims) == 0 {
			return Scores{}, nil, errors.New("INSUFFICIENT_EVIDENCE must name at least one dimension")
		}
		return Scores{}, dims, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return Scores{}, nil, fmt.Errorf("decode scores: %w", err)
	}
	var s Scores
	nested := map[string]string{}
	values := map[string]*int{"reasoning": &s.Reasoning, "scope": &s.Scope, "coupling": &s.Coupling, "uncertainty": &s.Uncertainty, "verification": &s.Verification, "risk": &s.Risk}
	for name, ptr := range values {
		value, ok := obj[name]
		if !ok {
			return Scores{}, nil, fmt.Errorf("missing score %q", name)
		}
		if err := json.Unmarshal(value, ptr); err != nil {
			// The judge sometimes nests each score with its reason.
			var pair struct {
				Score  *int   `json:"score"`
				Reason string `json:"reason"`
			}
			if json.Unmarshal(value, &pair) != nil || pair.Score == nil {
				return Scores{}, nil, fmt.Errorf("score %q: %w", name, err)
			}
			*ptr = *pair.Score
			if pair.Reason != "" {
				nested[name] = pair.Reason
			}
		}
		if *ptr < 1 || *ptr > 5 {
			return Scores{}, nil, fmt.Errorf("score %q must be 1 to 5", name)
		}
	}
	if reasons, ok := obj["reasons"]; ok {
		if err := json.Unmarshal(reasons, &s.Reasons); err != nil {
			return Scores{}, nil, fmt.Errorf("decode reasons: %w", err)
		}
	}
	for name, reason := range nested {
		if s.Reasons == nil {
			s.Reasons = map[string]string{}
		}
		if s.Reasons[name] == "" {
			s.Reasons[name] = reason
		}
	}
	if confidence, ok := obj["confidence"]; ok {
		if err := json.Unmarshal(confidence, &s.Confidence); err != nil {
			return Scores{}, nil, fmt.Errorf("decode confidence: %w", err)
		}
		if s.Confidence != "low" && s.Confidence != "medium" && s.Confidence != "high" {
			return Scores{}, nil, errors.New("confidence must be low, medium, or high")
		}
	}
	return s, nil, nil
}

var dimensionNames = []string{"reasoning", "scope", "coupling", "uncertainty", "verification", "risk"}

// Assessment is the analyzer profile plus the judge scores and the label they
// produce. Notes explains a fallback to "normal" when the run could not finish.
type Assessment struct {
	Profile       Profile       `json:"profile"`
	Mode          string        `json:"mode"`
	Scores        Scores        `json:"scores"`
	Label         string        `json:"label"`
	RulesVersion  string        `json:"rules_version"`
	D             float64       `json:"d"`
	Bottleneck    string        `json:"bottleneck,omitempty"`
	Confidence    string        `json:"confidence,omitempty"`
	Notes         string        `json:"notes,omitempty"`
	AnalyzerModel string        `json:"analyzer_model,omitempty"`
	JudgeModel    string        `json:"judge_model,omitempty"`
	Elapsed       time.Duration `json:"elapsed"`
}

// RulesVersion identifies the weights, cut points and bottleneck rule below.
// Change it whenever any of those rules change.
const RulesVersion = "2026-11-02.1"

// The coefficients and cut points are centralized for future tuning.
var scoreWeights = []float64{0.20, 0.10, 0.20, 0.15, 0.15, 0.20}
var labelCutPoints = []float64{1.5, 2.25, 3.0, 3.75}
var labelNames = []string{"trivial", "simple", "normal", "hard", "very_complex"}

func labelIndex(d float64) int {
	return sort.Search(len(labelCutPoints), func(i int) bool { return labelCutPoints[i] > d })
}

// Label applies the weighted score and the score-of-5 bottleneck rule.
func Label(s Scores) (label string, d float64, bottleneck string, version string) {
	version = RulesVersion
	values := []int{s.Reasoning, s.Scope, s.Coupling, s.Uncertainty, s.Verification, s.Risk}
	for i, value := range values {
		d += float64(value) * scoreWeights[i]
	}
	idx := labelIndex(d)
	label = labelNames[idx]
	var names []string
	for i, value := range values {
		if value == 5 {
			names = append(names, dimensionNames[i])
		}
	}
	if len(names) > 0 {
		bottleneck = strings.Join(names, ", ")
		if idx < 3 {
			label = "hard"
		}
	}
	return label, d, bottleneck, version
}

// Relabel applies the current rules to a previously stored assessment.
func Relabel(a Assessment) Assessment {
	a.Label, a.D, a.Bottleneck, a.RulesVersion = Label(a.Scores)
	return a
}
