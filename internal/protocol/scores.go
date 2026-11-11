package protocol

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// ModelScore is a capability score for a model or a loose model name.
type ModelScore struct {
	Match          string  `json:"match"`
	Effort         string  `json:"effort,omitempty"`
	Score          float64 `json:"score"`
	Source         string  `json:"source"`
	FallbackEffort string  `json:"fallback_effort,omitempty"`
}

//go:embed scores.json
var scoreTableJSON []byte

var (
	embeddedScoresOnce sync.Once
	embeddedScores     []ModelScore
)

// embeddedScoreTable decodes the bundled scores once. Callers must not change
// the result.
func embeddedScoreTable() []ModelScore {
	embeddedScoresOnce.Do(func() {
		if err := json.Unmarshal(scoreTableJSON, &embeddedScores); err != nil {
			panic(err)
		}
	})
	return embeddedScores
}

// EmbeddedModelScores returns the bundled model scores. The caller owns the
// returned slice.
func EmbeddedModelScores() []ModelScore {
	return append([]ModelScore(nil), embeddedScoreTable()...)
}

// scoreName normalizes provider prefixes, effort, free and snapshot suffixes.
var scoreSnapshot = regexp.MustCompile(`(?:[-:]\d{4,})$`)
var scoreVersionDot = regexp.MustCompile(`(\d)\.(\d)`)

// nameCache remembers a pure function of a model name. Model names come from
// a small set (the bundled table, advertised models, profiles), so the cache
// is capped only as a guard against a flood of distinct inputs.
type nameCache[V any] struct {
	m    sync.Map
	size atomic.Int64
}

const nameCacheMax = 8192

func (c *nameCache[V]) get(key string, compute func(string) V) V {
	if v, ok := c.m.Load(key); ok {
		return v.(V)
	}
	v := compute(key)
	if c.size.Load() < nameCacheMax {
		if _, loaded := c.m.LoadOrStore(key, v); !loaded {
			c.size.Add(1)
		}
	}
	return v
}

var (
	scoreNames nameCache[string]
	// scoreWords holds the token sets looseScoreMatch compares. Callers must
	// not change them.
	scoreWords nameCache[map[string]bool]
)

func scoreName(model string) string {
	return scoreNames.get(model, scoreNameCompute)
}

func scoreTokenSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range modelTokens(s) {
		out[w] = true
	}
	return out
}

func scoreNameCompute(model string) string {
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
	p, m := scoreWords.get(pattern, scoreTokenSet), scoreWords.get(model, scoreTokenSet)
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

// dashScoreLevels are the efforts agy writes after a dash, as in
// "gemini-4-argon-high". Words that can end a model name, such as "max" or
// "ultra", are left out.
var dashScoreLevels = map[string]bool{"minimal": true, "low": true, "medium": true, "high": true, "xhigh": true}

// splitScoreLevel is SplitModelLevel that also reads agy's dash suffix.
func splitScoreLevel(model string) (base, level string) {
	selection := ParseModelSelection("agy", "", model, "")
	base = selection.Model
	if selection.Provider != "" {
		base = selection.Provider + "/" + base
	}
	return base, selection.Effort
}

// matchModelRank scores the specificity of a model name match.
func matchModelRank(s ModelScore, model, base, part, partBase, partBaseNorm, partNorm string) int {
	match := strings.TrimSpace(s.Match)
	matchNorm := scoreVersionDot.ReplaceAllString(match, "${1}-${2}")
	switch {
	case strings.EqualFold(match, model):
		return 4
	case strings.EqualFold(match, part) || strings.EqualFold(matchNorm, partNorm):
		return 3
	case strings.EqualFold(match, base):
		return 2
	case strings.EqualFold(match, partBase) || strings.EqualFold(matchNorm, partBaseNorm) || strings.EqualFold(match, partBaseNorm):
		return 2
	default:
		if scoreName(match) == scoreName(model) && scoreName(model) != "" {
			return 1
		} else if looseScoreMatch(scoreName(match), scoreName(model)) {
			return 0
		}
	}
	return -1
}

// LookupModelScoreWithDefaultEffort uses the harness default when no effort is named.
func LookupModelScoreWithDefaultEffort(model, defaultEffort string, overrides []ModelScore) (ModelScore, bool) {
	base, level := splitScoreLevel(model)
	requested := strings.ToLower(level)
	if requested == "" {
		requested = strings.ToLower(defaultEffort)
	}
	if requested == "" {
		requested = "medium"
	}
	part := model
	if i := strings.LastIndexByte(part, '/'); i >= 0 {
		part = part[i+1:]
	}
	partBase, _ := SplitModelLevel(part)
	partBaseNorm := scoreVersionDot.ReplaceAllString(partBase, "${1}-${2}")
	partNorm := scoreVersionDot.ReplaceAllString(part, "${1}-${2}")

	lookupExact := func(scores []ModelScore) (ModelScore, bool) {
		best := -1
		var result ModelScore
		for _, s := range scores {
			entryEffort := s.Effort
			if entryEffort == "" {
				_, wantLevel := SplitModelLevel(s.Match)
				entryEffort = wantLevel
			}
			effortRank := 0
			if entryEffort != "" {
				if strings.EqualFold(entryEffort, requested) {
					effortRank = 2
				} else if entryEffort == "default" {
					effortRank = 1
				} else {
					continue
				}
			}
			rank := matchModelRank(s, model, base, part, partBase, partBaseNorm, partNorm)
			if rank >= 0 {
				totalRank := rank*3 + effortRank
				if totalRank > best {
					result, best = s, totalRank
				}
			}
		}
		return result, best >= 0
	}

	if result, ok := lookupExact(overrides); ok {
		return result, true
	}
	scores := embeddedScoreTable()
	if result, ok := lookupExact(scores); ok {
		return result, true
	}

	_, modelLevel := SplitModelLevel(model)
	if modelLevel == "" && defaultEffort == "" {
		var found ModelScore
		count := 0
		for _, s := range scores {
			if s.Effort != "" && scoreName(s.Match) == scoreName(model) {
				found = s
				count++
			}
		}
		if count == 1 {
			return found, true
		}
	}

	lookupFallback := func(list []ModelScore) (ModelScore, bool) {
		bestModelRank := -1
		bestIsLower := false
		bestDist := 999
		var bestResult ModelScore
		hasCandidate := false

		reqRank, ok := scoreEffortOrder[requested]
		if !ok {
			reqRank = scoreEffortOrder["medium"]
		}

		for _, s := range list {
			mRank := matchModelRank(s, model, base, part, partBase, partBaseNorm, partNorm)
			if mRank < 0 {
				continue
			}
			effortStr := s.Effort
			if effortStr == "" {
				_, effortStr = SplitModelLevel(s.Match)
			}
			effortStr = strings.ToLower(strings.TrimSpace(effortStr))
			if effortStr == "" || strings.EqualFold(effortStr, requested) || effortStr == "default" {
				continue
			}
			sRank, ok := scoreEffortOrder[effortStr]
			if !ok {
				sRank = scoreEffortOrder["medium"]
			}

			isLower := sRank < reqRank
			var dist int
			if isLower {
				dist = reqRank - sRank
			} else {
				dist = sRank - reqRank
			}

			better := false
			if !hasCandidate {
				better = true
			} else if mRank > bestModelRank {
				better = true
			} else if mRank == bestModelRank {
				if isLower && !bestIsLower {
					better = true
				} else if isLower == bestIsLower && dist < bestDist {
					better = true
				}
			}

			if better {
				hasCandidate = true
				bestModelRank = mRank
				bestIsLower = isLower
				bestDist = dist
				bestResult = s
				if bestResult.Effort == "" {
					bestResult.Effort = effortStr
				}
				bestResult.FallbackEffort = effortStr
			}
		}
		return bestResult, hasCandidate
	}

	if result, ok := lookupFallback(overrides); ok {
		return result, true
	}
	if result, ok := lookupFallback(scores); ok {
		return result, true
	}

	return ModelScore{}, false
}

// ModelEffortScore is one effort's capability score for a model, as the read
// tools show it.
type ModelEffortScore struct {
	Effort string  `json:"effort"`
	Score  float64 `json:"score"`
	Source string  `json:"source"`
}

// scoreEffortOrder gives the effort list a stable, readable order.
var scoreEffortOrder = map[string]int{"minimal": 0, "low": 1, "medium": 2, "high": 3, "xhigh": 4, "max": 5, "ultra": 6}

// scoreMatchesModel is the name match of LookupModelScoreWithDefaultEffort
// without the effort filter: it says whether an entry describes this model.
func scoreMatchesModel(s ModelScore, model string) bool {
	match := strings.TrimSpace(s.Match)
	if match == "" || model == "" {
		return false
	}
	base, _ := splitScoreLevel(model)
	part := model
	if i := strings.LastIndexByte(part, '/'); i >= 0 {
		part = part[i+1:]
	}
	switch {
	case strings.EqualFold(match, model), strings.EqualFold(match, part), strings.EqualFold(match, base):
		return true
	}
	if scoreName(model) == "" {
		return false
	}
	return scoreName(match) == scoreName(model) || looseScoreMatch(scoreName(match), scoreName(model))
}

// ModelScoresFor returns every score that applies to model, one per effort.
// Owner overrides win over embedded entries, using the same name match as
// LookupModelScoreWithDefaultEffort. An override without an effort applies to
// every effort, so it is the only entry for that model.
func ModelScoresFor(model string, overrides []ModelScore) []ModelEffortScore {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil
	}
	var matched []ModelScore
	for _, s := range overrides {
		if scoreMatchesModel(s, model) {
			matched = append(matched, s)
		}
	}
	chosen := matched
	coversAll := false
	for _, s := range matched {
		if strings.TrimSpace(s.Effort) == "" {
			coversAll = true
			break
		}
	}
	if !coversAll {
		covered := map[string]bool{}
		for _, s := range matched {
			covered[strings.ToLower(strings.TrimSpace(s.Effort))] = true
		}
		for _, s := range embeddedScoreTable() {
			if scoreMatchesModel(s, model) && !covered[strings.ToLower(strings.TrimSpace(s.Effort))] {
				chosen = append(chosen, s)
			}
		}
	}
	out := make([]ModelEffortScore, 0, len(chosen))
	for _, s := range chosen {
		out = append(out, ModelEffortScore{Effort: s.Effort, Score: s.Score, Source: s.Source})
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := scoreEffortOrder[strings.ToLower(out[i].Effort)], scoreEffortOrder[strings.ToLower(out[j].Effort)]
		if out[i].Effort == "" {
			ri = -1
		}
		if out[j].Effort == "" {
			rj = -1
		}
		if ri != rj {
			return ri < rj
		}
		return out[i].Effort < out[j].Effort
	})
	return out
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
