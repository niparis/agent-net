package protocol

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// The quota burn check (decision 0144). A provider can run out of quota in
// minutes when many workers start on it at once, and the quota reading does
// not always see it coming. Before an automatic start the picker projects the
// burn of the workers that run now on the provider, and holds the start back
// when one more worker would not last until the reset.
//
// Everything is measured. The burn rate is the tokens per second that the
// running workers have used so far. The size of the quota in tokens comes from
// the reading (tokens that the window has seen, divided by its percent used)
// or, when no reading shows the limit, from the budget that the last quota
// failure of the provider taught (the tokens used in the window before it).
// There is no fixed cap and no setting.

// BurnRun is one worker run as the burn check needs it: when it started, when
// it ended (0 while it runs) and the tokens it has used.
type BurnRun struct {
	StartedAt int64
	EndedAt   int64
	Tokens    int64
}

// QuotaBudget is the size of a provider's quota window in tokens, learned from
// a quota failure.
type QuotaBudget struct {
	WindowSeconds int64
	Tokens        int64
}

// QuotaBurn is what the gateway measured for one provider: its recent runs,
// the running ones included, and the budgets that failures taught.
type QuotaBurn struct {
	Runs    []BurnRun
	Budgets []QuotaBudget
}

// QuotaBurnSet is the burn of every provider at one moment. It is built by the
// gateway for one start and is never sent on the wire.
type QuotaBurnSet struct {
	Now        int64
	ByProvider map[string]QuotaBurn
}

// quotaBurnMinElapsed is the least total run time of the running workers that
// makes a rate. A worker that has run a few seconds says nothing yet.
const quotaBurnMinElapsed = 60 // seconds

// quotaBurnMinPercent is the least percent used at which a window can size the
// quota. Below it, the ratio of tokens to percent is mostly rounding.
const quotaBurnMinPercent = 1.0

// quotaStandardWindows are the window lengths quota providers use. A quota
// failure that names only a reset delay is filed under the shortest of them
// that holds the delay.
var quotaStandardWindows = []time.Duration{5 * time.Hour, 24 * time.Hour, 7 * 24 * time.Hour, 31 * 24 * time.Hour}

// QuotaBudgetWindow is the length of the window that a reset delay belongs to.
func QuotaBudgetWindow(wait time.Duration) time.Duration {
	for _, d := range quotaStandardWindows {
		if wait <= d {
			return d
		}
	}
	return quotaStandardWindows[len(quotaStandardWindows)-1]
}

// QuotaBurnLookback is how far back the gateway reads runs: the longest window.
func QuotaBurnLookback() time.Duration {
	return quotaStandardWindows[len(quotaStandardWindows)-1]
}

// BurnTokensSince adds the tokens that runs used since a time. A run that
// began before it counts for the part of its life after it. A running run
// lasts until now.
func BurnTokensSince(runs []BurnRun, since, now int64) float64 {
	var total float64
	for _, r := range runs {
		end := r.EndedAt
		if end == 0 || end > now {
			end = now
		}
		if end <= since || r.Tokens <= 0 {
			continue
		}
		start := r.StartedAt
		if start >= end {
			total += float64(r.Tokens)
			continue
		}
		from := max(start, since)
		total += float64(r.Tokens) * float64(end-from) / float64(end-start)
	}
	return total
}

// LearnQuotaBudget reads a quota failure. The provider ran out of quota at
// failedAt and named its reset at resetAt. The tokens that its runs used in
// the window before the failure are the budget. It returns false when no run
// used any token, so nothing can be learned.
func LearnQuotaBudget(runs []BurnRun, failedAt, resetAt time.Time) (QuotaBudget, bool) {
	window := QuotaBudgetWindow(resetAt.Sub(failedAt))
	tokens := BurnTokensSince(runs, failedAt.Add(-window).Unix(), failedAt.Unix())
	if tokens < 1 {
		return QuotaBudget{}, false
	}
	return QuotaBudget{WindowSeconds: int64(window / time.Second), Tokens: int64(tokens)}, true
}

// burnTest is one window that a start is projected against.
type burnTest struct {
	label     string
	remaining float64 // tokens left in the window
	toReset   float64 // seconds to the reset, 0 when it is not known
	source    string  // where the size of the quota came from
}

// QuotaBurnHold tells whether one more worker on provider would run the quota
// out before its reset, given what the running workers have burned so far. The
// reason says what was measured. It never holds the first worker of a
// provider: with none running there is no rate to project.
func QuotaBurnHold(q *Quotas, provider, model string) (string, bool) {
	if q == nil || q.Burn == nil {
		return "", false
	}
	b, ok := q.Burn.ByProvider[provider]
	if !ok {
		return "", false
	}
	now := q.Burn.Now
	var running int
	var tokens, elapsed float64
	var finished, finishedSeconds float64
	for _, r := range b.Runs {
		if r.EndedAt == 0 {
			running++
			tokens += float64(r.Tokens)
			elapsed += float64(max(now-r.StartedAt, 0))
		} else if r.EndedAt > r.StartedAt {
			finished++
			finishedSeconds += float64(r.EndedAt - r.StartedAt)
		}
	}
	if running == 0 || elapsed < quotaBurnMinElapsed || tokens <= 0 {
		return "", false
	}
	rate := tokens / elapsed // tokens per second of one worker
	// A worker is expected to run as long as the finished ones did. Without
	// finished runs, the running ones have shown at least their age.
	life := elapsed / float64(running)
	if finished > 0 {
		life = finishedSeconds / finished
	}

	var tests []burnTest
	if p, ok := q.Provider(provider); ok && p.usable() {
		for _, w := range p.Windows {
			if !QuotaWindowServes(w, model) || w.Exhausted || w.PercentUsed >= 100 {
				continue
			}
			duration := quotaWindowDuration(w)
			if duration == 0 || w.ResetsAt <= now {
				continue
			}
			perPercent, source := 0.0, ""
			if w.PercentUsed >= quotaBurnMinPercent {
				if used := BurnTokensSince(b.Runs, w.ResetsAt-int64(duration/time.Second), now); used > 0 {
					perPercent, source = used/w.PercentUsed, "measured from this window"
				}
			}
			if perPercent == 0 {
				for _, bu := range b.Budgets {
					if bu.WindowSeconds == int64(duration/time.Second) && bu.Tokens > 0 {
						perPercent, source = float64(bu.Tokens)/100, "learned from an earlier quota failure"
					}
				}
			}
			if perPercent == 0 {
				continue
			}
			tests = append(tests, burnTest{
				label:     fmt.Sprintf("%s window, %.0f%% used", QuotaWindowName(w), w.PercentUsed),
				remaining: (100 - w.PercentUsed) * perPercent,
				toReset:   float64(w.ResetsAt - now),
				source:    source,
			})
		}
	}
	if len(tests) == 0 {
		// No reading shows a limit: the budgets that failures taught stand in.
		budgets := append([]QuotaBudget(nil), b.Budgets...)
		sort.Slice(budgets, func(i, j int) bool { return budgets[i].WindowSeconds < budgets[j].WindowSeconds })
		for _, bu := range budgets {
			if bu.Tokens <= 0 || bu.WindowSeconds <= 0 {
				continue
			}
			used := BurnTokensSince(b.Runs, now-bu.WindowSeconds, now)
			tests = append(tests, burnTest{
				label:     fmt.Sprintf("%s window (no reading)", burnDuration(float64(bu.WindowSeconds))),
				remaining: float64(bu.Tokens) - used,
				source:    "learned from an earlier quota failure, " + burnTokens(float64(bu.Tokens)) + " tokens a window",
			})
		}
	}
	for _, t := range tests {
		horizon := life
		if t.toReset > 0 && t.toReset < horizon {
			horizon = t.toReset
		}
		need := rate * float64(running+1) * horizon
		if need <= t.remaining {
			continue
		}
		left := t.remaining
		if left < 0 {
			left = 0
		}
		reset := "its reset is not known"
		if t.toReset > 0 {
			reset = "it resets in " + burnDuration(t.toReset)
		}
		return fmt.Sprintf("%s: %d running worker(s) have used %s tokens a minute each; one more would need about %s tokens in the next %s, and the %s has about %s left (%s; %s)",
			provider, running, burnTokens(rate*60), burnTokens(need), burnDuration(horizon), t.label, burnTokens(left), reset, t.source), true
	}
	return "", false
}

// dropBurnHeld removes the candidates whose provider would run out of quota
// with one more worker. It returns the others and one reason per provider.
func dropBurnHeld(candidates []WorkerModelCandidate, q *Quotas) ([]WorkerModelCandidate, []string) {
	if q == nil || q.Burn == nil {
		return candidates, nil
	}
	var kept []WorkerModelCandidate
	var held []string
	seen := map[string]bool{}
	for _, c := range candidates {
		provider := c.Provider
		if provider == "" {
			provider = QuotaProviderForStart(c.Harness, c.Model)
		}
		reason, hold := QuotaBurnHold(q, provider, c.Model)
		if !hold {
			kept = append(kept, c)
			continue
		}
		if !seen[reason] {
			seen[reason] = true
			held = append(held, reason)
		}
	}
	return kept, held
}

// burnTokens writes a token count short: 840, 12k, 3.4M.
func burnTokens(n float64) string {
	switch {
	case n >= 999_500:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", n/1e6), ".0") + "M"
	case n >= 1e3:
		return fmt.Sprintf("%.0fk", n/1e3)
	}
	return fmt.Sprintf("%.0f", n)
}

// burnDuration writes seconds as 45m, 2h0m or 3d4h.
func burnDuration(sec float64) string {
	d := time.Duration(sec) * time.Second
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", max(int(d.Minutes()), 1))
}
