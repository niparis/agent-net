package tui

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

type quotaRow struct {
	provider     protocol.QuotaProvider
	at           int64
	machine      string
	readers      int
	disagree     bool
	machineNotes []string
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
		if m.tab == quotasTab && m.uiFilter != "" && !strings.Contains(strings.ToLower(r.provider.Provider+" "+r.provider.Plan+" "+r.machine), strings.ToLower(m.uiFilter)) {
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

// quotaDisplayRows keeps one row per provider while preserving each machine's
// reading in machineNotes. Quota selection and forecasting continue to use
// quotaRows, which retains provider and plan detail.
func (m Model) quotaDisplayRows() []quotaRow {
	if !m.usage.have {
		return nil
	}
	type machineStatus struct {
		name    string
		local   bool
		live    bool
		details []string
	}
	type providerReading struct {
		row      quotaRow
		set      bool
		live     bool
		chosenAt int64
		statuses map[string]machineStatus
	}
	nodes, ids := m.quotaNodes()
	readings := map[string]*providerReading{}
	var reporting []machineStatus
	for _, id := range ids {
		n := nodes[id]
		q := n.Usage.Quotas
		if q == nil {
			continue
		}
		machine := firstNonEmpty(n.Hostname, id)
		reporting = append(reporting, machineStatus{name: machine, local: id == m.usage.raw.NodeID})
		for _, p := range q.Providers {
			key := strings.ToLower(strings.TrimSpace(p.Provider))
			if key == "" {
				continue
			}
			r := readings[key]
			if r == nil {
				r = &providerReading{statuses: map[string]machineStatus{}}
				readings[key] = r
			}
			live := len(p.Windows) > 0
			previous, exists := r.statuses[machine]
			if !exists {
				previous = machineStatus{name: machine, local: id == m.usage.raw.NodeID}
			}
			if live {
				previous.live = true
			}
			if !live || p.Error != "" || p.NeedsAttention || p.Stale {
				for _, detail := range quotaProviderMachineStatuses(p) {
					if !slices.Contains(previous.details, detail) {
						previous.details = append(previous.details, detail)
					}
				}
			}
			r.statuses[machine] = previous
			if !r.set || live && !r.live || live == r.live && q.GeneratedAt > r.chosenAt {
				r.row = quotaRow{provider: p, at: q.GeneratedAt, machine: machine}
				r.set = true
				r.live = live
				r.chosenAt = q.GeneratedAt
			}
			if q.GeneratedAt > r.row.at {
				r.row.at = q.GeneratedAt
			}
		}
	}
	var out []quotaRow
	for _, r := range readings {
		for _, machine := range reporting {
			if _, ok := r.statuses[machine.name]; !ok {
				r.statuses[machine.name] = machineStatus{name: machine.name, local: machine.local, details: []string{"not reported"}}
			}
		}
		var live, other []machineStatus
		for _, status := range r.statuses {
			if status.live {
				live = append(live, status)
			} else {
				other = append(other, status)
			}
		}
		machineLess := func(a, b machineStatus) bool {
			if a.local != b.local {
				return a.local
			}
			return a.name < b.name
		}
		sort.Slice(live, func(i, j int) bool { return machineLess(live[i], live[j]) })
		sort.Slice(other, func(i, j int) bool { return machineLess(other[i], other[j]) })
		for _, status := range append(live, other...) {
			if status.live {
				r.row.machineNotes = append(r.row.machineNotes, "live on "+sanitize(status.name))
			}
			for _, detail := range status.details {
				r.row.machineNotes = append(r.row.machineNotes, sanitize(status.name)+": "+sanitize(detail))
			}
		}
		if m.tab == quotasTab && m.uiFilter != "" && !strings.Contains(strings.ToLower(r.row.provider.Provider+" "+r.row.provider.Plan+" "+strings.Join(r.row.machineNotes, " ")), strings.ToLower(m.uiFilter)) {
			continue
		}
		out = append(out, r.row)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].provider.Provider) < strings.ToLower(out[j].provider.Provider)
	})
	return out
}

func quotaProviderMachineStatuses(p protocol.QuotaProvider) []string {
	var statuses []string
	if p.Error != "" {
		statuses = append(statuses, quotaErrorLabel(p.Error))
	}
	if p.NeedsAttention {
		statuses = append(statuses, "needs you once: run agent-net setup --quota")
	}
	if p.Stale {
		statuses = append(statuses, "stale reading")
	}
	if len(statuses) == 0 {
		statuses = append(statuses, "no live windows")
	}
	return statuses
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

func (m Model) quotaUsableProviders() map[string]bool {
	relevant := m.quotaRelevantProviders()
	for _, name := range []string{"claude", "codex", "opencode-go", "agy"} {
		relevant[name] = true
	}
	for _, n := range m.snap.Nodes {
		for _, h := range n.Harnesses {
			if h.Name != "opencode" && h.Name != "pi" {
				continue
			}
			for _, model := range h.Models {
				if name := protocol.QuotaProviderForModel(model); name != "" {
					relevant[name] = true
				}
			}
		}
	}
	return relevant
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
	used, done, failedRuns, infraRuns             int
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
					row.track.Runs += track.Runs
					row.track.Complete += track.Complete
					row.track.Partial += track.Partial
					row.track.Failed += track.Failed
					row.track.Continue += track.Continue
					row.track.Fix += track.Fix
					row.used += track.Runs
					row.done += track.Complete
					row.failedRuns += track.Failed + track.TimedOut
					row.infraRuns += track.Infra
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
	// Strongest first, by score; unscored models last; then by name.
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.scored != b.scored {
			return a.scored
		}
		if a.scored && a.score.Score != b.score.Score {
			return a.score.Score > b.score.Score
		}
		return a.name < b.name
	})
	return rows
}

// workerForecastLevels are the preview columns of the Workers panel.
var workerForecastLevels = []string{"trivial", "simple", "normal", "hard", "very_complex"}

// localPickerPreview is the gateway's preview of the pick at the middle D of
// each level on this machine, as a Models row name (family and effort). The
// preview already uses the picker's cell rule; the screen never simulates it.
func (m Model) localPickerPreview() map[string]string {
	for _, n := range m.snap.Nodes {
		if n.NodeID != "" && n.NodeID == m.snap.NodeID {
			if len(n.QualityPickModels) > 0 {
				return n.QualityPickModels
			}
			return n.QualityPicks
		}
	}
	return nil
}

// pickerPreviewName is the name the gateway uses for a pick: the model family
// and the effort, for example "terra high".
func pickerPreviewName(model string) string {
	base, effort := protocol.SplitModelLevel(model)
	base = protocol.PickerModelDisplay(base)
	family := ""
	for _, token := range strings.FieldsFunc(strings.ToLower(base), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		switch token {
		case "deepseek", "luna", "sol", "terra", "sonnet", "opus":
			family = token
		}
		if family != "" {
			break
		}
	}
	if family == "" {
		family = base
	}
	if effort != "" {
		return family + " " + effort
	}
	return family
}

// pickerPreviewCell marks the profile the gateway preview chooses for a level.
func pickerPreviewCell(preview map[string]string, level string, r quotaModel) string {
	if len(preview) == 0 {
		return sDim.Render(cell("-", 3))
	}
	if preview[level] == pickerPreviewName(r.model) {
		return sAccent.Render(cell("▸", 3))
	}
	return sDim.Render(cell("·", 3))
}

func workerPreviewHeader() string {
	var cells []string
	for _, letter := range []string{"T", "S", "N", "H", "V"} {
		cells = append(cells, cell(letter, 3))
	}
	return strings.Join(cells, " ")
}

// shortWorkerModel keeps a short model name within w columns without cutting a
// word: it drops the effort suffix or a trailing whole segment first.
func shortWorkerModel(model string, w int) string {
	s := shortModel(model)
	if ansi.StringWidth(s) <= w {
		return s
	}
	if i := strings.LastIndexByte(s, ':'); i > 0 && ansi.StringWidth(s[:i]) <= w {
		return s[:i]
	}
	for i := len(s) - 1; i > 0; i-- {
		if s[i] == '-' || s[i] == '.' || s[i] == '_' || s[i] == ':' || s[i] == '/' {
			if ansi.StringWidth(s[:i])+1 <= w {
				return s[:i] + "…"
			}
		}
	}
	return cell(s, w)
}

func (m Model) quotaModelsView(w int) []string {
	rows, levels := m.quotaModels(), workerForecastLevels
	preview := m.localPickerPreview()
	runningCount := 0
	for _, a := range m.snap.Agents {
		if a.Online && a.Worker != nil {
			runningCount++
		}
	}
	nameWidth, harnessWidth, modelWidth, scoreWidth := 18, 8, 25, 12
	if w < 110 {
		nameWidth, harnessWidth, modelWidth, scoreWidth = 15, 8, 17, 0
	}
	previewWidth := len(workerPreviewHeader())
	usedWidth := max(w-4-(nameWidth+1+harnessWidth+1+modelWidth+1+scoreWidth+1+previewWidth+2), 0)
	showUsed := w >= 110 && usedWidth >= 10
	header := fmt.Sprintf("%-*s %-*s %-*s", nameWidth, "NAME", harnessWidth, "HARNESS", modelWidth, "MODEL")
	if scoreWidth > 0 {
		header += " " + cell("SCORE", scoreWidth)
	}
	header += " " + workerPreviewHeader()
	if showUsed {
		header += "  " + cell("USED", usedWidth)
	}
	var body []string
	body = append(body, sDim.Render(clip(header, w-4)))
	type displayRow struct {
		line string
	}
	displayed := make([]displayRow, 0, len(rows))
	for _, r := range rows {
		var cellsOut []string
		for _, level := range levels {
			cellsOut = append(cellsOut, pickerPreviewCell(preview, level, r))
		}
		score := "-"
		if r.scored && !r.stale {
			score = fmt.Sprintf("%g", r.score.Score)
		}
		if r.score.Effort != "" && !r.stale {
			score += "/" + r.score.Effort
		}
		model := shortWorkerModel(r.model, modelWidth)
		if r.protected {
			r.name = "🔒 " + r.name
		}
		line := fmt.Sprintf("%s %s %s", cell(sanitize(r.name), nameWidth), cell(r.harness, harnessWidth), cell(sanitize(model), modelWidth))
		if scoreWidth > 0 {
			line += " " + cell(score, scoreWidth)
		}
		line += " " + strings.Join(cellsOut, " ")
		if showUsed {
			usage := workerUsage(r)
			if ansi.StringWidth(usage) > usedWidth {
				usage = workerUsageShort(r)
			}
			line += "  " + cell(usage, usedWidth)
		}
		if !r.available || !r.allowed {
			line = sDim.Render(stripStyles(line))
		}
		displayed = append(displayed, displayRow{line: clip(line, w-4)})
	}
	for i, d := range displayed {
		body = append(body, d.line)
		r := rows[i]
		if protocol.HarnessKnown(r.harness) && !protocol.WorkerHarnessSupported(r.harness) {
			// A known harness whose worker adapter does not exist yet
			// (agy, decision 0049): the profile is kept, not called stale.
			body = append(body, sDim.Render("  not startable yet"))
			continue
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
	legend := "T S N H V: the picker's preview at the middle of each level · ▸ = chosen · gateway preview, not simulated"
	if len(preview) > 0 {
		var picks []string
		for _, level := range levels {
			if name := preview[level]; name != "" {
				picks = append(picks, strings.ToUpper(level[:1])+" "+name)
			}
		}
		if len(picks) > 0 {
			for _, line := range wrap(strings.Join(picks, " · "), w-4) {
				body = append(body, sDim.Render(line))
			}
		}
	} else {
		body = append(body, sDim.Render(clip("the gateway sent no picker preview", w-4)))
	}
	body = append(body, sDim.Render(clip(legend, w-4)))
	title := fmt.Sprintf("Workers · %d registered · %d running · scores: Artificial Analysis", len(rows), runningCount)
	if len(m.snap.ProtectedProviders.Providers) > 0 {
		title = fmt.Sprintf("Workers · 🔒 protected · %d registered · %d running · scores: Artificial Analysis", len(rows), runningCount)
	}
	return panel(title, body, w, roleAccent)
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
	if r.infraRuns > 0 {
		usage += fmt.Sprintf(" · %d infra", r.infraRuns)
	}
	return usage
}

// workerUsageShort fits the usage column when the table also shows the picker
// preview columns.
func workerUsageShort(r quotaModel) string {
	if r.used <= 0 {
		return "unused"
	}
	pct := (r.done*100 + r.used/2) / r.used
	usage := fmt.Sprintf("%d·%d%%", r.used, pct)
	if r.failedRuns > 0 {
		usage += fmt.Sprintf("·%d✗", r.failedRuns)
	}
	if r.infraRuns > 0 {
		usage += fmt.Sprintf("·%d infra", r.infraRuns)
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
	rows := m.quotaDisplayRows()
	relevant := m.quotaUsableProviders()
	var configured, liveUnused, unset, unused []quotaRow
	for _, r := range rows {
		provider := strings.ToLower(r.provider.Provider)
		if len(r.provider.Windows) > 0 {
			if relevant[provider] {
				configured = append(configured, r)
			} else {
				liveUnused = append(liveUnused, r)
			}
		} else if quotaNotSetUp(r.provider) {
			unset = append(unset, r)
		} else {
			unused = append(unused, r)
		}
	}
	if m.quotaShowUnset {
		configured = append(configured, liveUnused...)
		configured = append(configured, unset...)
		configured = append(configured, unused...)
	}
	return configured
}

func (m Model) quotaRowDisplayName(r quotaRow) string {
	name := sanitize(r.provider.Provider)
	nodes, ids := m.quotaNodes()
	var machines []protocol.UsageNode
	for _, id := range ids {
		machines = append(machines, nodes[id])
	}
	machineBadge := ""
	var liveMachine protocol.UsageNode
	if m.width < 110 {
		live := 0
		for _, node := range machines {
			if node.Usage.Quotas != nil && quotaMachineStatus(node.Usage.Quotas, r.provider.Provider) == "live" {
				live++
				liveMachine = node
			}
		}
		if live == 1 && liveMachine.Usage.Quotas != nil {
			machineBadge = sanitize(firstNonEmpty(liveMachine.Hostname, liveMachine.NodeID))
		}
	}
	if machineBadge != "" && liveMachine.Usage.Quotas != nil && quotaMachineStatus(liveMachine.Usage.Quotas, r.provider.Provider) == "live" {
		name += " (" + machineBadge + ")"
	}
	return name
}
