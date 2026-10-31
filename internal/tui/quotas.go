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
	notOn                                         []string
	score                                         protocol.ModelScore
	used, done, failedRuns                        int
	last                                          int64
	failed, allowed, scored, succeeded, available bool
	stale, protected                              bool
	provider                                      string
	track                                         protocol.TrackRecord
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
		row := quotaModel{name: p.Name, harness: p.Harness, model: model, failed: true, stale: p.Available != nil && !*p.Available}
		trackedModels := map[string]bool{model: true}
		missingMachines := map[string]bool{}
		for _, machine := range p.MachineAvailability {
			if machine.Available {
				row.available = true
				resolved := machine.ResolvedModel
				if resolved == "" {
					resolved = p.Model
				}
				if p.Effort != "" {
					resolved += ":" + p.Effort
				}
				trackedModels[resolved] = true
			} else if machine.Machine != "" {
				missingMachines[machine.Machine] = true
			}
		}
		row.provider = protocol.ProfileProvider(p.Harness, p.Model)
		row.protected = m.providerProtected(row.provider)
		found := false
		for _, n := range m.snap.Nodes {
			if !n.Online {
				continue
			}
			machine := firstNonEmpty(n.Hostname, n.NodeID)
			for _, h := range n.Harnesses {
				if h.Name != p.Harness {
					continue
				}
				resolved, _ := protocol.ResolveWorkerProfileModel(p.Model, h.Models)
				if resolved == "" {
					if machine != "" {
						missingMachines[machine] = true
					}
					continue
				}
				delete(missingMachines, machine)
				trackedModel := resolved
				if p.Effort != "" {
					trackedModel += ":" + p.Effort
				}
				trackedModels[trackedModel] = true
				quota := n.Quotas
				if usage, ok := quotaNodes[n.NodeID]; ok && usage.Usage.Quotas != nil {
					quota = usage.Usage.Quotas
				}
				allowed := protocol.WorkerProviderAllowed(h.Name, resolved, m.snap.WorkerProviders, quota)
				row.available = true
				row.allowed = row.allowed || allowed
				if !found {
					row.defaultEffort = h.ModelDefaultEfforts[resolved]
					found = true
				}
				if allowed {
					row.failed = row.failed && n.RecentModelFailures[h.Name][trackedModel]
					row.succeeded = row.succeeded || n.RecentModelSuccesses[h.Name][trackedModel]
				}
			}
		}
		for _, n := range m.snap.Nodes {
			for _, track := range n.Tracks {
				if track.Harness == p.Harness && trackedModels[track.Model] {
					row.track.Complete += track.Complete
					row.track.Partial += track.Partial
					row.track.Failed += track.Failed
					row.used += track.Runs
					row.done += track.Complete
					row.failedRuns += track.Failed + track.TimedOut
				}
			}
		}
		for machine := range missingMachines {
			row.notOn = append(row.notOn, machine)
		}
		sort.Strings(row.notOn)
		row.score, row.scored = protocol.LookupModelScoreWithDefaultEffort(model, row.defaultEffort, m.snap.Scores)
		if row.stale {
			row.available = false
		}
		for _, a := range m.snap.Agents {
			if a.Harness == p.Harness && trackedModels[a.Model] {
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
	levels := []string{"trivial", "simple", "normal", "hard", "very_complex"}
	levelText := []string{"trivial", "simple", "normal", "hard", "very complex"}
	candidates := make([]protocol.WorkerModelCandidate, 0, len(rows))
	for _, r := range rows {
		if r.available && r.allowed && r.scored {
			candidates = append(candidates, protocol.WorkerModelCandidate{Harness: r.harness, Model: r.model, DefaultEffort: r.defaultEffort, Failed: r.failed, Succeeded: r.succeeded, Track: r.track})
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
	runningCount := 0
	for _, a := range m.snap.Agents {
		if a.Online && a.Worker != nil {
			runningCount++
		}
	}
	picks := make([]protocol.WorkerModelChoice, len(levels))
	for i, level := range levels {
		var normal, protected []protocol.WorkerModelCandidate
		for _, c := range candidates {
			if len(m.snap.ProtectedProviders.Providers) > 0 {
				score, ok := protocol.LookupModelScoreWithDefaultEffort(c.Model, c.DefaultEffort, m.snap.Scores)
				if !ok {
					continue
				}
				fit, _ := protocol.DifficultyFit(score.Score, level, m.snap.DifficultyMinimums)
				if fit != "good" && fit != "excellent" {
					continue
				}
			}
			if m.providerProtected(protocol.ProfileProvider(c.Harness, c.Model)) {
				protected = append(protected, c)
			} else {
				normal = append(normal, c)
			}
		}
		var err error
		picks[i], err = protocol.ChooseWorkerModel(normal, level, m.snap.Scores, m.snap.DifficultyMinimums, q, running, 0)
		if err != nil {
			picks[i], _ = protocol.ChooseWorkerModel(protected, level, m.snap.Scores, m.snap.DifficultyMinimums, q, running, 0)
		}
	}
	nameWidth, harnessWidth, modelWidth, scoreWidth := 18, 8, 25, 12
	showUsed := w >= 110
	usedWidth := max(w-4-(nameWidth+1+harnessWidth+1+modelWidth+1+scoreWidth+1+9+2), 0)
	header := fmt.Sprintf("%-*s %-*s %-*s %-*s T S N H V", nameWidth, "NAME", harnessWidth, "HARNESS", modelWidth, "MODEL", scoreWidth, "SCORE")
	if showUsed {
		header += "  USED"
	}
	var body []string
	body = append(body, sDim.Render(header))
	type displayRow struct {
		line      string
		picks     []int
		fitLetter []string
	}
	displayed := make([]displayRow, 0, len(rows))
	selectedRow, selectedCount := -1, 0
	for rowIndex, r := range rows {
		var fits, fitLetters []string
		var chosen []int
		for i, level := range levels {
			letter := "-"
			color := roleFaint
			if r.scored && !r.stale {
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
			if picks[i].Model == r.model && picks[i].Harness == r.harness && picks[i].Model != "" {
				style = sSel.Bold(true)
				chosen = append(chosen, i)
			}
			fits = append(fits, style.Render(letter))
			fitLetters = append(fitLetters, letter)
		}
		score := "-"
		if r.scored && !r.stale {
			score = fmt.Sprintf("%g", r.score.Score)
		}
		if r.score.Effort != "" && !r.stale {
			score += "/" + r.score.Effort
		}
		model := workerDisplayModel(r.harness, r.model)
		if r.protected {
			r.name = "🔒 " + r.name
		}
		line := fmt.Sprintf("%s %s %s %s %s", cell(sanitize(r.name), nameWidth), cell(r.harness, harnessWidth), cell(sanitize(model), modelWidth), cell(score, scoreWidth), strings.Join(fits, " "))
		if showUsed {
			line += "  " + cell(workerUsage(r), usedWidth)
		}
		if len(chosen) > selectedCount {
			selectedRow, selectedCount = rowIndex, len(chosen)
		}
		if !r.available || !r.allowed {
			line = sDim.Render(stripStyles(line))
		}
		displayed = append(displayed, displayRow{line: clip(line, w-4), picks: chosen, fitLetter: fitLetters})
	}
	for i, d := range displayed {
		body = append(body, d.line)
		r := rows[i]
		if r.protected {
			body = append(body, sDim.Render(clip("  protected · saved for your orchestrator ("+r.provider+")", w-4)))
		}
		if r.stale {
			body = append(body, sDim.Render("  model gone - ask the CTO to fix"))
		} else {
			if r.available && len(r.notOn) > 0 {
				machines := make([]string, len(r.notOn))
				for i, machine := range r.notOn {
					machines[i] = sanitize(machine)
				}
				body = append(body, sDim.Render(clip("  not on "+strings.Join(machines, ", "), w-4)))
			}
			if !r.available || !r.allowed {
				reason := "not available on an online machine"
				if r.available {
					reason = "not a worker provider"
				}
				body = append(body, sDim.Render("  ("+reason+")"))
			}
		}
	}
	key := "T trivial S simple N normal H hard V very complex · E strong G ok P weak - too weak · lit = picked now"
	if w < 110 {
		key = "T/S/N/H/V=tri/sim/nor/hard/v.complex·E/G/P/-=strong/ok/weak/too weak·lit=now"
	}
	body = append(body, sDim.Render(clip(key, w-4)))
	title := fmt.Sprintf("Workers · %d registered · %d running · scores: Artificial Analysis", len(rows), runningCount)
	if len(m.snap.ProtectedProviders.Providers) > 0 {
		title = fmt.Sprintf("Workers · 🔒 protected · %d registered · %d running · scores: Artificial Analysis", len(rows), runningCount)
	}
	out := panel(title, body, w, roleAccent)
	if selectedRow >= 0 {
		r := rows[selectedRow]
		var selected, strong, good, poor []string
		for _, i := range displayed[selectedRow].picks {
			selected = append(selected, levelText[i])
		}
		for i, fit := range displayed[selectedRow].fitLetter {
			switch fit {
			case "E":
				strong = append(strong, levelText[i])
			case "G":
				good = append(good, levelText[i])
			case "P":
				poor = append(poor, levelText[i])
			}
		}
		label := sanitize(r.name)
		if !strings.Contains(strings.ToLower(label), strings.ToLower(r.harness)) {
			label += " " + sanitize(r.harness)
		}
		detail := fmt.Sprintf("%s: picked for %s.", label, naturalList(selected))
		if len(strong) > 0 {
			detail += " Strong at " + naturalList(strong) + "."
		} else if len(good) > 0 {
			detail += " OK at " + naturalList(good) + "."
		} else if len(poor) > 0 {
			detail += " Weak at " + naturalList(poor) + "."
		}
		out = append(out, sDim.Render(clip(detail, w)))
	}
	return out
}

func workerDisplayModel(harness, model string) string {
	i := strings.IndexByte(model, '/')
	if i <= 0 || i == len(model)-1 {
		return model
	}
	provider := strings.ToLower(model[:i])
	if provider == "opencode-go" || provider == "openai-codex" || provider == "openai" || provider == strings.ToLower(harness) {
		return model[i+1:]
	}
	return model
}

func workerUsage(r quotaModel) string {
	if r.used <= 0 {
		return "unused"
	}
	pct := (r.done*100 + r.used/2) / r.used
	usage := fmt.Sprintf("%d · %d%% done", r.used, pct)
	if r.failedRuns > 0 {
		usage += fmt.Sprintf(" · %d failed", r.failedRuns)
	}
	return usage
}

func naturalList(items []string) string {
	switch len(items) {
	case 0:
		return "none"
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
	}
}

func (m Model) providerProtected(provider string) bool {
	for _, p := range m.snap.ProtectedProviders.Providers {
		if strings.EqualFold(p, provider) {
			return true
		}
	}
	return false
}

func (m Model) quotaProviderProtected(provider string) bool {
	for _, p := range m.snap.ProtectedProviders.Providers {
		if strings.EqualFold(p, provider) || protocol.QuotaProviderForModelProvider(p) != "" && strings.EqualFold(protocol.QuotaProviderForModelProvider(p), provider) {
			return true
		}
	}
	return false
}

// selectableQuotaRows follows the same ordering and visibility as the panel.
func (m Model) selectableQuotaRows() []quotaRow {
	rows := m.quotaRows()
	relevant := m.quotaRelevantProviders()
	withQuota := map[string]bool{}
	for _, r := range rows {
		if len(r.provider.Windows) > 0 {
			withQuota[strings.ToLower(r.provider.Provider)] = true
		}
	}
	var configured, unset, unused []quotaRow
	for _, r := range rows {
		provider := strings.ToLower(r.provider.Provider)
		if quotaNotSetUp(r.provider) && !withQuota[provider] {
			unset = append(unset, r)
		} else if !relevant[provider] {
			unused = append(unused, r)
		} else {
			configured = append(configured, r)
		}
	}
	if m.quotaShowUnset {
		configured = append(configured, unset...)
		configured = append(configured, unused...)
	}
	return configured
}
