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
//  3. The candidates are ranked by: how many sessions on the machine used the
//     model in the last ModelUsageDays days (usage), the position of its
//     provider in the owner's providers list, no "-free" variant, no dated
//     snapshot (":0813"), and the shortest model name (without provider).
//  4. If one candidate is strictly best, it is the answer. If the best ones
//     are equal on every rule, or none matches, the error says so and lists
//     up to five.
func ResolveModel(query string, models []string, usage map[string]int, providers []string) (string, error) {
	query = strings.TrimSpace(query)
	for _, m := range models {
		if m == query {
			return m, nil
		}
	}
	toks := modelTokens(query)
	if len(toks) == 0 {
		return "", fmt.Errorf("model %q has no letters or digits", query)
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
		return "", fmt.Errorf("no model in the list matches %q (%d models; see `agent-net nodes --wide`)", query, len(models))
	}
	rank := func(m string) [5]int {
		r := [5]int{-usage[m], len(providers), 0, 0, len(modelPart(m))}
		for i, p := range providers {
			if providerOf(m) == p {
				r[1] = i
				break
			}
		}
		if strings.HasSuffix(m, "-free") {
			r[2] = 1
		}
		if datedSuffix.MatchString(m) {
			r[3] = 1
		}
		return r
	}
	cmp := func(a, b [5]int) int {
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
	if len(cands) == 1 || cmp(rank(cands[0]), rank(cands[1])) < 0 {
		return cands[0], nil
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
	return "", fmt.Errorf("%q matches %d models equally well: %s. Name one of them in full", query, len(tied), strings.Join(list, ", "))
}
