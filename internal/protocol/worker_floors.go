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
	provider := ProfileProvider(harness, model)
	qp := QuotaProviderForStart(harness, model)

	floor := floors.Floor(provider)
	if floor <= 0 && qp != "" {
		floor = floors.Floor(qp)
	}
	if floor <= 0 {
		return false, "", false
	}

	if quotas == nil {
		return false, "no quota reading, floor not applied", true
	}

	p, ok := quotas.Provider(qp)
	if !ok || !p.usable() || len(p.Windows) == 0 {
		p, ok = quotas.Provider(provider)
	}
	if !ok || !p.usable() || len(p.Windows) == 0 {
		return false, "no quota reading, floor not applied", true
	}

	var worstWindow QuotaWindow
	worstRemaining := math.MaxInt
	foundUnder := false

	for _, w := range p.Windows {
		if !QuotaWindowServes(w, model) {
			continue
		}
		var remaining int
		if w.Exhausted || w.PercentUsed >= 100 {
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
		sentence := fmt.Sprintf("%s has %d%% left in its %s window, under your %d%% worker floor", provider, worstRemaining, QuotaWindowName(worstWindow), floor)
		return true, sentence, false
	}

	return false, "", false
}
