package main

import (
	"math"
	"sort"

	"github.com/niparis/agent-net/internal/protocol"
)

// Option B: a logistic regression for "this task ends clean" from the judge's
// six sub-scores, D, the project, the harness, the provider and the model, with
// the model effect shrunk toward its family (partial pooling).

// regInput is one row of the regression: the task, and the model that would do
// it.
type regInput struct {
	D       float64
	Scores  [6]float64
	Project string
	Key     string
	Harness string
	Family  string
	Prov    string
}

const (
	groupNumeric = iota
	groupFamily
	groupModel
	groupHarness
	groupProvider
	groupProject
)

// featureSpec maps categorical values to feature columns. It is built from the
// training tasks, so a value never seen in training contributes only its
// broader group.
type featureSpec struct {
	names []string
	group []int
	index []map[string]int
}

func newFeatureSpec(tasks []Task) *featureSpec {
	spec := &featureSpec{index: []map[string]int{{}, {}, {}, {}, {}, {}}}
	// Seven numeric columns: six sub-scores and D.
	for _, name := range append(append([]string{}, SubScoreNames[:]...), "d") {
		spec.names = append(spec.names, name)
		spec.group = append(spec.group, groupNumeric)
	}
	seen := map[int]map[string]bool{}
	add := func(group int, value string) {
		if value == "" {
			return
		}
		if seen[group] == nil {
			seen[group] = map[string]bool{}
		}
		if seen[group][value] {
			return
		}
		seen[group][value] = true
		spec.index[group][value] = len(spec.names)
		spec.names = append(spec.names, value)
		spec.group = append(spec.group, group)
	}
	// Projects are limited to the busiest ones; a rare project pools into
	// "other" through having no column of its own.
	projectN := map[string]int{}
	for _, t := range tasks {
		projectN[t.Project]++
	}
	type pcount struct {
		name string
		n    int
	}
	var projects []pcount
	for name, n := range projectN {
		if name != "" {
			projects = append(projects, pcount{name, n})
		}
	}
	sort.Slice(projects, func(i, j int) bool {
		if projects[i].n != projects[j].n {
			return projects[i].n > projects[j].n
		}
		return projects[i].name < projects[j].name
	})
	if len(projects) > 8 {
		projects = projects[:8]
	}
	for _, p := range projects {
		add(groupProject, p.name)
	}
	for _, t := range tasks {
		add(groupFamily, modelFamily(t.Key))
		add(groupModel, t.Key)
		add(groupHarness, t.Harness)
		add(groupProvider, providerOf(t.Key))
	}
	return spec
}

// vector turns one input into the design row.
func (spec *featureSpec) vector(in regInput) []float64 {
	x := make([]float64, len(spec.names))
	for i, v := range in.Scores {
		x[i] = clamp01((v - 1) / 4)
	}
	x[6] = clamp01((in.D - 1) / 4)
	put := func(group int, value string) {
		if i, ok := spec.index[group][value]; ok {
			x[i] = 1
		}
	}
	put(groupFamily, in.Family)
	put(groupModel, in.Key)
	put(groupHarness, in.Harness)
	put(groupProvider, in.Prov)
	put(groupProject, in.Project)
	return x
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// modelPenalty is the ridge weight of a model's own effect. The flag exists so
// the replay can show that option B's answer does not depend on this knob.
var modelPenalty = 40.0

// groupPenalty is the ridge weight per feature group. A model effect is pulled
// harder toward zero than its family, which is the partial pooling: a family
// with many tasks sets a level, and a thin model moves off it only with
// evidence of its own.
func groupPenalty(group int) float64 {
	switch group {
	case groupModel:
		return modelPenalty
	case groupFamily:
		return 8
	case groupProject:
		return 4
	case groupHarness, groupProvider:
		return 2
	default:
		return .1
	}
}

// logistic is a fitted model.
type logistic struct {
	spec *featureSpec
	beta []float64
}

// predict is the probability that the task ends clean.
func (l *logistic) predict(x []float64) float64 {
	eta := 0.0
	for i, v := range x {
		eta += l.beta[i] * v
	}
	return sigmoid(eta)
}

func sigmoid(v float64) float64 {
	if v >= 0 {
		z := math.Exp(-v)
		return 1 / (1 + z)
	}
	z := math.Exp(v)
	return z / (1 + z)
}

// fitLogistic runs Newton-Raphson (IRLS) with the group penalties. The design
// is small (about a hundred columns), so a dense solve is fast and exact.
func fitLogistic(x [][]float64, y []float64, spec *featureSpec) *logistic {
	n, d := len(x), len(spec.names)
	beta := make([]float64, d)
	penalty := make([]float64, d)
	for i := range penalty {
		penalty[i] = groupPenalty(spec.group[i])
	}
	for iter := 0; iter < 40; iter++ {
		// A holds X'WX + penalty; b holds X'Wz.
		a := make([][]float64, d)
		for i := range a {
			a[i] = make([]float64, d)
			a[i][i] = penalty[i] + 1e-9
		}
		b := make([]float64, d)
		for r := 0; r < n; r++ {
			eta := 0.0
			var nz []int
			for j, v := range x[r] {
				if v != 0 {
					eta += beta[j] * v
					nz = append(nz, j)
				}
			}
			p := sigmoid(eta)
			w := p * (1 - p)
			if w < 1e-6 {
				w = 1e-6
			}
			z := eta + (y[r]-p)/w
			for _, i := range nz {
				vi := x[r][i]
				b[i] += w * vi * z
				for _, j := range nz {
					a[i][j] += w * vi * x[r][j]
				}
			}
		}
		next, ok := solve(a, b)
		if !ok {
			break
		}
		delta := 0.0
		for i := range next {
			delta = math.Max(delta, math.Abs(next[i]-beta[i]))
		}
		beta = next
		if delta < 1e-8 {
			break
		}
	}
	return &logistic{spec: spec, beta: beta}
}

// solve is Gaussian elimination with partial pivoting.
func solve(a [][]float64, b []float64) ([]float64, bool) {
	n := len(b)
	for col := 0; col < n; col++ {
		pivot := col
		for r := col + 1; r < n; r++ {
			if math.Abs(a[r][col]) > math.Abs(a[pivot][col]) {
				pivot = r
			}
		}
		if math.Abs(a[pivot][col]) < 1e-12 {
			return nil, false
		}
		a[col], a[pivot] = a[pivot], a[col]
		b[col], b[pivot] = b[pivot], b[col]
		for r := col + 1; r < n; r++ {
			f := a[r][col] / a[col][col]
			if f == 0 {
				continue
			}
			for c := col; c < n; c++ {
				a[r][c] -= f * a[col][c]
			}
			b[r] -= f * b[col]
		}
	}
	x := make([]float64, n)
	for r := n - 1; r >= 0; r-- {
		sum := b[r]
		for c := r + 1; c < n; c++ {
			sum -= a[r][c] * x[c]
		}
		x[r] = sum / a[r][r]
	}
	return x, true
}

// logLoss is the mean negative log likelihood, with predictions clipped away
// from 0 and 1 so one confident miss cannot outweigh the table.
func logLoss(ps, ys []float64) float64 {
	if len(ps) == 0 {
		return 0
	}
	var sum float64
	for i := range ps {
		p := math.Max(1e-6, math.Min(1-1e-6, ps[i]))
		if ys[i] > .5 {
			sum -= math.Log(p)
		} else {
			sum -= math.Log(1 - p)
		}
	}
	return sum / float64(len(ps))
}

func brier(ps, ys []float64) float64 {
	if len(ps) == 0 {
		return 0
	}
	var sum float64
	for i := range ps {
		sum += (ps[i] - ys[i]) * (ps[i] - ys[i])
	}
	return sum / float64(len(ps))
}

// calibration puts predictions into ten bins and compares the mean prediction
// with the observed share.
func calibration(ps, ys []float64) []CalibrationBin {
	bins := make([]CalibrationBin, 10)
	for i := range bins {
		bins[i].Lo = float64(i) / 10
		bins[i].Hi = float64(i+1) / 10
	}
	var sums [10]float64
	var obs [10]float64
	for i := range ps {
		b := int(clamp01(ps[i]) * 10)
		if b > 9 {
			b = 9
		}
		bins[b].N++
		sums[b] += ps[i]
		if ys[i] > .5 {
			obs[b]++
		}
	}
	for i := range bins {
		if bins[i].N > 0 {
			bins[i].Predicted = sums[i] / float64(bins[i].N)
			bins[i].Observed = obs[i] / float64(bins[i].N)
		}
	}
	return bins
}

// TrainOptionB fits the regression on tasks that ended before split and scores
// it, and today's binned clean table, on the later tasks.
func TrainOptionB(s *Snapshot, split int64) (*BinResult, *logistic) {
	res := &BinResult{Split: split}
	var train, test []Task
	for _, t := range s.Tasks {
		if t.End < split {
			train = append(train, t)
		} else {
			test = append(test, t)
		}
	}
	res.Train, res.Test = len(train), len(test)
	res.TrainFirst, res.TrainLast = span(train)
	res.TestFirst, res.TestLast = span(test)
	spec := newFeatureSpec(train)
	xs := make([][]float64, 0, len(train))
	ys := make([]float64, 0, len(train))
	for _, t := range train {
		xs = append(xs, spec.vector(inputFor(t)))
		ys = append(ys, cleanY(t))
	}
	if len(xs) == 0 {
		return res, nil
	}
	reg := fitLogistic(xs, ys, spec)
	res.BaseRate = mean(ys)

	table := protocol.BuildPickerTable(pickerTasks(train, math.MaxInt64, func(t Task) float64 { return t.ScoreToday }), protocol.DefaultPickerSettings())
	var psB, psBin, ysTest []float64
	for _, t := range test {
		psB = append(psB, reg.predict(spec.vector(inputFor(t))))
		psBin = append(psBin, table.CleanCell(t.Key, table.Bucket(t.D)).Score)
		ysTest = append(ysTest, cleanY(t))
	}
	res.LogLossB, res.LogLossBin = logLoss(psB, ysTest), logLoss(psBin, ysTest)
	res.BrierB, res.BrierBin = brier(psB, ysTest), brier(psBin, ysTest)
	res.CalibrationB = calibration(psB, ysTest)
	res.CalibrationBin = calibration(psBin, ysTest)
	return res, reg
}

func inputFor(t Task) regInput {
	return regInput{D: t.D, Scores: t.Scores, Project: t.Project, Key: t.Key, Harness: t.Harness, Family: modelFamily(t.Key), Prov: providerOf(t.Key)}
}

func cleanY(t Task) float64 {
	if t.Dirty {
		return 0
	}
	return 1
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	var sum float64
	for _, x := range v {
		sum += x
	}
	return sum / float64(len(v))
}

func span(tasks []Task) (int64, int64) {
	if len(tasks) == 0 {
		return 0, 0
	}
	lo, hi := tasks[0].End, tasks[0].End
	for _, t := range tasks[1:] {
		lo = min(lo, t.End)
		hi = max(hi, t.End)
	}
	return lo, hi
}
