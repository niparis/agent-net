package protocol

import (
	"fmt"
	"regexp"
	"sort"
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

// PriceName turns one of our model ids into the name part of an OpenRouter
// id: no effort suffix, no provider prefix, no date, and a version with a dot
// ("claude-opus-5-5:high" becomes "claude-opus-5.5").
func PriceName(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.IndexByte(m, ':'); i >= 0 {
		m = m[:i]
	}
	m = m[strings.LastIndexByte(m, '/')+1:]
	m = priceDate.ReplaceAllString(m, "")
	return priceVersion.ReplaceAllString(m, "$1.$2")
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

// Lookup finds the price of one of our model ids. ok is false when no listed
// model has that name.
func (b *PriceBook) Lookup(model string) (ModelPrice, bool) {
	if b == nil {
		return ModelPrice{}, false
	}
	name := PriceName(model)
	list := b.byName[name]
	if len(list) == 0 {
		return ModelPrice{}, false
	}
	vendor := priceVendor(name)
	for _, p := range list {
		if strings.HasPrefix(p.ID, vendor+"/") {
			return p, true
		}
	}
	return list[0], true
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
