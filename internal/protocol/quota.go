package protocol

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/providererr"
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
	// Burn is what the gateway measured of the workers on each provider, for
	// one start (decision 0144). It is not part of a reading and never travels.
	Burn *QuotaBurnSet `json:"-"`
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
	return q.ExhaustedWindowFor(provider, "")
}

// ExhaustedWindowFor looks only at the windows that limit model (see
// QuotaWindowServes); an empty model means every window.
func (q *Quotas) ExhaustedWindowFor(provider, model string) (QuotaWindow, bool) {
	p, ok := q.Provider(provider)
	if !ok || !p.usable() {
		return QuotaWindow{}, false
	}
	for _, w := range p.Windows {
		if !QuotaWindowServes(w, model) {
			continue
		}
		if w.Exhausted || w.PercentUsed >= 100 {
			return w, true
		}
	}
	return QuotaWindow{}, false
}

// HighWindow returns the most used window when it is above QuotaHighPercent.
func (q *Quotas) HighWindow(provider string) (QuotaWindow, bool) {
	return q.HighWindowFor(provider, "")
}

// HighWindowFor is HighWindow over the windows that limit model.
func (q *Quotas) HighWindowFor(provider, model string) (QuotaWindow, bool) {
	p, ok := q.Provider(provider)
	if !ok || !p.usable() {
		return QuotaWindow{}, false
	}
	w, ok := p.worstWindowFor(model)
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

func (p QuotaProvider) worstWindow() (QuotaWindow, bool) { return p.worstWindowFor("") }

func (p QuotaProvider) worstWindowFor(model string) (QuotaWindow, bool) {
	var best QuotaWindow
	found := false
	for _, w := range p.Windows {
		if !QuotaWindowServes(w, model) {
			continue
		}
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
	case "alibaba", "qwen", "alibaba-token-plan":
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
	case "agy":
		return "agy"
	}
	return ""
}

// quotaWindowFamilies are the model families a window id can name, most
// specific first. agy reads one provider with windows per family: "gemini_argon_*"
// for Argon, "gemini_*" for every Gemini model, "claude_gpt_*" for Claude and GPT
// models. A window id with none of these prefixes limits every model.
var quotaWindowFamilies = []string{"gemini_argon_", "gemini_", "claude_gpt_"}

// quotaWindowFamily returns the family prefix of a window id, or "".
func quotaWindowFamily(id string) string {
	id = strings.ToLower(id)
	for _, f := range quotaWindowFamilies {
		if strings.HasPrefix(id, f) {
			return f
		}
	}
	return ""
}

// QuotaWindowServes tells whether a window limits a model (see
// quotaWindowFamilies).
func QuotaWindowServes(w QuotaWindow, model string) bool {
	m := strings.ToLower(model)
	if i := strings.LastIndexByte(m, '/'); i >= 0 {
		m = m[i+1:]
	}
	if m == "" {
		return true
	}
	switch quotaWindowFamily(w.ID) {
	case "gemini_argon_":
		return strings.Contains(m, "argon")
	case "gemini_":
		return strings.HasPrefix(m, "gemini")
	case "claude_gpt_":
		return strings.HasPrefix(m, "claude") || strings.HasPrefix(m, "gpt")
	}
	return true
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

// QuotaWindowName names a window for a sentence: its label, else its id,
// else its kind. quota-axi calls the monthly window "month" (id
// included_usage), so a monthly window is always named "monthly".
func QuotaWindowName(w QuotaWindow) string {
	if strings.EqualFold(w.Kind, "monthly") || strings.EqualFold(w.Kind, "month") ||
		strings.EqualFold(w.Label, "month") || strings.EqualFold(w.Label, "monthly") ||
		strings.EqualFold(w.ID, "month") || strings.EqualFold(w.ID, "monthly") {
		return "monthly"
	}
	for _, name := range []string{w.Label, w.ID, w.Kind} {
		if name != "" {
			return name
		}
	}
	return "quota"
}

// QuotaWorkerReading names the provider and window that the picker's floor
// reads for a worker's model, and how much of it is used. ok is false when
// there is no usable reading or no window serves the model; name is "" when
// the harness and model name no quota provider.
func QuotaWorkerReading(q *Quotas, harness, model string) (name string, percent float64, ok bool) {
	provider := QuotaProviderForStart(harness, model)
	if provider == "" {
		return "", 0, false
	}
	p, found := q.Provider(provider)
	if !found || !p.usable() {
		return provider, 0, false
	}
	w, found := p.worstWindowFor(model)
	if !found {
		return provider, 0, false
	}
	return provider + " " + QuotaWindowName(w), w.PercentUsed, true
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
	if w, ok := quotas.ExhaustedWindowFor(qp, model); ok {
		return quotaExhaustedText(qp, w)
	}
	if w, ok := quotas.HighWindowFor(qp, model); ok {
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
	w, ok := quotas.ExhaustedWindowFor(qp, model)
	if !ok {
		return ""
	}
	return quotaExhaustedText(qp, w)
}

// QuotaHarnessRefusal is the reason a harness with no model named cannot start
// anything: a window of its provider that limits every model is exhausted. A
// window of one model family leaves the other models, so it never refuses.
// It is "" when the harness has no quota provider or may still be used.
func QuotaHarnessRefusal(quotas *Quotas, harness string) string {
	qp := QuotaProviderForHarness(harness)
	if qp == "" {
		return ""
	}
	p, ok := quotas.Provider(qp)
	if !ok || !p.usable() {
		return ""
	}
	for _, w := range p.Windows {
		if quotaWindowFamily(w.ID) == "" && (w.Exhausted || w.PercentUsed >= 100) {
			return quotaExhaustedText(qp, w)
		}
	}
	return ""
}

// quotaExhaustedText says that a provider window has no allowance left. A
// window that a worker run reported (kind quotaAccountKind) says so and until
// when. The account window blocks every model of the provider, so the sentence
// says to use another harness; any other reported window leaves the models it
// does not serve.
func quotaExhaustedText(provider string, w QuotaWindow) string {
	if w.Kind == quotaAccountKind {
		until := time.Unix(w.ResetsAt, 0).UTC().Format("2006-01-02 15:04 UTC")
		if w.ID == QuotaAccountWindowID {
			return fmt.Sprintf("%s quota used up until %s (a worker run reported it); pick another harness", provider, until)
		}
		return fmt.Sprintf("%s %s used up until %s; other %s models may still be used", provider, QuotaWindowName(w), until, provider)
	}
	return fmt.Sprintf("%s is exhausted (%s; %s)", provider, QuotaWindowName(w), QuotaResetText(w))
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

// QuotaAccountWindowID names the window of a block that covers a whole provider:
// a worker's run shows the account is out of quota and no window of the
// provider serves the run's model (see AccountQuotaBlock). It has no family
// prefix, so it limits every model of the provider.
const QuotaAccountWindowID = "account"

// quotaAccountKind marks a window the gateway adds from a worker run's report,
// not from quota-axi.
const quotaAccountKind = "account"

// accountQuotaMarkers are the phrases of an account-level quota refusal. A
// rate limit or an overload is short and does not block the account.
var accountQuotaMarkers = []string{"quota reached", "quota exceeded", "quota exhausted", "out of quota"}

// AccountQuotaBlock reads a worker's failure text at the current time. See
// AccountQuotaBlockAt.
func AccountQuotaBlock(harness, failure string) (string, time.Duration, bool) {
	return AccountQuotaBlockAt(harness, failure, time.Now())
}

// AccountQuotaBlockAt reads a worker's failure text. When the run failed because
// the provider account is out of quota, and the text says when it resets, it
// returns the quota provider and how long to wait from now. Two harnesses are
// read this way, because quota-axi does not see their account limit in time:
// agy ("Individual quota reached ... Resets in 134h28m52s", read per model
// family by quota-axi) and Codex ("You've hit your usage limit ... try again
// at 3:39 AM"). A failure with no reset time blocks nothing.
func AccountQuotaBlockAt(harness, failure string, now time.Time) (string, time.Duration, bool) {
	switch strings.ToLower(strings.TrimSpace(harness)) {
	case "agy":
		return agyQuotaBlock(failure)
	case "codex":
		return codexQuotaBlock(failure, now)
	}
	return "", 0, false
}

func agyQuotaBlock(failure string) (string, time.Duration, bool) {
	text := strings.ToLower(failure)
	found := false
	for _, m := range accountQuotaMarkers {
		if strings.Contains(text, m) {
			found = true
			break
		}
	}
	if !found {
		return "", 0, false
	}
	i := strings.Index(text, "resets in ")
	if i < 0 {
		return "", 0, false
	}
	wait, ok := parseResetDelay(text[i+len("resets in "):])
	if !ok {
		return "", 0, false
	}
	return "agy", wait, true
}

// codexQuotaBlock reads Codex's usage limit. It must name its reset time; a
// short rate limit ("try again in 30s") is not an account block.
func codexQuotaBlock(failure string, now time.Time) (string, time.Duration, bool) {
	if !strings.Contains(strings.ToLower(failure), "usage limit") {
		return "", 0, false
	}
	l, ok := providererr.Limit(failure, now)
	if !ok || !l.Known {
		return "", 0, false
	}
	return "codex", l.Until.Sub(now), true
}

// parseResetDelay reads a delay such as "160h24m5s" or "2d 3h" from the start
// of s. It stops at the first word that is not a number with a unit.
func parseResetDelay(s string) (time.Duration, bool) {
	units := map[byte]time.Duration{'d': 24 * time.Hour, 'h': time.Hour, 'm': time.Minute, 's': time.Second}
	var total time.Duration
	parts := 0
	for {
		s = strings.TrimLeft(s, " ")
		n := 0
		for n < len(s) && s[n] >= '0' && s[n] <= '9' {
			n++
		}
		if n == 0 || n > 6 || n == len(s) {
			break
		}
		unit, ok := units[s[n]]
		if !ok {
			break
		}
		var v int64
		for _, r := range s[:n] {
			v = v*10 + int64(r-'0')
		}
		total += time.Duration(v) * unit
		parts++
		s = s[n+1:]
	}
	return total, parts > 0 && total > 0
}

// QuotaBlock is an out-of-quota block that a worker run reported on one
// machine. WindowID is the window it blocks, so it refuses only the models that
// window serves. An empty WindowID blocks every model of the provider.
type QuotaBlock struct {
	Provider    string
	WindowID    string
	WindowLabel string
	// Until is the reset the provider named. The block ends then.
	Until time.Time
	// Recorded is when the gateway recorded the run's report.
	Recorded time.Time
}

// clearedBy tells whether a fresher usable reading shows the block's window
// with allowance left. The measured reading then wins over the run's report. A
// provider block is never cleared, because quota-axi does not see the account
// limit.
func (b QuotaBlock) clearedBy(q *Quotas) bool {
	if b.WindowID == "" || b.Recorded.IsZero() || q == nil || q.GeneratedAt <= b.Recorded.Unix() {
		return false
	}
	p, ok := q.Provider(b.Provider)
	if !ok || !p.usable() {
		return false
	}
	for _, w := range p.Windows {
		if w.ID == b.WindowID {
			return !w.Exhausted && w.PercentUsed < 100
		}
	}
	return false
}

// window is the exhausted window that stands for the block in a reading.
func (b QuotaBlock) window() QuotaWindow {
	w := QuotaWindow{ID: b.WindowID, Label: b.WindowLabel, Kind: quotaAccountKind, PercentUsed: 100,
		ResetsAt: b.Until.Unix(), Exhausted: true}
	if b.WindowID == "" {
		w.ID, w.Label = QuotaAccountWindowID, "account quota"
	}
	return w
}

// WithQuotaBlocks returns the reading q with the blocks still in force at now.
// Each block adds an exhausted window to its provider, so it refuses the models
// that window serves. A window block that a fresher usable reading clears is
// left out. It does not change q. A blocked provider whose reading is stale or
// failed is replaced by the block, so the block still refuses starts.
func WithQuotaBlocks(q *Quotas, blocks []QuotaBlock, now time.Time) *Quotas {
	var active []QuotaBlock
	for _, b := range blocks {
		if b.Until.After(now) && !b.clearedBy(q) {
			active = append(active, b)
		}
	}
	if len(active) == 0 {
		return q
	}
	sort.SliceStable(active, func(i, j int) bool {
		if active[i].Provider != active[j].Provider {
			return active[i].Provider < active[j].Provider
		}
		return active[i].WindowID < active[j].WindowID
	})
	out := &Quotas{}
	if q != nil {
		*out = *q
		out.Providers = append(make([]QuotaProvider, 0, len(q.Providers)+len(active)), q.Providers...)
	}
	for _, b := range active {
		w := b.window()
		i := -1
		for j, p := range out.Providers {
			if p.Provider == b.Provider {
				i = j
				break
			}
		}
		switch {
		case i < 0:
			out.Providers = append(out.Providers, QuotaProvider{Provider: b.Provider, Windows: []QuotaWindow{w}})
		case !out.Providers[i].usable():
			out.Providers[i] = QuotaProvider{Provider: b.Provider, Plan: out.Providers[i].Plan, Windows: []QuotaWindow{w}}
		default:
			p := out.Providers[i]
			p.Windows = append([]QuotaWindow{w}, p.Windows...)
			out.Providers[i] = p
		}
	}
	return out
}

// QuotaBlockWindowFor names the window that a worker run's account quota
// failure blocks: the window of the provider's reading that serves the run's
// model. When several serve it, the most specific family wins (gemini_argon_*
// over gemini_*), then the window whose reset is nearest until, the reset the
// run named. It returns false when the model is empty or no window serves it;
// the block then covers the whole provider. Only the window ids are read, so a
// stale reading still names the window.
func QuotaBlockWindowFor(q *Quotas, provider, model string, until time.Time) (QuotaWindow, bool) {
	p, ok := q.Provider(provider)
	if !ok || strings.TrimSpace(model) == "" {
		return QuotaWindow{}, false
	}
	var best QuotaWindow
	found := false
	for _, w := range p.Windows {
		if w.ID == "" || !QuotaWindowServes(w, model) {
			continue
		}
		if !found || quotaWindowNearer(w, best, until) {
			best, found = w, true
		}
	}
	return best, found
}

// quotaWindowNearer tells whether window a is a better block than window b for a
// run that reset at until: a more specific family first, then the nearer reset.
func quotaWindowNearer(a, b QuotaWindow, until time.Time) bool {
	if fa, fb := len(quotaWindowFamily(a.ID)), len(quotaWindowFamily(b.ID)); fa != fb {
		return fa > fb
	}
	return quotaResetGap(a, until) < quotaResetGap(b, until)
}

// quotaResetGap is how far a window's reset is from until, in seconds. A window
// with no reset time is the furthest away.
func quotaResetGap(w QuotaWindow, until time.Time) int64 {
	if w.ResetsAt <= 0 || until.IsZero() {
		return math.MaxInt64
	}
	gap := w.ResetsAt - until.Unix()
	if gap < 0 {
		gap = -gap
	}
	return gap
}
