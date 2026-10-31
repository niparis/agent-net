package protocol

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Quotas are the subscription windows of the owner's model providers, read
// from quota-axi on the machine that holds the logins (decision 0022). The
// daemon reports them with its usage report; the gateway keeps the newest
// reading and uses it to choose a model and admit a start.

// QuotaHighPercent is the share of a window above which a provider is skipped
// for a loose model name. An exact model id still works, with a warning.
const QuotaHighPercent = 95.0

// Pace values of one window, as quota-axi names them.
const (
	QuotaPaceAhead   = "ahead"
	QuotaPaceOnPace  = "on_pace"
	QuotaPaceBehind  = "behind"
	QuotaPaceUnknown = "unknown"
)

// QuotaWindow is one window of a provider, for example a weekly window.
type QuotaWindow struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	Kind  string `json:"kind,omitempty"`
	// PercentUsed is the share of the window that is spent, 0 to 100.
	PercentUsed float64 `json:"percent_used"`
	// ResetsAt is when the window resets, in Unix seconds. 0 when unknown.
	ResetsAt int64 `json:"resets_at,omitempty"`
	// Pace is ahead, on_pace, behind or unknown.
	Pace string `json:"pace,omitempty"`
	// Exhausted is set when the window has no allowance left now, including a
	// quota-axi runway of exhausted_now.
	Exhausted bool `json:"exhausted,omitempty"`
}

// QuotaProvider is one provider's reading.
type QuotaProvider struct {
	Provider string `json:"provider"`
	Plan     string `json:"plan,omitempty"`
	// Stale is true when quota-axi served a cached reading. A stale reading is
	// shown but never used to choose or refuse a model.
	Stale          bool          `json:"stale,omitempty"`
	Error          string        `json:"error,omitempty"`
	NeedsAttention bool          `json:"needs_attention,omitempty"`
	Windows        []QuotaWindow `json:"windows"`
}

// QuotaCopy identifies the executable that produced a reading.
type QuotaCopy struct {
	Version string `json:"version"`
	Dir     string `json:"dir,omitempty"`
	Private bool   `json:"private,omitempty"`
}

// Quotas is one quota-axi reading.
type Quotas struct {
	// GeneratedAt is when quota-axi produced the reading, in Unix seconds.
	GeneratedAt int64           `json:"generated_at,omitempty"`
	Copy        *QuotaCopy      `json:"copy,omitempty"`
	Providers   []QuotaProvider `json:"providers"`
}

// Provider returns the reading of one quota provider.
func (q *Quotas) Provider(name string) (QuotaProvider, bool) {
	if q == nil {
		return QuotaProvider{}, false
	}
	for _, p := range q.Providers {
		if p.Provider == name {
			return p, true
		}
	}
	return QuotaProvider{}, false
}

// usable tells whether a provider's reading may decide a model choice.
func (p QuotaProvider) usable() bool {
	return !p.Stale && p.Error == ""
}

// ExhaustedWindow returns the window that has no allowance left. A stale
// reading is ignored.
func (q *Quotas) ExhaustedWindow(provider string) (QuotaWindow, bool) {
	p, ok := q.Provider(provider)
	if !ok || !p.usable() {
		return QuotaWindow{}, false
	}
	for _, w := range p.Windows {
		if w.Exhausted || w.PercentUsed >= 100 {
			return w, true
		}
	}
	return QuotaWindow{}, false
}

// HighWindow returns the most used window when it is above QuotaHighPercent.
func (q *Quotas) HighWindow(provider string) (QuotaWindow, bool) {
	p, ok := q.Provider(provider)
	if !ok || !p.usable() {
		return QuotaWindow{}, false
	}
	w, ok := p.worstWindow()
	if !ok || w.PercentUsed <= QuotaHighPercent {
		return QuotaWindow{}, false
	}
	return w, true
}

// Ahead tells whether a provider is ahead of its reset clock in any window.
// A provider on pace or behind it does not rank lower.
func (q *Quotas) Ahead(provider string) bool {
	p, ok := q.Provider(provider)
	if !ok || !p.usable() {
		return false
	}
	for _, w := range p.Windows {
		if w.Pace == QuotaPaceAhead {
			return true
		}
	}
	return false
}

func (p QuotaProvider) worstWindow() (QuotaWindow, bool) {
	var best QuotaWindow
	found := false
	for _, w := range p.Windows {
		if !found || w.PercentUsed > best.PercentUsed {
			best, found = w, true
		}
	}
	return best, found
}

// QuotaProviderForModelProvider maps the provider part of a model id to the
// quota-axi provider that serves it. The table is documented in
// docs/reference.md; keep the two in step.
func QuotaProviderForModelProvider(modelProvider string) string {
	switch strings.ToLower(strings.TrimSpace(modelProvider)) {
	case "anthropic", "claude":
		return "claude"
	case "openai", "codex", "openai-codex":
		return "codex"
	case "opencode-go":
		return "opencode-go"
	case "cursor":
		return "cursor"
	case "github-copilot", "copilot":
		return "copilot"
	case "xai", "grok":
		return "grok"
	case "kimi", "moonshot":
		return "kimi"
	case "zai", "z-ai", "zhipu":
		return "zai"
	case "google", "gemini", "agy", "antigravity":
		return "agy"
	case "alibaba", "qwen":
		return "alibaba"
	case "minimax":
		return "minimax"
	case "xiaomi", "mimo":
		return "mimo"
	case "deepseek":
		return "deepseek"
	case "openrouter":
		return "openrouter"
	case "commandcode", "command-code":
		return "commandcode"
	}
	return ""
}

// QuotaProviderForHarness maps a harness to the quota provider its default
// model draws on. claude and codex name no provider in their model ids.
func QuotaProviderForHarness(harness string) string {
	switch strings.ToLower(harness) {
	case "claude":
		return "claude"
	case "codex":
		return "codex"
	}
	return ""
}

// QuotaProviderForModel maps a full model id to its quota provider. It returns
// "" when the model names no provider, or names one without a quota.
func QuotaProviderForModel(model string) string {
	if i := strings.Index(model, "/"); i > 0 {
		return QuotaProviderForModelProvider(model[:i])
	}
	return ""
}

// QuotaProviderForStart maps a start's harness and model to a quota provider.
func QuotaProviderForStart(harness, model string) string {
	if p := QuotaProviderForModel(model); p != "" {
		return p
	}
	return QuotaProviderForHarness(harness)
}

// QuotaWindowName names a window for a sentence: its label, else its id.
func QuotaWindowName(w QuotaWindow) string {
	if w.Label != "" {
		return w.Label
	}
	if w.ID != "" {
		return w.ID
	}
	return "quota"
}

// QuotaResetText writes a reset time for a sentence.
func QuotaResetText(w QuotaWindow) string {
	if w.ResetsAt <= 0 {
		return "its reset time is unknown"
	}
	return "it resets at " + time.Unix(w.ResetsAt, 0).UTC().Format("2006-01-02 15:04 UTC")
}

// QuotaWarning returns the note for an exact model id whose provider is
// exhausted or above QuotaHighPercent. It is "" when the model may be used
// without a warning.
func QuotaWarning(quotas *Quotas, provider, model string) string {
	qp := QuotaProviderForStart(provider, model)
	if qp == "" {
		return ""
	}
	if w, ok := quotas.ExhaustedWindow(qp); ok {
		return fmt.Sprintf("%s is exhausted (%s; %s)", qp, QuotaWindowName(w), QuotaResetText(w))
	}
	if w, ok := quotas.HighWindow(qp); ok {
		return fmt.Sprintf("%s is at %.0f%% of its %s window", qp, w.PercentUsed, QuotaWindowName(w))
	}
	return ""
}

// QuotaRefusal is the admission reason when a start's provider is exhausted.
// It is "" when the start may go ahead.
func QuotaRefusal(quotas *Quotas, harness, model string) string {
	qp := QuotaProviderForStart(harness, model)
	if qp == "" {
		return ""
	}
	w, ok := quotas.ExhaustedWindow(qp)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%s is exhausted (%s; %s)", qp, QuotaWindowName(w), QuotaResetText(w))
}

// quotaExhaustedForDisplay tells whether a provider has no allowance left,
// including a stale reading: the line reports what was last seen.
func quotaExhaustedForDisplay(p QuotaProvider) bool {
	for _, w := range p.Windows {
		if w.Exhausted || w.PercentUsed >= 100 {
			return true
		}
	}
	return false
}

// quotaSuffix writes a window kind compactly for the header line.
func quotaSuffix(kind string) string {
	switch strings.ToLower(kind) {
	case "weekly", "week":
		return "w"
	case "session", "five_hour", "5h":
		return "s"
	case "monthly", "month":
		return "m"
	case "daily", "day":
		return "d"
	case "credits":
		return "c"
	}
	if kind == "" {
		return "w"
	}
	return kind[:1]
}

// QuotaSummary writes one compact line for every provider that has a reading,
// for example "claude 59%w · codex 90%w · cursor exhausted". Providers with a
// window above QuotaHighPercent are still shown: the line reports facts.
func QuotaSummary(q *Quotas) string {
	if q == nil || len(q.Providers) == 0 {
		return ""
	}
	order := []string{"claude", "codex", "opencode-go", "cursor", "copilot", "grok", "kimi", "zai", "agy", "alibaba", "minimax", "mimo", "deepseek", "openrouter"}
	rank := map[string]int{}
	for i, p := range order {
		rank[p] = i
	}
	providers := append([]QuotaProvider(nil), q.Providers...)
	sort.SliceStable(providers, func(i, j int) bool {
		ri, iok := rank[providers[i].Provider]
		rj, jok := rank[providers[j].Provider]
		switch {
		case iok && jok:
			return ri < rj
		case iok:
			return true
		case jok:
			return false
		}
		return providers[i].Provider < providers[j].Provider
	})
	var parts []string
	for _, p := range providers {
		w, ok := p.worstWindow()
		if !ok {
			continue
		}
		if quotaExhaustedForDisplay(p) {
			parts = append(parts, p.Provider+" exhausted")
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %.0f%%%s", p.Provider, w.PercentUsed, quotaSuffix(w.Kind)))
	}
	return strings.Join(parts, " · ")
}
