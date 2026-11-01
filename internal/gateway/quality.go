package gateway

import (
	"math"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// ridgePull is ten equivalent tasks at zero for each effect. The intercept
// is unpenalized. Correlated effects share credit; overlap is needed to tell
// them apart. Effort is nested within model, never compared across vendors.
const ridgePull = 10.0

type qualitySample struct {
	Model, Harness, Effort, Difficulty string
	Value, Weight                      float64
	Strong                             bool
}

func qualityModel(model string) string {
	model, _ = protocol.SplitModelLevel(model)
	if i := strings.LastIndexByte(model, '/'); i >= 0 {
		model = model[i+1:]
	}
	return strings.ToLower(model)
}
func qualityKeys(model, harness, effort, difficulty string) []string {
	if difficulty == "" {
		difficulty = "normal"
	}
	return []string{"model\x00" + qualityModel(model), "harness\x00" + harness, "effort\x00" + qualityModel(model) + " " + effort, "difficulty\x00" + difficulty}
}

type qualityFit struct {
	Average float64
	Effects map[string]float64
	Counts  map[string]int
}

func (f qualityFit) effect(kind, name string) float64 { return f.Effects[kind+"\x00"+name] }
func (f qualityFit) predict(model, harness, effort, difficulty string) float64 {
	y := f.Average
	for _, key := range qualityKeys(model, harness, effort, difficulty) {
		y += f.Effects[key]
	}
	return y
}
func fitQuality(samples []qualitySample) qualityFit {
	f := qualityFit{Effects: map[string]float64{}, Counts: map[string]int{}}
	type entry struct {
		index  int
		weight float64
	}
	columns := map[string][]entry{}
	total := 0.0
	for i, s := range samples {
		total += s.Weight
		f.Average += s.Weight * s.Value
		for _, key := range qualityKeys(s.Model, s.Harness, s.Effort, s.Difficulty) {
			columns[key] = append(columns[key], entry{i, s.Weight})
			f.Counts[key]++
		}
	}
	if total == 0 {
		return f
	}
	f.Average /= total
	keys := make([]string, 0, len(columns))
	for key := range columns {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	effortGroups := map[string][]string{}
	var families []string
	for _, key := range keys {
		if strings.HasPrefix(key, "effort\x00") {
			name := strings.TrimPrefix(key, "effort\x00")
			model, _, _ := strings.Cut(name, " ")
			if _, ok := effortGroups[model]; !ok {
				families = append(families, model)
			}
			effortGroups[model] = append(effortGroups[model], key)
		}
	}
	sort.Strings(families)
	residual := make([]float64, len(samples))
	for i, s := range samples {
		residual[i] = s.Value - f.Average
	}
	// Coordinate descent on weighted squared error plus ridgePull*sum(effect²).
	// Deterministic ordering and a convergence bound keep cached fits stable.
	for iteration := 0; iteration < 1000; iteration++ {
		delta := 0.0
		for _, key := range keys {
			if strings.HasPrefix(key, "effort\x00") {
				continue
			}
			old := f.Effects[key]
			num, den := 0.0, ridgePull
			for _, e := range columns[key] {
				num += e.weight * (residual[e.index] + old)
				den += e.weight
			}
			v := num / den
			f.Effects[key] = v
			delta = math.Max(delta, math.Abs(v-old))
			for _, e := range columns[key] {
				residual[e.index] -= v - old
			}
		}

		// Solve each effort block with sum(effect)=0 within its model.
		// The Lagrange multiplier gives the exact constrained ridge update.
		for _, family := range families {
			group := effortGroups[family]
			nums, dens := make([]float64, len(group)), make([]float64, len(group))
			top, bottom := 0.0, 0.0
			for j, key := range group {
				dens[j] = ridgePull
				for _, e := range columns[key] {
					nums[j] += e.weight * (residual[e.index] + f.Effects[key])
					dens[j] += e.weight
				}
				top += nums[j] / dens[j]
				bottom += 1 / dens[j]
			}
			lambda := top / bottom
			for j, key := range group {
				old := f.Effects[key]
				v := (nums[j] - lambda) / dens[j]
				f.Effects[key] = v
				delta = math.Max(delta, math.Abs(v-old))
				for _, e := range columns[key] {
					residual[e.index] -= v - old
				}
			}
		}
		shift := 0.0
		for i, s := range samples {
			shift += s.Weight * residual[i]
		}
		shift /= total
		f.Average += shift
		for i := range residual {
			residual[i] -= shift
		}
		if math.Max(delta, math.Abs(shift)) < 1e-8 {
			break
		}
	}
	return f
}

// refreshQuality is called on source writes and startup, never by a read or
// a TUI redraw. Holding qualityMu serializes rebuilds and snapshot reads.
func (s *Store) refreshQuality() error {
	s.qualityMu.Lock()
	defer s.qualityMu.Unlock()
	scores, err := s.scores()
	if err != nil {
		return err
	}
	rows, err := s.db.Query(`SELECT model,harness,effort,difficulty,score,rater_model FROM worker_ratings ORDER BY id`)
	if err != nil {
		return err
	}
	var ratings []qualitySample
	strong := map[string]int{}
	for rows.Next() {
		var x qualitySample
		var rater string
		if err := rows.Scan(&x.Model, &x.Harness, &x.Effort, &x.Difficulty, &x.Value, &rater); err != nil {
			rows.Close()
			return err
		}
		x.Model = qualityModel(x.Model)
		x.Weight = .5
		if score, ok := protocol.LookupModelScore(rater, scores); ok && score.Score >= protocol.CalibrationStrongScore {
			x.Weight = 1
			x.Strong = true
			strong[x.Model]++
		}
		ratings = append(ratings, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = s.db.Query(`SELECT model,harness,difficulty,continue_count,fix_count FROM worker_runs
 WHERE model <> '' AND (session_id = '' OR id IN (SELECT MAX(id) FROM worker_runs WHERE session_id <> '' GROUP BY session_id)) ORDER BY id`)
	if err != nil {
		return err
	}
	var runs []qualitySample
	for rows.Next() {
		var x qualitySample
		var model string
		var cont, fix int
		if err := rows.Scan(&model, &x.Harness, &x.Difficulty, &cont, &fix); err != nil {
			rows.Close()
			return err
		}
		x.Model, x.Effort = protocol.SplitModelLevel(model)
		x.Model = qualityModel(x.Model)
		x.Value = protocol.ReworkWeight(fix, cont)
		x.Weight = 1
		runs = append(runs, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	q, r := fitQuality(ratings), fitQuality(runs)
	out := protocol.QualityAnalysis{Status: "not enough ratings", Ratings: len(ratings), Average: q.Average, ReworkAverage: r.Average}
	if len(ratings) >= 5 {
		out.Status = "tentative: overlap and more ratings needed"
	}
	keys := map[string]bool{}
	for key := range q.Counts {
		keys[key] = true
	}
	for key := range r.Counts {
		keys[key] = true
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		kind, name, _ := strings.Cut(key, "\x00")
		out.Effects = append(out.Effects, protocol.QualityEffect{Kind: kind, Name: name, Quality: q.Effects[key], Rework: r.Effects[key], Ratings: q.Counts[key], Runs: r.Counts[key]})
	}
	profiles, err := s.WorkerProfiles()
	if err != nil {
		return err
	}
	for _, p := range profiles {
		v := qualityPrediction(q, strong, p.Model, p.Harness, p.Effort, "normal")
		row := protocol.ProfileQuality{Name: p.Name, Quality: v}
		actual, predicted := 0.0, 0.0
		for _, x := range ratings {
			if x.Model == qualityModel(p.Model) && x.Harness == p.Harness && x.Effort == p.Effort {
				row.Ratings++
				actual += x.Value
				predicted += q.predict(x.Model, x.Harness, x.Effort, x.Difficulty)
			}
		}
		for _, x := range runs {
			if x.Model == qualityModel(p.Model) && x.Harness == p.Harness && x.Effort == p.Effort {
				row.Runs++
			}
		}
		row.Unexpected = row.Ratings >= 5 && math.Abs(actual-predicted)/float64(row.Ratings) > .75
		out.Profiles = append(out.Profiles, row)
	}
	s.quality = out
	s.qualityFit = q
	s.qualityStrong = strong
	return nil
}
func qualityPrediction(f qualityFit, strong map[string]int, model, harness, effort, difficulty string) float64 {
	v := f.predict(model, harness, effort, difficulty)
	// Generous weak reviewers alone cannot promote a model above average.
	if strong[qualityModel(model)] == 0 {
		v = math.Min(v, f.Average)
	}
	return math.Max(1, math.Min(5, v))
}
func (s *Store) QualityAnalysis() protocol.QualityAnalysis {
	s.qualityMu.Lock()
	defer s.qualityMu.Unlock()
	out := s.quality
	out.Effects = append([]protocol.QualityEffect(nil), out.Effects...)
	out.Profiles = append([]protocol.ProfileQuality(nil), out.Profiles...)
	return out
}
func (s *Store) applyQualityWeights(candidates []protocol.WorkerModelCandidate, difficulty string) error {
	settings, err := s.qualitySettings()
	if err != nil || !settings.Enabled {
		return err
	}
	s.qualityMu.Lock()
	defer s.qualityMu.Unlock()
	if s.quality.Ratings < 5 {
		return nil
	}
	for i, c := range candidates {
		model, effort := protocol.SplitModelLevel(c.Model)
		if effort == "" {
			effort = c.DefaultEffort
		}
		v := qualityPrediction(s.qualityFit, s.qualityStrong, model, c.Harness, effort, difficulty)
		candidates[i].QualityWeight = math.Max(.5, math.Min(1.5, 1+(v-s.qualityFit.Average)/4))
	}
	return nil
}
