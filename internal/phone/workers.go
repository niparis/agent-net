package phone

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

// The Workers screen reads the same data as the TUI Quotas tab: the quota
// report, the registered profiles with their rotation, the scores, and the
// worker log. It is read only: protect, start and stop are TUI actions.

type quotaCellView struct {
	Provider, Plan, Window, Pace, Reset string
	Left                                int
	Warn, Protected                     bool
}

type pickRowView struct {
	Model, Harness, Score, Usage string
	Levels                       [5]bool
	scored                       bool
	value                        float64
}

type runningRowView struct {
	Name, Model, Task, Run, Tokens, Cost string
}

type workersView struct {
	Quotas       []quotaCellView
	QuotaNote    string
	QuotaAge     string
	Picks        []pickRowView
	Running      []runningRowView
	RunningLabel string
}

// workerLevels are the picker's five difficulty columns, left to right.
var workerLevels = []string{"trivial", "simple", "normal", "hard", "very_complex"}

// workerLevelLetters are the table headings of those levels.
var workerLevelLetters = []string{"T", "S", "N", "H", "V"}

func (a *app) workers(ctx context.Context, snap operator.Snapshot) *workersView {
	v := &workersView{}
	v.Quotas, v.QuotaAge, v.QuotaNote = a.subscriptions(ctx, snap)
	v.Picks = workerPicks(snap)
	v.Running = runningWorkers(snap)
	v.RunningLabel = plural(len(v.Running), "worker")
	return v
}

// ---- Subscriptions ----

type quotaReading struct {
	provider protocol.QuotaProvider
	window   protocol.QuotaWindow
	at       int64
}

// subscriptions merges the quota readings of every machine. A newer reading
// of the same provider, plan and window replaces an older one.
func (a *app) subscriptions(ctx context.Context, snap operator.Snapshot) ([]quotaCellView, string, string) {
	raw, err := a.src.Call(ctx, protocol.OpUsage, nil)
	if err != nil {
		return nil, "", "The quota report could not be read: " + firstLine(err.Error())
	}
	var res protocol.UsageResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, "", "The quota report could not be read: " + firstLine(err.Error())
	}
	nodes := map[string]protocol.UsageNode{}
	if res.Gateway != nil {
		for _, n := range res.Gateway.Nodes {
			nodes[n.NodeID] = n
		}
	}
	if res.Local != nil && res.NodeID != "" {
		nodes[res.NodeID] = protocol.UsageNode{NodeID: res.NodeID, Hostname: res.Hostname, Usage: *res.Local}
	}
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	type key struct{ provider, plan, window string }
	readings := map[key]quotaReading{}
	for _, id := range ids {
		n := nodes[id]
		q := n.Usage.Quotas
		if q == nil {
			continue
		}
		for _, p := range q.Providers {
			for _, w := range p.Windows {
				k := key{strings.ToLower(p.Provider), strings.ToLower(p.Plan), strings.ToLower(protocol.QuotaWindowName(w))}
				old, ok := readings[k]
				if !ok || q.GeneratedAt > old.at {
					readings[k] = quotaReading{provider: p, window: w, at: q.GeneratedAt}
				}
			}
		}
	}
	if len(readings) == 0 {
		return nil, "", "No quota readings yet. The TUI Quotas tab says what is missing."
	}
	var out []quotaCellView
	latest := int64(0)
	for _, r := range readings {
		left := 100 - int(math.Round(r.window.PercentUsed))
		left = max(0, min(100, left))
		c := quotaCellView{Provider: r.provider.Provider, Plan: r.provider.Plan, Window: protocol.QuotaWindowName(r.window),
			Pace: quotaPace(r.window.Pace), Reset: quotaResetText(r.window.ResetsAt, time.Now()), Left: left,
			Warn: r.window.Exhausted || left <= 20, Protected: quotaProtected(snap.ProtectedProviders, r.provider.Provider)}
		out = append(out, c)
		if r.at > latest {
			latest = r.at
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		if out[i].Plan != out[j].Plan {
			return out[i].Plan < out[j].Plan
		}
		return out[i].Window < out[j].Window
	})
	age := ""
	if latest > 0 {
		if at := ago(time.Unix(latest, 0), time.Now()); at == "now" {
			age = "read now"
		} else {
			age = "read " + at + " ago"
		}
	}
	return out, age, ""
}

func quotaPace(pace string) string {
	switch pace {
	case "ahead":
		return "ahead"
	case "on_pace", "on pace":
		return "on pace"
	case "behind":
		return "behind"
	}
	return "pace unknown"
}

// quotaResetText names the reset in the shape of the mockup: "resets in 2h10",
// "resets Thu", "resets Jan 2".
func quotaResetText(at int64, now time.Time) string {
	if at <= 0 {
		return "reset unknown"
	}
	d := time.Unix(at, 0).Sub(now)
	switch {
	case d <= 0:
		return "resetting"
	case d < 24*time.Hour:
		return "resets in " + shortRun(int64(d/time.Second))
	case d < 7*24*time.Hour:
		return "resets " + time.Unix(at, 0).Format("Mon")
	}
	return "resets " + time.Unix(at, 0).Format("Jan 2")
}

// quotaProtected says whether the owner keeps a provider for the orchestrators.
func quotaProtected(pp protocol.ProtectedProviders, provider string) bool {
	for _, p := range pp.Providers {
		if strings.EqualFold(p, provider) || strings.EqualFold(protocol.QuotaProviderForModelProvider(p), provider) {
			return true
		}
	}
	return false
}

func shortRun(seconds int64) string {
	if seconds <= 0 {
		return "-"
	}
	d := time.Duration(seconds) * time.Second
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", seconds)
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 48*time.Hour:
		h := int(d / time.Hour)
		m := int((d - time.Duration(h)*time.Hour) / time.Minute)
		if m > 0 {
			return fmt.Sprintf("%dh%02d", h, m)
		}
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
}

// ---- Model picks ----

// pickerPreviewName is the name the gateway uses for a pick: the model family
// and the effort, for example "terra high". It matches the TUI Quotas preview.
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

func profileModel(p protocol.WorkerProfile) string {
	model := p.Model
	if p.Effort != "" && !strings.HasSuffix(model, ":"+p.Effort) {
		model += ":" + p.Effort
	}
	return model
}

func workerPicks(snap operator.Snapshot) []pickRowView {
	preview := map[string]string{}
	for _, n := range snap.Nodes {
		if n.NodeID != "" && n.NodeID == snap.NodeID && len(n.QualityPickModels) > 0 {
			preview = n.QualityPickModels
		}
	}
	var rows []pickRowView
	for _, p := range snap.WorkerProfiles {
		model := profileModel(p)
		row := pickRowView{Model: profileModelLabel(p), Harness: p.Harness, Score: "-"}
		if score, ok := protocol.LookupModelScoreWithDefaultEffort(model, "", snap.Scores); ok {
			row.Score, row.scored, row.value = fmt.Sprintf("%g", score.Score), true, score.Score
		}
		tracked := map[string]bool{model: true, p.Model: true}
		used, done := 0, 0
		display := protocol.PickerModelDisplay(model)
		for _, n := range snap.Nodes {
			for _, tr := range n.Tracks {
				if tr.Harness == p.Harness && (tracked[tr.Model] || strings.EqualFold(protocol.PickerModelDisplay(tr.Model), display)) {
					used += tr.Runs
					done += tr.Complete
				}
			}
		}
		if used > 0 {
			row.Usage = fmt.Sprintf("%d · %d%%", used, (done*100+used/2)/used)
		} else {
			row.Usage = "unused"
		}
		for i, level := range workerLevels {
			if state := snap.WorkerRotation.Levels[level]; state != nil && state.Current[p.Harness+"\x00"+model] > 0 {
				row.Levels[i] = true
			}
			if preview[level] != "" && preview[level] == pickerPreviewName(model) {
				row.Levels[i] = true
			}
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].scored != rows[j].scored {
			return rows[i].scored
		}
		if rows[i].scored && rows[i].value != rows[j].value {
			return rows[i].value > rows[j].value
		}
		return rows[i].Model < rows[j].Model
	})
	return rows
}

// profileModelLabel is the model the table names: the model without its
// provider folder, with the effort after a colon.
func profileModelLabel(p protocol.WorkerProfile) string {
	label := protocol.PickerModelDisplay(p.Model)
	if p.Effort != "" && !strings.HasSuffix(label, ":"+p.Effort) {
		label += ":" + p.Effort
	}
	return label
}

// ---- Running workers ----

func runningWorkers(snap operator.Snapshot) []runningRowView {
	agents := map[string]protocol.AgentView{}
	for _, a := range snap.Agents {
		agents[a.SessionID] = a
	}
	var out []runningRowView
	now := time.Now().Unix()
	for _, e := range snap.WorkerLog {
		if e.Status != "running" {
			continue
		}
		name := firstNonEmpty(e.Title, "worker")
		task := e.Title
		tokens := int64(0)
		if a, ok := agents[e.SessionID]; ok {
			name = firstNonEmpty(sessionName(a), name)
			if a.Worker != nil {
				tokens = a.Worker.Tokens
				task = firstNonEmpty(a.Worker.Task, task)
			}
		}
		run := e.RunSeconds
		if run <= 0 && e.StartedAt > 0 {
			run = max(now-e.StartedAt, 0)
		}
		model := e.Harness
		if e.Model != "" {
			model = strings.TrimSpace(e.Harness + " · " + e.Model)
		}
		out = append(out, runningRowView{Name: name, Model: model, Task: firstNonEmpty(task, e.Project, "-"),
			Run: shortRun(run), Tokens: firstNonEmpty(protocol.CompactTokens(tokens), "-"), Cost: "-"})
	}
	return out
}
