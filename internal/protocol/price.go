package protocol

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// RunTokens is what a worker run used, by kind. Cache reads and writes are
// kept apart from input because they are priced differently.
type RunTokens struct {
	In         float64 `json:"in,omitempty"`
	Out        float64 `json:"out,omitempty"`
	Reasoning  float64 `json:"reasoning,omitempty"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
}

// Zero tells whether no token was recorded.
func (t RunTokens) Zero() bool { return t == RunTokens{} }

// Plus adds two token counts.
func (t RunTokens) Plus(o RunTokens) RunTokens {
	return RunTokens{t.In + o.In, t.Out + o.Out, t.Reasoning + o.Reasoning, t.CacheRead + o.CacheRead, t.CacheWrite + o.CacheWrite}
}

// Max keeps the larger count of each kind. A harness reports cumulative
// figures, and a missed read must not lower them.
func (t RunTokens) Max(o RunTokens) RunTokens {
	return RunTokens{max(t.In, o.In), max(t.Out, o.Out), max(t.Reasoning, o.Reasoning), max(t.CacheRead, o.CacheRead), max(t.CacheWrite, o.CacheWrite)}
}

// Scaled multiplies every kind by f.
func (t RunTokens) Scaled(f float64) RunTokens {
	return RunTokens{t.In * f, t.Out * f, t.Reasoning * f, t.CacheRead * f, t.CacheWrite * f}
}

// ModelPrice is the API price of one model in dollars per token, as OpenRouter
// lists it. A cache price of 0 means the model has none.
type ModelPrice struct {
	ID         string  `json:"id"`
	Prompt     float64 `json:"prompt"`
	Completion float64 `json:"completion"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
}

// Cost is the price of a run at API prices. Input is priced at the prompt
// price, output and reasoning at the completion price. A missing cache price
// falls back to the prompt price.
func (p ModelPrice) Cost(t RunTokens) float64 {
	read, write := p.CacheRead, p.CacheWrite
	if read <= 0 {
		read = p.Prompt
	}
	if write <= 0 {
		write = p.Prompt
	}
	return t.In*p.Prompt + (t.Out+t.Reasoning)*p.Completion + t.CacheRead*read + t.CacheWrite*write
}

// PriceBook is a list of prices indexed so that one of our model ids finds its
// entry.
type PriceBook struct {
	all    []ModelPrice
	byName map[string][]ModelPrice
}

// NewPriceBook indexes a price list. The ids with a ':' suffix (':batch',
// ':free') and the '~...-latest' aliases are skipped: they are not the model's
// own price.
func NewPriceBook(list []ModelPrice) *PriceBook {
	b := &PriceBook{byName: map[string][]ModelPrice{}}
	for _, p := range list {
		if p.Prompt <= 0 && p.Completion <= 0 || strings.ContainsAny(p.ID, ":~") {
			continue
		}
		b.all = append(b.all, p)
		name := p.ID[strings.LastIndexByte(p.ID, '/')+1:]
		b.byName[name] = append(b.byName[name], p)
	}
	sort.Slice(b.all, func(i, j int) bool { return b.all[i].ID < b.all[j].ID })
	return b
}

var (
	priceDate    = regexp.MustCompile(`-\d{8}$`)
	priceVersion = regexp.MustCompile(`\b(\d)-(\d)\b`)
)

// priceEfforts are the effort words that harnesses append to a model id with a
// dash. They are a known list, not any word after a hyphen.
var priceEfforts = map[string]bool{"minimal": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true, "ultra": true}

// priceNames turns one of our model ids into the names to look for: no
// provider prefix, no date, and a version with a dot ("claude-opus-5-5:high"
// becomes "claude-opus-5.5"). The exact name comes first, then the name with
// one known effort suffix removed ("gemini-4-argon-high" also tries
// "gemini-4-argon"). The exact name wins, so a model whose real name ends in
// an effort word keeps its own price.
func priceNames(model string) []string {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.IndexByte(m, ':'); i >= 0 {
		m = m[:i]
	}
	m = m[strings.LastIndexByte(m, '/')+1:]
	m = priceDate.ReplaceAllString(m, "")
	m = priceVersion.ReplaceAllString(m, "$1.$2")
	names := []string{m}
	if i := strings.LastIndexByte(m, '-'); i > 0 && priceEfforts[m[i+1:]] {
		names = append(names, m[:i])
	}
	return names
}

// priceVendor is the OpenRouter vendor of a model family, used only to pick
// between two listings of the same name.
func priceVendor(name string) string {
	switch {
	case strings.HasPrefix(name, "claude"):
		return "anthropic"
	case strings.HasPrefix(name, "gpt"), strings.HasPrefix(name, "o1"), strings.HasPrefix(name, "o3"), strings.HasPrefix(name, "o4"):
		return "openai"
	case strings.HasPrefix(name, "gemini"):
		return "google"
	case strings.HasPrefix(name, "deepseek"):
		return "deepseek"
	}
	return ""
}

// Len is how many priced models the book holds.
func (b *PriceBook) Len() int {
	if b == nil {
		return 0
	}
	return len(b.all)
}

// Lookup finds the price of one of our model ids. ok is false when no listed
// model has that name.
func (b *PriceBook) Lookup(model string) (ModelPrice, bool) {
	if b == nil {
		return ModelPrice{}, false
	}
	for _, name := range priceNames(model) {
		list := b.byName[name]
		if len(list) == 0 {
			continue
		}
		vendor := priceVendor(name)
		for _, p := range list {
			if strings.HasPrefix(p.ID, vendor+"/") {
				return p, true
			}
		}
		return list[0], true
	}
	return ModelPrice{}, false
}

// Middle is the middle priced model of the book, by prompt plus completion
// price. It stands in for a model whose mapping is not known.
func (b *PriceBook) Middle() (ModelPrice, bool) {
	if b == nil || len(b.all) == 0 {
		return ModelPrice{}, false
	}
	byPrice := append([]ModelPrice(nil), b.all...)
	sort.SliceStable(byPrice, func(i, j int) bool {
		return byPrice[i].Prompt+byPrice[i].Completion < byPrice[j].Prompt+byPrice[j].Completion
	})
	return byPrice[len(byPrice)/2], true
}

// PriceOf is Lookup with the middle price as the fallback. known is false when
// the fallback was used; ok is false when the book is empty.
func (b *PriceBook) PriceOf(model string) (p ModelPrice, known, ok bool) {
	if p, found := b.Lookup(model); found {
		return p, true, true
	}
	p, ok = b.Middle()
	return p, false, ok
}

// APICostText is a cost for a notice or a screen: "$0.43 at API prices", with
// a note when the middle price was used because the model has no listed one.
func APICostText(cost float64, priceKnown bool) string {
	text := "<$0.01"
	if cost >= .005 {
		text = fmt.Sprintf("$%.2f", cost)
	}
	text += " at API prices"
	if !priceKnown {
		text += " (price unknown, middle price used)"
	}
	return text
}

// Text is the tokens by kind for a screen: "1.2M in, 340k out, 8M cache read".
// A kind with no tokens is left out.
func (t RunTokens) Text() string {
	var parts []string
	for _, k := range []struct {
		n    float64
		name string
	}{{t.In, "in"}, {t.Out, "out"}, {t.Reasoning, "reasoning"}, {t.CacheRead, "cache read"}, {t.CacheWrite, "cache write"}} {
		if n := CompactTokens(int64(k.n)); n != "" {
			parts = append(parts, n+" "+k.name)
		}
	}
	return strings.Join(parts, ", ")
}

var tokenTextPart = regexp.MustCompile(`(\d+) (in|out|reasoning|cache read|cache write)`)

// ParseTokenText reads the "tokens:" line of a worker notice back into tokens
// by kind ("1200 in, 340 out, 50 reasoning, 90 cache read, 5 cache write"). ok
// is false when the text names no count.
func ParseTokenText(text string) (t RunTokens, ok bool) {
	for _, m := range tokenTextPart.FindAllStringSubmatch(text, -1) {
		n, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			continue
		}
		ok = true
		switch m[2] {
		case "in":
			t.In = n
		case "out":
			t.Out = n
		case "reasoning":
			t.Reasoning = n
		case "cache read":
			t.CacheRead = n
		case "cache write":
			t.CacheWrite = n
		}
	}
	return t, ok
}
