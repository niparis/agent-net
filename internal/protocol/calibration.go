package protocol

import (
	"sort"
)

// Difficulty calibration, part 2 (docs/design/difficulty-calibration.md).
//
// The loop reads how much rework a caller's recent tasks needed and says what
// it would do with that caller's label. Part 2 is read only: nothing here is
// used by ChooseWorkerModel or by a worker start.

const (
	// CalibrationWindow is how many finished tasks a median reads.
	CalibrationWindow = 7
	// CalibrationMinTasks is the least a window may hold before it can move
	// an offset.
	CalibrationMinTasks = 5
	// CalibrationFixWeight and CalibrationContinueWeight make one task's
	// weighted rework: a fix costs review attention, a continue is cheaper.
	// The owner set the continue weight to 0.33 on 2026-11-01.
	CalibrationFixWeight      = 1.0
	CalibrationContinueWeight = 0.33
	// CalibrationStrongScore is the model score at or above which a caller is
	// strong enough to lower a tier (decision 0026 scores).
	CalibrationStrongScore = 45.0
	// CalibrationLowerBelow and CalibrationRaiseAbove bracket the target
	// median of about one.
	CalibrationLowerBelow = 0.5
	CalibrationRaiseAbove = 1.5
)

// DifficultyLevels is the tier order, weakest first.
var DifficultyLevels = []string{"trivial", "simple", "normal", "hard", "very_complex"}

// What the loop would do with one caller and difficulty.
const (
	CalibrationRaise     = "raise"
	CalibrationLower     = "lower"
	CalibrationKeep      = "keep"
	CalibrationNotEnough = "not enough tasks"
)

// CalibrationSample is one finished task as the calibration reads it: who
// asked, the difficulty it asked for, the two rework counters and when it
// ended. EndedAt orders the window; ties keep their input order.
type CalibrationSample struct {
	Caller     string
	Difficulty string
	Continue   int
	Fix        int
	EndedAt    int64
}

// CalibrationLevel is one caller's row for one difficulty. Offset is the
// number of tiers the loop would move the label; Action says which way.
type CalibrationLevel struct {
	Difficulty string  `json:"difficulty"`
	Median     float64 `json:"median"`
	Tasks      int     `json:"tasks"`
	Action     string  `json:"action"`
	Offset     int     `json:"offset"`
}

// CalibrationCaller is one caller and its levels. Strong is true when the
// caller's own model scores at least CalibrationStrongScore.
type CalibrationCaller struct {
	Caller string             `json:"caller"`
	Name   string             `json:"name,omitempty"`
	Model  string             `json:"model,omitempty"`
	Strong bool               `json:"strong"`
	Levels []CalibrationLevel `json:"levels,omitempty"`
}

// Calibration is the read-only table: one entry per caller, plus the network
// offsets learned from strong callers.
type Calibration struct {
	Quality QualityAnalysis     `json:"quality"`
	Callers []CalibrationCaller `json:"callers,omitempty"`
	Network []CalibrationLevel  `json:"network,omitempty"`
	// Ratings is how many satisfaction ratings each model has (part 4). It is
	// read only, like the rest of the table.
	Ratings []CalibrationRating `json:"ratings,omitempty"`
	// Defects is how many defects were reported later on workers in the
	// window. Each report also adds its severity to the task's median.
	Defects int `json:"defects,omitempty"`
}

// ReworkWeight is one task's weighted rework: fix + 0.33 * continue. Defect
// severities are added by the gateway before this is called.
func ReworkWeight(fix, cont int) float64 {
	if fix < 0 {
		fix = 0
	}
	if cont < 0 {
		cont = 0
	}
	return CalibrationFixWeight*float64(fix) + CalibrationContinueWeight*float64(cont)
}

// CalibrationWindowSamples returns the newest CalibrationWindow tasks,
// oldest first. Ties keep their input order.
func CalibrationWindowSamples(samples []CalibrationSample) []CalibrationSample {
	ordered := append([]CalibrationSample(nil), samples...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].EndedAt < ordered[j].EndedAt })
	if len(ordered) > CalibrationWindow {
		ordered = ordered[len(ordered)-CalibrationWindow:]
	}
	return ordered
}

// MedianRework is the median weighted rework of the samples. An even count
// averages the two middle values. No samples gives 0.
func MedianRework(samples []CalibrationSample) float64 {
	if len(samples) == 0 {
		return 0
	}
	values := make([]float64, len(samples))
	for i, s := range samples {
		values[i] = ReworkWeight(s.Fix, s.Continue)
	}
	sort.Float64s(values)
	m := len(values) / 2
	if len(values)%2 == 1 {
		return values[m]
	}
	return (values[m-1] + values[m]) / 2
}

// CalibrationAction says what the loop would do with a window. Fewer than
// five tasks is not enough. A weak caller may raise a tier but not lower one.
func CalibrationAction(median float64, tasks int, strong bool) string {
	if tasks < CalibrationMinTasks {
		return CalibrationNotEnough
	}
	switch {
	case median < CalibrationLowerBelow:
		if !strong {
			return CalibrationKeep
		}
		return CalibrationLower
	case median > CalibrationRaiseAbove:
		return CalibrationRaise
	default:
		return CalibrationKeep
	}
}

// CalibrationOffset turns an action into a tier step.
func CalibrationOffset(action string) int {
	switch action {
	case CalibrationLower:
		return -1
	case CalibrationRaise:
		return 1
	default:
		return 0
	}
}

// BuildCalibration reads the samples per caller and difficulty.
//
// A caller's own last-seven window decides its action. A caller with fewer
// than five own tasks starts from the network offset. A weak caller also
// starts from the network offset: its own record can raise it a tier but
// never lower it. The network offset for a difficulty pools the last-seven
// windows of the strong callers, because only they can tell good work from
// bad.
func BuildCalibration(samples []CalibrationSample, strong map[string]bool) Calibration {
	byCaller := map[string]map[string][]CalibrationSample{}
	callers := []string{}
	for _, s := range samples {
		if s.Caller == "" || difficultyIndex(s.Difficulty) < 0 {
			continue
		}
		if _, ok := byCaller[s.Caller]; !ok {
			byCaller[s.Caller] = map[string][]CalibrationSample{}
			callers = append(callers, s.Caller)
		}
		byCaller[s.Caller][s.Difficulty] = append(byCaller[s.Caller][s.Difficulty], s)
	}
	sort.Strings(callers)

	networkSamples := map[string][]CalibrationSample{}
	for caller, levels := range byCaller {
		if !strong[caller] {
			continue
		}
		for level, list := range levels {
			networkSamples[level] = append(networkSamples[level], CalibrationWindowSamples(list)...)
		}
	}
	network := []CalibrationLevel{}
	networkOffset := map[string]int{}
	for _, level := range DifficultyLevels {
		list := networkSamples[level]
		if len(list) == 0 {
			continue
		}
		median := MedianRework(list)
		action := CalibrationAction(median, len(list), true)
		offset := CalibrationOffset(action)
		network = append(network, CalibrationLevel{Difficulty: level, Median: median, Tasks: len(list), Action: action, Offset: offset})
		networkOffset[level] = offset
	}

	out := Calibration{Network: network}
	for _, caller := range callers {
		c := CalibrationCaller{Caller: caller, Strong: strong[caller]}
		for _, level := range DifficultyLevels {
			list := CalibrationWindowSamples(byCaller[caller][level])
			if len(list) == 0 {
				continue
			}
			median := MedianRework(list)
			action := CalibrationAction(median, len(list), c.Strong)
			offset := networkOffset[level]
			switch {
			case len(list) >= CalibrationMinTasks && c.Strong:
				offset = CalibrationOffset(action)
			case len(list) >= CalibrationMinTasks && action == CalibrationRaise:
				offset = clampOffset(offset + 1)
			}
			c.Levels = append(c.Levels, CalibrationLevel{Difficulty: level, Median: median, Tasks: len(list), Action: action, Offset: offset})
		}
		out.Callers = append(out.Callers, c)
	}
	return out
}

func difficultyIndex(level string) int {
	for i, name := range DifficultyLevels {
		if name == level {
			return i
		}
	}
	return -1
}

func clampOffset(offset int) int {
	if offset < -2 {
		return -2
	}
	if offset > 2 {
		return 2
	}
	return offset
}
