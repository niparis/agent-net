package protocol

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// ModelScore is a capability score for a model or a loose model name.
type ModelScore struct {
	Match  string  `json:"match"`
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

// LookupModelScore finds the most specific matching owner override, then the
// bundled score. Exact IDs take precedence over provider-less IDs and names
// without an effort suffix. Loose names match model tokens.
func LookupModelScore(model string, overrides []ModelScore) (ModelScore, bool) {
	lookup := func(scores []ModelScore) (ModelScore, bool) {
		best := -1
		var result ModelScore
		base, _ := SplitModelLevel(model)
		for _, s := range scores {
			m := strings.TrimSpace(s.Match)
			rank := -1
			switch {
			case strings.EqualFold(m, model):
				rank = 3
			case strings.EqualFold(m, modelPart(model)):
				rank = 2
			case strings.EqualFold(m, base), strings.EqualFold(m, modelPart(base)):
				rank = 1
			default:
				patternBase, level := SplitModelLevel(m)
				_, modelLevel := SplitModelLevel(model)
				if level != "" && !strings.EqualFold(level, modelLevel) {
					continue
				}
				tokens := modelTokens(patternBase)
				if len(tokens) == 0 {
					continue
				}
				match := true
				for _, token := range tokens {
					if !hasToken(strings.ToLower(modelPart(base)), token) {
						match = false
						break
					}
				}
				if match {
					rank = 0
				}
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
