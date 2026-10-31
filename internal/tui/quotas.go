package tui

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/niparis/agent-net/internal/protocol"
)

type quotaRow struct {
	provider protocol.QuotaProvider
	at       int64
	machine  string
	readers  int
	disagree bool
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
			if ok && !reflect.DeepEqual(r.provider, p) {
				r.disagree = true
			}
			r.readers++
			if !ok || q.GeneratedAt > r.at {
				r.provider = p
				r.at = q.GeneratedAt
				r.machine = firstNonEmpty(n.Hostname, id)
			}
			rows[key] = r
		}
	}

	out := make([]quotaRow, 0, len(rows))
	for _, r := range rows {
		if m.tab == 2 && m.uiFilter != "" && !strings.Contains(strings.ToLower(r.provider.Provider+" "+r.provider.Plan+" "+r.machine), strings.ToLower(m.uiFilter)) {
			continue
		}
		if r.readers > 1 && !r.disagree {
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

func (m Model) quotaRelevantProviders() map[string]bool {
	relevant := map[string]bool{}
	for _, p := range m.snap.WorkerProfiles {
		if name := protocol.QuotaProviderForStart(p.Harness, p.Model); name != "" {
			relevant[name] = true
		}
	}
	for _, a := range m.snap.Agents {
		if a.Online {
			if name := protocol.QuotaProviderForStart(a.Harness, a.Model); name != "" {
				relevant[name] = true
			}
		}
	}
	return relevant
}

func (m Model) quotaWarning() (string, bool) {
	relevant := m.quotaRelevantProviders()
	for _, r := range m.quotaRows() {
		if !relevant[strings.ToLower(r.provider.Provider)] || r.provider.Stale || r.provider.Error != "" {
			continue
		}
		for _, w := range r.provider.Windows {
			if w.Exhausted || w.PercentUsed >= 100 {
				return fmt.Sprintf("%s exhausted %s · 3", sanitize(r.provider.Provider), sanitize(protocol.QuotaWindowName(w))), true
			}
			if w.PercentUsed > 80 {
				return fmt.Sprintf("%s %.0f%% %s · 3", sanitize(r.provider.Provider), w.PercentUsed, sanitize(protocol.QuotaWindowName(w))), false
			}
		}
	}
	return "", false
}

type quotaModel struct {
	name, harness, model, defaultEffort           string
	score                                         protocol.ModelScore
	used, done, failedRuns                        int
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
			for _, track := range n.Tracks {
				if track.Harness == p.Harness && track.Model == model {
					row.used += track.Runs
					row.done += track.Complete
					row.failedRuns += track.Failed + track.TimedOut
				}
			}
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

			}
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	return rows
}

func (m Model) quotaModelsView(w int) []string {
	rows := m.quotaModels()
	title := fmt.Sprintf("Workers · %d registered · scores: Artificial Analysis", len(rows))
	var out []string
	nameWidth, harnessWidth, modelWidth := 18, 8, 26
	header := fmt.Sprintf("%-*s %-*s %-*s %-11s T S N H V  USED", nameWidth, "NAME", harnessWidth, "HARNESS", modelWidth, "MODEL", "SCORE")
	if w < 100 {
		header = fmt.Sprintf("%-18s %-8s %-26s %-5s T S N H V", "NAME", "HARNESS", "MODEL", "SCORE")
	}
	out = append(out, sDim.Render(header))
	levels := []string{"trivial", "simple", "normal", "hard", "very_complex"}
	levelNames := []string{"T", "S", "N", "H", "V"}
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
	for _, r := range rows {
		var fits, chosen []string
		for i, level := range levels {
			letter := "-"
			color := roleFaint
			if r.scored {
				fit, _ := protocol.DifficultyFit(r.score.Score, level, m.snap.DifficultyMinimums)
				switch fit {
				case "excellent":
					letter = "E"
					color = roleGood
				case "good":
					letter = "G"
					color = roleGood
				case "poor":
					letter = "P"
					color = roleWarning
				}
			}
			style := lipgloss.NewStyle().Foreground(color)
			if letter == "G" {
				style = style.Faint(true)
			}
			fits = append(fits, style.Render(letter))
			if picks[i].Model == r.model && picks[i].Harness == r.harness && picks[i].Model != "" {
				chosen = append(chosen, levelNames[i])
			}
		}
		score := "-"
		if r.scored {
			score = fmt.Sprintf("%g", r.score.Score)
		}
		if r.score.Effort != "" {
			score += "/" + r.score.Effort
		}
		usage := fmt.Sprintf("%d runs", r.used)
		if w >= 100 {
			usage += " " + sBusy.Render(fmt.Sprintf("%d done", r.done)) + " " + sBad.Render(fmt.Sprintf("%d failed", r.failedRuns))
		}
		line := fmt.Sprintf("%-*s %-*s %-*s %-11s %s  %s", nameWidth, cell(sanitize(r.name), nameWidth), harnessWidth, cell(r.harness, harnessWidth), modelWidth, cell(sanitize(r.model), modelWidth), cell(score, 11), strings.Join(fits, " "), usage)
		if w < 100 {
			shortScore := "-"
			if r.scored {
				shortScore = fmt.Sprintf("%g", r.score.Score)
			}
			line = fmt.Sprintf("%-18s %-8s %-26s %-5s %s", cell(sanitize(r.name), 18), cell(r.harness, 8), cell(sanitize(r.model), 26), cell(shortScore, 5), strings.Join(fits, " "))
		}
		if len(chosen) > 0 {
			marker := "◀ " + strings.Join(chosen, ",")
			if w < 100 {
				marker = "◀"
			}
			line += "  " + lipgloss.NewStyle().Foreground(roleAccent).Render(marker)
		}
		if !r.available || !r.allowed {
			line = sDim.Render(stripStyles(line))
		}
		out = append(out, clip(line, w-4))
		if !r.available || !r.allowed {
			reason := "not available on an online machine"
			if r.available {
				reason = "not a worker provider"
			}
			out = append(out, sDim.Render("  ("+reason+")"))
		}
	}
	return panel(title, out, w, roleAccent)
}
