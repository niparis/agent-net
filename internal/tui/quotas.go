package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

type quotaRow struct {
	provider protocol.QuotaProvider
	at       int64
	machine  string
	readers  int
}

// quotaRows merges readings for the same provider and plan. A local report
// replaces the gateway copy of that machine.
func (m Model) quotaRows() []quotaRow {
	if !m.usage.have {
		return nil
	}
	nodes, ids := m.quotaNodes()
	rows := map[string]quotaRow{}
	for _, id := range ids {
		n := nodes[id]
		q := n.Usage.Quotas
		if q == nil {
			continue
		}
		for _, p := range q.Providers {
			key := strings.ToLower(strings.TrimSpace(p.Provider)) + "\x00" + strings.ToLower(strings.TrimSpace(p.Plan))
			r, ok := rows[key]
			r.readers++
			if !ok || q.GeneratedAt > r.at {
				r.provider = p
				r.at = q.GeneratedAt
				r.machine = firstNonEmpty(n.Hostname, id)
			}
			rows[key] = r
		}
	}
	counts := map[string]int{}
	for _, r := range rows {
		counts[strings.ToLower(r.provider.Provider)]++
	}
	out := make([]quotaRow, 0, len(rows))
	for _, r := range rows {
		if counts[strings.ToLower(r.provider.Provider)] == 1 && r.readers > 1 {
			r.machine = ""
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].provider.Provider != out[j].provider.Provider {
			return out[i].provider.Provider < out[j].provider.Provider
		}
		return out[i].provider.Plan < out[j].provider.Plan
	})
	return out
}

// quotaNodes selects the live local usage over the gateway copy.
func (m Model) quotaNodes() (map[string]protocol.UsageNode, []string) {
	nodes := map[string]protocol.UsageNode{}
	if !m.usage.have {
		return nodes, nil
	}
	if g := m.usage.raw.Gateway; g != nil {
		for _, n := range g.Nodes {
			nodes[n.NodeID] = n
		}
	}
	if l := m.usage.raw.Local; l != nil {
		nodes[m.usage.raw.NodeID] = protocol.UsageNode{NodeID: m.usage.raw.NodeID, Hostname: m.usage.raw.Hostname, Usage: *l}
	}
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return nodes, ids
}

func (m Model) quotaWarning() string {
	for _, r := range m.quotaRows() {
		if r.provider.Stale || r.provider.Error != "" {
			continue
		}
		for _, w := range r.provider.Windows {
			if w.Exhausted || w.PercentUsed >= 100 {
				return fmt.Sprintf("%s exhausted %s - see 3", sanitize(r.provider.Provider), sanitize(protocol.QuotaWindowName(w)))
			}
			if w.PercentUsed > 80 {
				return fmt.Sprintf("%s %.0f%% %s - see 3", sanitize(r.provider.Provider), w.PercentUsed, sanitize(protocol.QuotaWindowName(w)))
			}
		}
	}
	return ""
}

type quotaModel struct {
	name, harness, model, defaultEffort           string
	score                                         protocol.ModelScore
	used                                          int
	last                                          int64
	failed, allowed, scored, succeeded, available bool
}

// quotaModels lists only registered profiles, including unavailable profiles.
func (m Model) quotaModels() []quotaModel {
	quotaNodes, _ := m.quotaNodes()
	var rows []quotaModel
	for _, p := range m.snap.WorkerProfiles {
		model := p.Model
		if p.Effort != "" {
			model += ":" + p.Effort
		}
		row := quotaModel{name: p.Name, harness: p.Harness, model: model, failed: true}
		found := false
		for _, n := range m.snap.Nodes {
			if !n.Online {
				continue
			}
			for _, h := range n.Harnesses {
				if h.Name != p.Harness {
					continue
				}
				installed := false
				for _, id := range h.Models {
					if id == p.Model {
						installed = true
						break
					}
				}
				if !installed {
					continue
				}
				quota := n.Quotas
				if usage, ok := quotaNodes[n.NodeID]; ok && usage.Usage.Quotas != nil {
					quota = usage.Usage.Quotas
				}
				allowed := protocol.WorkerProviderAllowed(h.Name, p.Model, m.snap.WorkerProviders, quota)
				row.available = true
				row.allowed = row.allowed || allowed
				if !found {
					row.defaultEffort = h.ModelDefaultEfforts[p.Model]
					found = true
				}
				if allowed {
					row.failed = row.failed && n.RecentModelFailures[h.Name][model]
					row.succeeded = row.succeeded || n.RecentModelSuccesses[h.Name][model]
				}
			}
		}
		row.score, row.scored = protocol.LookupModelScoreWithDefaultEffort(model, row.defaultEffort, m.snap.Scores)
		for _, a := range m.snap.Agents {
			if a.Harness == p.Harness && a.Model == model {
				if a.LastSeen > row.last {
					row.last = a.LastSeen
				}
				if a.LastSeen >= m.now().Add(-7*24*time.Hour).Unix() {
					row.used++
				}
			}
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	return rows
}

func (m Model) quotaModelsView(w int) []string {
	rows := m.quotaModels()
	out := []string{"", sBold.Render("  MODELS"), sDim.Render(fmt.Sprintf("  %d registered workers · USED 7 DAYS", len(rows)))}
	if len(rows) == 0 {
		return out
	}
	nameWidth, harnessWidth, modelWidth := 7, 8, 20
	if w >= 100 {
		nameWidth, harnessWidth, modelWidth = 18, 9, 32
	}
	heading := fmt.Sprintf("  #  %-*s %-*s %-*s %-11s T  S  N  H  V  7D LAST", nameWidth, "NAME", harnessWidth, "HARNESS", modelWidth, "MODEL", "SCORE")
	out = append(out, sBold.Render(clip(heading, w)))
	levels := []string{"trivial", "simple", "normal", "hard", "very_complex"}
	candidates := make([]protocol.WorkerModelCandidate, 0, len(rows))
	for _, r := range rows {
		if r.available && r.allowed && r.scored {
			candidates = append(candidates, protocol.WorkerModelCandidate{Harness: r.harness, Model: r.model, DefaultEffort: r.defaultEffort, Failed: r.failed, Succeeded: r.succeeded})
		}
	}
	q := &protocol.Quotas{}
	for _, r := range m.quotaRows() {
		q.Providers = append(q.Providers, r.provider)
		if r.at > q.GeneratedAt {
			q.GeneratedAt = r.at
		}
	}
	running := map[string]int{}
	for _, a := range m.snap.Agents {
		if a.Online && a.Worker != nil {
			running[protocol.QuotaProviderForStart(a.Harness, a.Model)]++
		}
	}
	picks := make([]protocol.WorkerModelChoice, len(levels))
	for i, level := range levels {
		picks[i], _ = protocol.ChooseWorkerModel(candidates, level, m.snap.Scores, m.snap.DifficultyMinimums, q, running, 0)
	}
	attribution := false
	for rowIndex, r := range rows {
		attribution = attribution || r.score.Source == "artificialanalysis"
		var fits []string
		for i, level := range levels {
			letter := "-"
			if r.scored {
				fit, _ := protocol.DifficultyFit(r.score.Score, level, m.snap.DifficultyMinimums)
				letter = map[string]string{"excellent": "E", "good": "G", "poor": "P", "denied": "-"}[fit]
			}
			if picks[i].Model == r.model && picks[i].Harness == r.harness {
				letter += "*"
			}
			fits = append(fits, fmt.Sprintf("%-2s", letter))
		}
		last := "-"
		if r.last > 0 {
			last = time.Unix(r.last, 0).In(m.loc).Format("Jan 02")
		}
		score := "-"
		if r.scored {
			score = fmt.Sprintf("%g", r.score.Score)
		}
		if r.score.Effort != "" {
			score += "/" + r.score.Effort
		}
		line := fmt.Sprintf("  %-2d %-*s %-*s %-*s %-11s %s %2d %s", rowIndex+1, nameWidth, cell(sanitize(r.name), nameWidth), harnessWidth, cell(r.harness, harnessWidth), modelWidth, cell(sanitize(r.model), modelWidth), score, strings.Join(fits, " "), r.used, last)
		if !r.available || !r.allowed {
			out = append(out, sDim.Render(clip(line, w)))
			reason := "not available on an online machine"
			if r.available {
				reason = "not a worker provider"
			}
			out = append(out, sDim.Render(clip("     ("+reason+")", w)))
		} else {
			out = append(out, clip(line, w))
		}
		if len(r.name) > nameWidth || len(r.model) > modelWidth {
			detail := "     " + sanitize(r.name) + " · " + sanitize(r.model)
			out = append(out, sDim.Render(clip(detail, w)))
		}
	}

	var choices []string
	for i, level := range levels {
		name := "-"
		for j, r := range rows {
			if picks[i].Model == r.model && picks[i].Harness == r.harness {
				name = fmt.Sprintf("#%d", j+1)
				break
			}
		}
		choices = append(choices, level+" -> "+name)
	}
	out = append(out, sDim.Render(clip("  now: "+strings.Join(choices, ", "), w)))
	if attribution {
		out = append(out, sDim.Render("  scores: Artificial Analysis (artificialanalysis.ai)"))
	}
	return out
}
