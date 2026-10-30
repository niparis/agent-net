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

// maxModelCandidates is how many candidates an ambiguity error lists.
const maxModelCandidates = 5

// datedSuffix matches a snapshot suffix such as ":0813" or "-20250929".
var datedSuffix = regexp.MustCompile(`(:\d{3,}|-\d{8})$`)

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

// ResolveModel turns a loose model name into one id of the machine's list
// (decision 0002, W17). The rules, in order:
//
//  1. An exact match wins.
//  2. Otherwise the query is split into words (case does not matter, "4.1"
//     matches "v4.1"), and the candidates are the ids that contain all words.
//  3. The candidates are ranked by: no recent worker failure (ResolveModelFailed), how many sessions on the machine used the
//     model in the last ModelUsageDays days (usage), how many used any model
//     of the same provider in that time (provider usage), the position of its
//     provider in the owner's providers list, no "-free" variant, no dated
//     snapshot (":0813"), and the shortest model name (without provider).
//  4. If one candidate is strictly best, it is the answer. If the best ones
//     are equal on every rule, or none matches, the error says so and lists
//     up to five.
func ResolveModel(query string, models []string, usage map[string]int, providers []string) (string, error) {
	r, err := ResolveModelFailed(query, models, usage, providers, nil)
	return r.Model, err
}

// ModelResolution is the answer of ResolveModelFailed.
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

// ResolveModelFailed is ResolveModel with the models that failed a worker
// recently (failed, by full id). They rank after every model that did not
// fail, whatever their usage. The other models of the same provider rank
// between the two. An exact match still wins: the caller named it.
func ResolveModelFailed(query string, models []string, usage map[string]int, providers []string, failed map[string]bool) (ModelResolution, error) {
	return ResolveModelQuota(query, models, usage, providers, failed, nil)
}

// ResolveModelQuota is ResolveModelFailed with the machine's quotas (decision
// 0022). A loose model name skips a provider that is exhausted or above
// QuotaHighPercent; a provider ahead of its reset clock ranks below providers
// on pace. An exact model id still wins, with QuotaNote when its provider is
// exhausted or nearly so.
func ResolveModelQuota(query string, models []string, usage map[string]int, providers []string, failed map[string]bool, quotas *Quotas) (ModelResolution, error) {
	query = strings.TrimSpace(query)
	for _, m := range models {
		if m == query {
			return ModelResolution{Model: m, Exact: true, QuotaNote: QuotaWarning(quotas, "", m)}, nil
		}
	}
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
		return ModelResolution{}, fmt.Errorf("no model in the list matches %q (%d models; see `agent-net nodes --wide`)", query, len(models))
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
	rank := func(m string) [8]int {
		r := [8]int{0, 0, -usage[m], -byProvider[providerOf(m)], len(providers), 0, 0, len(modelPart(m))}
		if failed[m] {
			r[0] = 2
		} else if failedProv[providerOf(m)] {
			r[0] = 1
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
		return ModelResolution{Model: cands[0], Demoted: demoted}, nil
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
