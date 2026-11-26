package protocol

import (
	"fmt"
	"math"
	"strings"
)

const (
	OpWorkerFloorsGet = "worker_floors_get"
	OpWorkerFloorsSet = "worker_floors_set"
)

// WorkerFloors holds per-provider minimum remaining quota percent that workers must leave untouched.
type WorkerFloors struct {
	Floors     map[string]int `json:"floors"`
	Configured bool           `json:"configured,omitempty"`
}

// canonicalFloorProvider normalizes provider aliases so Anthropic/Claude and OpenAI/Codex share floors.
func canonicalFloorProvider(provider string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	switch p {
	case "claude":
		return "anthropic"
	case "codex":
		return "openai"
	}
	if mapped := QuotaProviderForModelProvider(p); mapped != "" {
		if mapped == "claude" {
			return "anthropic"
		}
		if mapped == "codex" {
			return "openai"
		}
		return mapped
	}
	return p
}

// Floor returns the configured floor percentage (0 = off) for a provider name.
func (wf WorkerFloors) Floor(provider string) int {
	if len(wf.Floors) == 0 {
		return 0
	}
	p := strings.ToLower(strings.TrimSpace(provider))
	canonical := canonicalFloorProvider(p)

	// Direct match
	if v, ok := wf.Floors[p]; ok {
		return v
	}
	if v, ok := wf.Floors[canonical]; ok {
		return v
	}

	// Case-insensitive / canonical match against existing keys
	for k, v := range wf.Floors {
		if strings.EqualFold(k, p) || strings.EqualFold(canonicalFloorProvider(k), canonical) {
			return v
		}
	}
	return 0
}

// CheckWorkerFloor checks whether the model's provider has a worker floor, and whether
// any known window is below that floor.
// If under floor: returns true, sentence ("<provider> has <N>% left in its <window> window, under your <F>% worker floor"), false.
// If provider has a floor but no quota reading: returns false, "no quota reading, floor not applied", true.
// If above floor or no floor set: returns false, "", false.
func CheckWorkerFloor(quotas *Quotas, floors WorkerFloors, harness, model string) (underFloor bool, note string, noReading bool) {
	r := WorkerFloorRead(quotas, floors, harness, model)
	return r.Under, r.Note, r.NoReading
}

// WorkerFloorReading is what the floor check read for one model: the floor in
// force for its provider, and the window under it when there is one.
type WorkerFloorReading struct {
	// Floor is the owner's worker floor in percent left, 0 when none is set.
	Floor     int
	Under     bool
	Window    QuotaWindow
	Note      string
	NoReading bool
}

// WorkerFloorRead is CheckWorkerFloor with the floor and the window that
// decided it, so a pick can record what it read.
func WorkerFloorRead(quotas *Quotas, floors WorkerFloors, harness, model string) WorkerFloorReading {
	provider := ProfileProvider(harness, model)
	qp := QuotaProviderForStart(harness, model)

	floor := floors.Floor(provider)
	if floor <= 0 && qp != "" {
		floor = floors.Floor(qp)
	}
	if floor <= 0 {
		return WorkerFloorReading{}
	}
	read := WorkerFloorReading{Floor: floor}

	if quotas == nil {
		read.Note, read.NoReading = "no quota reading, floor not applied", true
		return read
	}

	p, ok := quotas.Provider(qp)
	if !ok || !p.usable() || len(p.Windows) == 0 {
		p, ok = quotas.Provider(provider)
	}
	if !ok || !p.usable() || len(p.Windows) == 0 {
		read.Note, read.NoReading = "no quota reading, floor not applied", true
		return read
	}

	var worstWindow QuotaWindow
	worstRemaining := math.MaxInt
	foundUnder := false

	for _, w := range p.Windows {
		if !QuotaWindowServes(w, model) {
			continue
		}
		var remaining int
		if QuotaWindowUsedUp(w) {
			remaining = 0
		} else {
			remaining = int(math.Round(math.Max(0, math.Min(100, 100-w.PercentUsed))))
		}
		if remaining < floor {
			if !foundUnder || remaining < worstRemaining {
				worstRemaining = remaining
				worstWindow = w
				foundUnder = true
			}
		}
	}

	if foundUnder {
		read.Under, read.Window = true, worstWindow
		read.Note = fmt.Sprintf("%s has %d%% left in its %s window, under your %d%% worker floor", provider, worstRemaining, QuotaWindowName(worstWindow), floor)
	}
	return read
}
