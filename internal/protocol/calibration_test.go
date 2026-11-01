package protocol

import (
	"math"
	"testing"
)

func calSamples(caller, level string, fix int, n int) []CalibrationSample {
	out := make([]CalibrationSample, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, CalibrationSample{Caller: caller, Difficulty: level, Fix: fix, EndedAt: int64(i + 1)})
	}
	return out
}

func calCaller(cal Calibration, caller string) (CalibrationCaller, bool) {
	for _, c := range cal.Callers {
		if c.Caller == caller {
			return c, true
		}
	}
	return CalibrationCaller{}, false
}

func calLevel(c CalibrationCaller, level string) (CalibrationLevel, bool) {
	for _, l := range c.Levels {
		if l.Difficulty == level {
			return l, true
		}
	}
	return CalibrationLevel{}, false
}

// Fewer than five tasks cannot move an offset: the table says so instead.
func TestCalibrationNeedsFiveTasks(t *testing.T) {
	cal := BuildCalibration(calSamples("s_a", "normal", 3, 4), map[string]bool{"s_a": true})
	c, ok := calCaller(cal, "s_a")
	if !ok {
		t.Fatal("caller missing")
	}
	l, ok := calLevel(c, "normal")
	if !ok {
		t.Fatal("level missing")
	}
	if l.Tasks != 4 || l.Action != CalibrationNotEnough {
		t.Fatalf("four tasks: %+v", l)
	}
	if _, ok := calLevel(c, "hard"); ok {
		t.Fatal("a level with no tasks was shown")
	}
}

// The window keeps the newest seven tasks, so an older bad task cannot swing
// the median once seven newer tasks exist.
func TestCalibrationWindowKeepsLastSeven(t *testing.T) {
	var samples []CalibrationSample
	// The oldest task is clean, the seven newer ones alternate 0 and 2.
	samples = append(samples, CalibrationSample{Caller: "s_a", Difficulty: "normal", Fix: 0, EndedAt: 1})
	for i := 0; i < 7; i++ {
		fix := 0
		if i%2 == 0 {
			fix = 2
		}
		samples = append(samples, CalibrationSample{Caller: "s_a", Difficulty: "normal", Fix: fix, EndedAt: int64(i + 2)})
	}
	// Pass them out of order: the window sorts by end time.
	samples[0], samples[len(samples)-1] = samples[len(samples)-1], samples[0]
	win := CalibrationWindowSamples(samples)
	if len(win) != CalibrationWindow {
		t.Fatalf("window has %d tasks", len(win))
	}
	for _, s := range win {
		if s.EndedAt == 1 {
			t.Fatal("the oldest task is still in the window")
		}
	}
	got := MedianRework(win)
	if got != 2 {
		t.Fatalf("median of the last seven = %v, want 2 (four 2s and three 0s)", got)
	}
	cal := BuildCalibration(samples, map[string]bool{"s_a": true})
	c, _ := calCaller(cal, "s_a")
	l, _ := calLevel(c, "normal")
	if l.Tasks != 7 || l.Median != 2 {
		t.Fatalf("window in calibration: %+v", l)
	}
}

func TestCalibrationWeights(t *testing.T) {
	if got := ReworkWeight(1, 0); got != 1 {
		t.Errorf("fix weight = %v", got)
	}
	if got := ReworkWeight(0, 1); got != 0.33 {
		t.Errorf("continue weight = %v", got)
	}
	if got := ReworkWeight(1, 1); got != 1.33 {
		t.Errorf("fix plus continue = %v", got)
	}
	got := MedianRework([]CalibrationSample{
		{Fix: 1}, {Continue: 1}, {Fix: 0}, {Fix: 1},
	})
	if math.Abs(got-0.665) > 1e-9 {
		t.Errorf("median = %v, want 0.665", got)
	}
}

func TestCalibrationStrongCallerGuard(t *testing.T) {
	cases := []struct {
		name   string
		median float64
		tasks  int
		strong bool
		want   string
	}{
		{"strong lowers", 0.4, 5, true, CalibrationLower},
		{"weak cannot lower", 0.4, 5, false, CalibrationKeep},
		{"any caller raises", 1.6, 5, false, CalibrationRaise},
		{"strong raises", 1.6, 5, true, CalibrationRaise},
		{"low edge keeps", 0.5, 5, true, CalibrationKeep},
		{"high edge keeps", 1.5, 5, true, CalibrationKeep},
		{"too few tasks", 9, 4, true, CalibrationNotEnough},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CalibrationAction(tc.median, tc.tasks, tc.strong); got != tc.want {
				t.Errorf("action = %q, want %q", got, tc.want)
			}
		})
	}
	if CalibrationOffset(CalibrationLower) != -1 || CalibrationOffset(CalibrationRaise) != 1 || CalibrationOffset(CalibrationKeep) != 0 {
		t.Error("offset is not one tier")
	}
}

// The network offset comes from strong callers only. A weak caller uses it,
// and its own low median cannot lower a tier.
func TestCalibrationNetworkOffsetFromStrongCallers(t *testing.T) {
	var samples []CalibrationSample
	samples = append(samples, calSamples("s_strong1", "hard", 0, 5)...)
	samples = append(samples, calSamples("s_strong2", "hard", 0, 5)...)
	// A strong caller with a clean record keeps its own offset at hard.
	samples = append(samples, calSamples("s_strong3", "hard", 1, 5)...)
	// A weak caller with a low median still cannot lower, and follows the
	// network's -1 tier.
	samples = append(samples, calSamples("s_weak1", "hard", 0, 5)...)
	// A weak caller can still raise.
	samples = append(samples, calSamples("s_weak2", "hard", 2, 5)...)
	// Too few own tasks means the network offset.
	samples = append(samples, calSamples("s_weak3", "hard", 0, 3)...)

	strong := map[string]bool{"s_strong1": true, "s_strong2": true, "s_strong3": true}
	cal := BuildCalibration(samples, strong)

	var network CalibrationLevel
	for _, l := range cal.Network {
		if l.Difficulty == "hard" {
			network = l
		}
	}
	if network.Difficulty != "hard" || network.Action != CalibrationLower || network.Offset != -1 {
		t.Fatalf("network hard: %+v", cal.Network)
	}

	cases := []struct {
		caller string
		action string
		offset int
	}{
		{"s_strong3", CalibrationKeep, 0},
		{"s_weak1", CalibrationKeep, -1},
		{"s_weak2", CalibrationRaise, 0},
		{"s_weak3", CalibrationNotEnough, -1},
	}
	for _, tc := range cases {
		c, ok := calCaller(cal, tc.caller)
		if !ok {
			t.Fatalf("%s missing", tc.caller)
		}
		l, ok := calLevel(c, "hard")
		if !ok {
			t.Fatalf("%s hard missing", tc.caller)
		}
		if l.Action != tc.action || l.Offset != tc.offset {
			t.Errorf("%s: action %q offset %d, want %q %d", tc.caller, l.Action, l.Offset, tc.action, tc.offset)
		}
	}
}
