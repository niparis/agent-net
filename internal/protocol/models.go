package protocol

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ModelUsageDays is how far back the gateway looks when it counts which model
// the sessions of a machine used (see ResolveModel).
const ModelUsageDays = 30

// ModelFailureHours is how long a model that failed a worker (provider, billing
// or auth error) is ranked last by ResolveModelFailed.
const ModelFailureHours = 24

// ModelSuccessDays is how far back a worker that finished with a hand-back
// status done makes its model rank first (see ResolveModelRanked).
const ModelSuccessDays = 7

// maxModelCandidates is how many candidates an ambiguity error lists.
const maxModelCandidates = 5

// datedSuffix matches a snapshot suffix such as ":0813" or "-20250929".
var datedSuffix = regexp.MustCompile(`(:\d{3,}|-\d{8})$`)

// modelLevels are the words after a colon that set a harness reasoning or
// thinking level: Codex's efforts and Pi's thinking levels. They are not part
// of the model id, so a loose name is resolved without its suffix and the
// suffix is put back.
var modelLevels = map[string]bool{
	"none": true, "off": true, "minimal": true, "low": true, "medium": true,
	"high": true, "xhigh": true, "max": true, "ultra": true, "persistent": true,
}

// SplitModelLevel splits a trailing ":<level>" from a model name. A colon that
// is followed by anything else (a dated suffix such as ":1017", or a word such
// as ":free") stays in the name.
func SplitModelLevel(model string) (name, level string) {
	i := strings.LastIndexByte(model, ':')
	if i <= 0 || i == len(model)-1 {
		return model, ""
	}
	l := strings.ToLower(model[i+1:])
	if !modelLevels[l] {
		return model, ""
	}
	return model[:i], model[i+1:]
}

// modelTokens splits a loose model name into lower case words. A dot between
// digits stays in the word ("4.1"), and a "v" before a digit is dropped, so
// that "v4.1" and "4.1" are the same word.
func modelTokens(s string) []string {
	rs := []rune(strings.ToLower(s))
	var toks []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			if len(cur) > 1 && cur[0] == 'v' && cur[1] >= '0' && cur[1] <= '9' {
				cur = cur[1:]
			}
			toks = append(toks, string(cur))
			cur = nil
		}
	}
	digit := func(r rune) bool { return r >= '0' && r <= '9' }
	for i, r := range rs {
		switch {
		case r >= 'a' && r <= 'z' || digit(r):
			cur = append(cur, r)
		case r == '.' && i > 0 && i+1 < len(rs) && digit(rs[i-1]) && digit(rs[i+1]):
			cur = append(cur, r)
		default:
			flush()
		}
	}
	flush()
	return toks
}

// hasToken tells whether the id contains the word. A word that starts with a
// digit must not follow another digit or a dot ("4.1" is not in "14.1").
func hasToken(id, tok string) bool {
	for from := 0; from < len(id); {
		i := strings.Index(id[from:], tok)
		if i < 0 {
			return false
		}
		i += from
		if tok[0] < '0' || tok[0] > '9' || i == 0 || !(id[i-1] >= '0' && id[i-1] <= '9' || id[i-1] == '.') {
			return true
		}
		from = i + 1
	}
	return false
}

func modelWithLevel(model, level string) string {
	if level == "" {
		return model
	}
	return model + ":" + level
}

func modelPart(id string) string {
	if i := strings.Index(id, "/"); i >= 0 {
		return id[i+1:]
	}
	return id
}

// quotaHigh tells whether a provider is above QuotaHighPercent in a window.
func quotaHigh(q *Quotas, provider string) bool {
	if q == nil {
		return false
	}
	_, ok := q.HighWindow(provider)
	return ok
}

func providerOf(id string) string {
	if i := strings.Index(id, "/"); i >= 0 {
		return id[:i]
	}
	return ""
}

// NoModelMatchError says that a loose name matched no model. Closest holds up
// to maxModelCandidates models that look like it, best first; it is empty when
// nothing is close.
type NoModelMatchError struct {
	Query   string
	Count   int
	Closest []string
}

func (e *NoModelMatchError) Error() string {
	s := fmt.Sprintf("no model in the list matches %q", e.Query)
	if len(e.Closest) > 0 {
		s += "; closest: " + strings.Join(e.Closest, ", ")
	}
	return s + fmt.Sprintf(" (%d models; see `agent-net nodes --wide`)", e.Count)
}

// ResolveModel turns a loose model name into one id of the machine's list
// (decision 0002, W17). The rules, in order:
//
//  1. An exact match wins.
//  2. Otherwise the query is split into words (case does not matter, "4.1"
//     matches "v4.1"), and the candidates are the ids that contain all words.
//  3. The candidates are ranked by: a recent successful worker
//     (ResolveModelRanked), no recent worker failure (ResolveModelFailed), how
//     many sessions on the machine used the model in the last ModelUsageDays
//     days (usage), how many used any model of the same provider in that time
//     (provider usage), the position of its provider in the owner's providers
//     list, no "-free" variant, no dated snapshot (":0813"), and the shortest
//     model name (without provider).
//  4. If one candidate is strictly best, it is the answer. If the best ones
//     are equal on every rule, or none matches, the error says so and lists
//     up to five.
//
// A trailing reasoning or thinking level (Codex :high, Pi :thinking) is kept:
// the base id is resolved and the level is added back.
func ResolveModel(query string, models []string, usage map[string]int, providers []string) (string, error) {
	r, err := ResolveModelRanked(query, models, usage, providers, nil, nil)
	return r.Model, err
}

// ModelResolution is the answer of ResolveModelRanked.
type ModelResolution struct {
	Model string
	// Exact is true when the query was the full model id.
	Exact bool
	// Demoted lists the matching models that were ranked last because they
	// failed a worker recently. It includes Model when that one failed too and
	// nothing better matched.
	Demoted []string
	// QuotaNote warns that an exact model id draws on a provider that is
	// exhausted or above QuotaHighPercent. Empty otherwise (decision 0022).
	QuotaNote string
}

// ResolveModelFailed is ResolveModelRanked without the recent successes.
func ResolveModelFailed(query string, models []string, usage map[string]int, providers []string, failed map[string]bool) (ModelResolution, error) {
	return ResolveModelRanked(query, models, usage, providers, failed, nil)
}

// ResolveModelQuota is ResolveModelRankedQuota without the recent successes.
func ResolveModelQuota(query string, models []string, usage map[string]int, providers []string, failed map[string]bool, quotas *Quotas) (ModelResolution, error) {
	return ResolveModelRankedQuota(query, models, usage, providers, failed, nil, quotas)
}

// ResolveModelRanked is ResolveModelRankedQuota without quotas.
func ResolveModelRanked(query string, models []string, usage map[string]int, providers []string, failed, succeeded map[string]bool) (ModelResolution, error) {
	return ResolveModelRankedQuota(query, models, usage, providers, failed, succeeded, nil)
}

// ResolveModelRankedQuota is ResolveModel with the models that failed a worker
// recently (failed, by full id), those that finished one successfully
// (succeeded, by full id), and the machine's quotas (decision 0022).
//
// A model that failed a worker ranks after every other model, whatever its
// usage; the other models of the same provider rank between the two. A model
// that recently finished a worker with a hand-back status done ranks before
// models without such a success. A loose name skips a provider that is
// exhausted or above QuotaHighPercent, and a provider ahead of its reset clock
// ranks below providers on pace. An exact match still wins, with QuotaNote
// when its provider is exhausted or nearly so.
func ResolveModelRankedQuota(query string, models []string, usage map[string]int, providers []string, failed, succeeded map[string]bool, quotas *Quotas) (ModelResolution, error) {
	query = strings.TrimSpace(query)
	for _, m := range models {
		if m == query {
			return ModelResolution{Model: m, Exact: true, QuotaNote: QuotaWarning(quotas, "", m)}, nil
		}
	}
	// A thinking or reasoning level is not part of the id: resolve the name
	// before it, and put the suffix back on the result.
	base, level := SplitModelLevel(query)
	if level != "" {
		for _, m := range models {
			if m == base {
				return ModelResolution{Model: modelWithLevel(m, level)}, nil
			}
		}
	}
	query = base
	toks := modelTokens(query)
	if len(toks) == 0 {
		return ModelResolution{}, fmt.Errorf("model %q has no letters or digits", query)
	}
	var cands []string
	for _, m := range models {
		low := strings.ToLower(m)
		ok := true
		for _, t := range toks {
			ok = ok && hasToken(low, t)
		}
		if ok {
			cands = append(cands, m)
		}
	}
	if len(cands) == 0 {
		return ModelResolution{}, &NoModelMatchError{Query: query, Count: len(models), Closest: closestModels(toks, models)}
	}
	// A provider that is exhausted, or above QuotaHighPercent of any window,
	// is skipped for a loose name. An exact id was handled above.
	var blocked []string
	if quotas != nil {
		keep := cands[:0:0]
		seen := map[string]bool{}
		for _, m := range cands {
			qp := QuotaProviderForModel(m)
			if qp == "" {
				keep = append(keep, m)
				continue
			}
			if _, ok := quotas.ExhaustedWindow(qp); ok || quotaHigh(quotas, qp) {
				if !seen[qp] {
					seen[qp] = true
					if w, ok := quotas.ExhaustedWindow(qp); ok {
						blocked = append(blocked, fmt.Sprintf("%s is exhausted (%s; %s)", qp, QuotaWindowName(w), QuotaResetText(w)))
					} else if w, ok := quotas.HighWindow(qp); ok {
						blocked = append(blocked, fmt.Sprintf("%s is at %.0f%% of its %s window", qp, w.PercentUsed, QuotaWindowName(w)))
					}
				}
				continue
			}
			keep = append(keep, m)
		}
		if len(keep) == 0 {
			sort.Strings(blocked)
			return ModelResolution{}, fmt.Errorf("every model that matches %q is out of quota: %s", query, strings.Join(blocked, "; "))
		}
		cands = keep
	}
	// Sessions per provider: a provider the owner uses for other models wins
	// over one the owner never used.
	byProvider := map[string]int{}
	for m, n := range usage {
		byProvider[providerOf(m)] += n
	}
	// A billing or auth error is usually the provider's, not the model's: the
	// other models of that provider rank after the rest too, but before the
	// model that failed.
	failedProv := map[string]bool{}
	for m, f := range failed {
		if f && providerOf(m) != "" {
			failedProv[providerOf(m)] = true
		}
	}
	// r[0] ranks a recent success first (0), then a model with no recent
	// history (1), then a model whose provider failed (2), then the model that
	// failed (3). r[1] is the quota pace, then usage and the older rules.
	rank := func(m string) [8]int {
		r := [8]int{1, 0, -usage[m], -byProvider[providerOf(m)], len(providers), 0, 0, len(modelPart(m))}
		switch {
		case failed[m]:
			r[0] = 3
		case succeeded[m]:
			r[0] = 0
		case failedProv[providerOf(m)]:
			r[0] = 2
		}
		if quotas != nil && quotas.Ahead(QuotaProviderForModel(m)) {
			r[1] = 1
		}
		for i, p := range providers {
			if providerOf(m) == p {
				r[4] = i
				break
			}
		}
		if strings.HasSuffix(m, "-free") {
			r[5] = 1
		}
		if datedSuffix.MatchString(m) {
			r[6] = 1
		}
		return r
	}
	cmp := func(a, b [8]int) int {
		for i := range a {
			if a[i] != b[i] {
				if a[i] < b[i] {
					return -1
				}
				return 1
			}
		}
		return 0
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if c := cmp(rank(cands[i]), rank(cands[j])); c != 0 {
			return c < 0
		}
		return cands[i] < cands[j]
	})
	var demoted []string
	for _, c := range cands {
		if failed[c] {
			demoted = append(demoted, c)
		}
	}
	if len(cands) == 1 || cmp(rank(cands[0]), rank(cands[1])) < 0 {
		return ModelResolution{Model: modelWithLevel(cands[0], level), Demoted: demoted}, nil
	}
	var tied []string
	for _, c := range cands {
		if cmp(rank(cands[0]), rank(c)) == 0 {
			tied = append(tied, c)
		}
	}
	list := tied
	if len(list) > maxModelCandidates {
		list = list[:maxModelCandidates]
	}
	return ModelResolution{}, fmt.Errorf("%q matches %d models equally well: %s. Name one of them in full", query, len(tied), strings.Join(list, ", "))
}

// closestModels ranks the models that share a word with the query, by how many
// query words they contain, then by how many extra words they have and how
// short the model name is. It returns at most maxModelCandidates.
func closestModels(qtoks []string, models []string) []string {
	type scored struct {
		id    string
		score int
		extra int
		size  int
	}
	var all []scored
	for _, m := range models {
		part := modelPart(m)
		low := strings.ToLower(part)
		mtoks := modelTokens(part)
		matched := 0
		for _, q := range qtoks {
			hit := false
			for _, mt := range mtoks {
				if mt == q {
					hit = true
					break
				}
			}
			if !hit {
				hit = hasToken(low, q)
			}
			if hit {
				matched++
			}
		}
		if matched == 0 {
			continue
		}
		extra := 0
		for _, mt := range mtoks {
			used := false
			for _, q := range qtoks {
				if mt == q || hasToken(mt, q) {
					used = true
					break
				}
			}
			if !used {
				extra++
			}
		}
		all = append(all, scored{id: m, score: matched, extra: extra, size: len(part)})
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.extra != b.extra {
			return a.extra < b.extra
		}
		if a.size != b.size {
			return a.size < b.size
		}
		return a.id < b.id
	})
	var out []string
	for _, s := range all {
		if len(out) == maxModelCandidates {
			break
		}
		out = append(out, s.id)
	}
	return out
}
