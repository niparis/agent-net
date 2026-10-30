package protocol

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ModelScore is a capability score for a model or a loose model name.
type ModelScore struct {
	Match  string  `json:"match"`
	Effort string  `json:"effort,omitempty"`
	Score  float64 `json:"score"`
	Source string  `json:"source"`
}

//go:embed scores.json
var scoreTableJSON []byte

// EmbeddedModelScores returns the bundled model scores.
func EmbeddedModelScores() []ModelScore {
	var scores []ModelScore
	if err := json.Unmarshal(scoreTableJSON, &scores); err != nil {
		panic(err)
	}
	return scores
}

// scoreName normalizes provider prefixes, effort, free and snapshot suffixes.
var scoreSnapshot = regexp.MustCompile(`(?:[-:]\d{4,})$`)
var scoreVersionDot = regexp.MustCompile(`(\d)\.(\d)`)

func scoreName(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndexByte(model, '/'); i >= 0 {
		model = model[i+1:]
	}
	model, _ = SplitModelLevel(model)
	model = strings.TrimSuffix(strings.TrimSuffix(model, ":free"), "-free")
	model = scoreSnapshot.ReplaceAllString(model, "")
	model = scoreVersionDot.ReplaceAllString(model, "${1}-${2}")
	switch model {
	case "opus":
		return "claude-opus-5-5"
	case "sonnet":
		return "claude-sonnet-5-5"
	case "haiku":
		return "claude-haiku-5-5"
	}
	return model
}

// looseScoreMatch requires all pattern tokens and the same variant words.
func looseScoreMatch(pattern, model string) bool {
	if pattern == "" || model == "" {
		return false
	}
	variants := []string{"fast", "pro", "ultrafast", "flash", "lite"}
	words := func(s string) map[string]bool {
		out := map[string]bool{}
		for _, w := range modelTokens(s) {
			out[w] = true
		}
		return out
	}
	p, m := words(pattern), words(model)
	for _, v := range variants {
		if p[v] != m[v] {
			return false
		}
	}
	for token := range p {
		if !m[token] {
			return false
		}
	}
	return true
}

// LookupModelScore prefers owner overrides and preserves exact effort matches.
func LookupModelScore(model string, overrides []ModelScore) (ModelScore, bool) {
	return LookupModelScoreWithDefaultEffort(model, "", overrides)
}

// LookupModelScoreWithDefaultEffort uses the harness default when no effort is named.
func LookupModelScoreWithDefaultEffort(model, defaultEffort string, overrides []ModelScore) (ModelScore, bool) {
	lookup := func(scores []ModelScore) (ModelScore, bool) {
		best := -1
		var result ModelScore
		base, level := SplitModelLevel(model)
		part := model
		if i := strings.LastIndexByte(part, '/'); i >= 0 {
			part = part[i+1:]
		}
		for _, s := range scores {
			requested := strings.ToLower(level)
			if requested == "" {
				requested = strings.ToLower(defaultEffort)
			}
			if requested == "" {
				requested = "medium"
			}
			effortRank := 0
			if s.Effort != "" {
				if strings.EqualFold(s.Effort, requested) {
					effortRank = 2
				} else if s.Effort == "default" {
					effortRank = 1
				} else {
					continue
				}
			}
			match := strings.TrimSpace(s.Match)
			rank := -1
			switch {
			case strings.EqualFold(match, model):
				rank = 3
			case strings.EqualFold(match, part):
				rank = 2
			case strings.EqualFold(match, base):
				rank = 1
			default:
				_, wantLevel := SplitModelLevel(match)
				if wantLevel != "" && !strings.EqualFold(wantLevel, level) {
					continue
				}
				if scoreName(match) == scoreName(model) && scoreName(model) != "" {
					rank = 1
				} else if looseScoreMatch(scoreName(match), scoreName(model)) {
					rank = 0
				}
			}
			if rank >= 0 {
				rank = rank*3 + effortRank
			}
			if rank > best {
				result, best = s, rank
			}
		}
		return result, best >= 0
	}
	if result, ok := lookup(overrides); ok {
		return result, true
	}
	return lookup(EmbeddedModelScores())
}

// DifficultyMinimums are the default minimum capability scores.
var DifficultyMinimums = map[string]float64{
	"trivial": 0, "simple": 20, "normal": 30, "hard": 40, "very_complex": 50,
}

// DifficultyFit classifies a score relative to a difficulty's minimum.
// A nil or partial minimums map uses the defaults for missing levels.
func DifficultyFit(score float64, level string, minimums map[string]float64) (string, error) {
	if level == "" {
		level = "normal"
	}
	minimum, ok := DifficultyMinimums[level]
	if !ok {
		return "", fmt.Errorf("unknown difficulty %q", level)
	}
	if custom, ok := minimums[level]; ok {
		minimum = custom
	}
	switch difference := score - minimum; {
	case difference >= 10:
		return "excellent", nil
	case difference >= 0:
		return "good", nil
	case difference >= -10:
		return "poor", nil
	default:
		return "denied", nil
	}
}

// ScoreSetRequest changes one owner override; nil Score clears it.
type ScoreSetRequest struct {
	Model string   `json:"model"`
	Score *float64 `json:"score,omitempty"`
}

const (
	OpScoresGet = "scores_get"
	OpScoresSet = "scores_set"
)
