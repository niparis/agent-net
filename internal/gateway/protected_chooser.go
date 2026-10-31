package gateway

import (
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

func isProtected(harness, model string, setting protocol.ProtectedProviders) bool {
	provider := protocol.ProfileProvider(harness, model)
	for _, p := range setting.Providers {
		if strings.EqualFold(p, provider) {
			return true
		}
	}
	return false
}

func chooseProtectedWorker(candidates []protocol.WorkerModelCandidate, level string, scores []protocol.ModelScore, minimums map[string]float64, quotas *protocol.Quotas, running map[string]int, rotation int, setting protocol.ProtectedProviders) (protocol.WorkerModelChoice, string, error) {
	if len(setting.Providers) == 0 {
		c, err := protocol.ChooseWorkerModel(candidates, level, scores, minimums, quotas, running, rotation)
		return c, "", err
	}
	var ordinary, protected []protocol.WorkerModelCandidate
	var skipped []string
	for _, c := range candidates {
		score, ok := protocol.LookupModelScoreWithDefaultEffort(c.Model, c.DefaultEffort, scores)
		if !ok {
			continue
		}
		fit, err := protocol.DifficultyFit(score.Score, level, minimums)
		if err != nil {
			return protocol.WorkerModelChoice{}, "", err
		}
		if fit != "good" && fit != "excellent" {
			continue
		}
		if isProtected(c.Harness, c.Model, setting) {
			protected = append(protected, c)
			name := c.Name
			if name == "" {
				name = c.Harness + " " + c.Model
			}
			skipped = append(skipped, fmt.Sprintf("skipped %s: protected provider (%s), saved for your orchestrator", name, protocol.ProfileProvider(c.Harness, c.Model)))
		} else {
			ordinary = append(ordinary, c)
		}
	}
	if len(ordinary) > 0 {
		c, err := protocol.ChooseWorkerModel(ordinary, level, scores, minimums, quotas, running, rotation)
		if err == nil {
			return c, strings.Join(skipped, "; "), nil
		}
	}
	c, err := protocol.ChooseWorkerModel(protected, level, scores, minimums, quotas, running, rotation)
	if err != nil {
		return c, "", err
	}
	reason := setting.Reason
	if reason == "" {
		reason = "your orchestrator's provider"
	}
	return c, fmt.Sprintf("used %s %s (protected: %s) because no other profile meets %s, or the others are out of quota", c.Harness, c.Model, reason, level), nil
}
