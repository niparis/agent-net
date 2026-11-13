// Package model holds the one choice path every session start uses: rankers
// order the candidates, modifiers reorder that list, and the gate returns the
// first candidate that can run now (decision 0162).
//
// The gate owns the checks that no caller may switch off. A candidate whose
// harness is not installed or not signed in, whose provider authentication
// failed, whose provider is exhausted, or whose provider a live session
// reported limited never passes, whatever Checks says.
package model

import (
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Candidate is one harness, provider and model a route may start.
type Candidate struct {
	Name     string
	Harness  string
	Model    string
	Effort   string
	Provider string
}

// Key identifies a candidate's harness and model for the facts snapshot.
func (c Candidate) Key() string { return c.Harness + "\x00" + c.Model }

// Display is the name a note uses for a candidate.
func (c Candidate) Display() string {
	if strings.TrimSpace(c.Name) != "" {
		return c.Name
	}
	return c.Harness + " " + c.Model
}

// QuotaProvider is the provider whose windows limit this candidate.
func (c Candidate) QuotaProvider() string {
	if c.Provider != "" {
		return c.Provider
	}
	return protocol.QuotaProviderForStart(c.Harness, c.Model)
}

// Checks are the optional steps a caller's policy may turn on. The locked
// checks have no field here on purpose: no caller can turn them off.
type Checks struct {
	Floor   bool
	Block90 bool
	Burn    bool
	Cooling bool
}

// HarnessFact says whether one harness is installed and signed in on the
// machine that would run the start.
type HarnessFact struct {
	Hostname  string
	Installed bool
	SignedIn  string
}

// Limit is a provider limit that a live session reported.
type Limit struct {
	Until  int64
	Reason string
}

// CoolingFact is a model that is cooling down after infrastructure failures.
type CoolingFact struct {
	Failures int
	Until    time.Time
}

// Facts is one snapshot that a whole start decides on: the one owner quota
// reading with burn, the machine's harnesses, auth blocks, model health and
// the provider limits live sessions reported.
type Facts struct {
	Now         time.Time
	Hostname    string
	Harnesses   map[string]HarnessFact
	AuthBlocked map[string]bool
	Limits      map[string]Limit
	Quotas      *protocol.Quotas
	Floors      protocol.WorkerFloors
	Cooling     map[string]CoolingFact
}

// Skip is one candidate the gate left out, with the reason it did.
type Skip struct {
	Candidate Candidate
	Reason    string
	// Check names the step that skipped the candidate. It is for the note and
	// the roster print order; it never changes the decision.
	Check string
	// Locked is true for a check that no policy can switch off.
	Locked bool
}

// Gate returns the first candidate that can run now, with the reason for each
// candidate it skipped before that one. It is the only way to know a candidate
// may start, and it cannot be asked to skip a locked check.
func Gate(list []Candidate, checks Checks, facts Facts) (Candidate, []Skip, error) {
	var skips []Skip
	for _, c := range list {
		if skip, blocked := lockedSkip(c, facts); blocked {
			skips = append(skips, skip)
			continue
		}
		if skip, blocked := optionalSkip(c, checks, facts); blocked {
			skips = append(skips, skip)
			continue
		}
		return c, skips, nil
	}
	return Candidate{}, skips, fmt.Errorf("no candidate can run now: %s", skipSummary(skips))
}

// lockedSkip applies the checks that have no switch.
func lockedSkip(c Candidate, facts Facts) (Skip, bool) {
	skip := Skip{Candidate: c, Check: "locked", Locked: true}
	h, installed := facts.Harnesses[c.Harness]
	if !installed || !h.Installed {
		host := facts.Hostname
		if host == "" {
			host = h.Hostname
		}
		if host == "" {
			host = "this machine"
		}
		skip.Check = "installed"
		skip.Reason = fmt.Sprintf("harness %s is not installed on %s", c.Harness, host)
		return skip, true
	}
	if h.SignedIn == "no" {
		host := facts.Hostname
		if host == "" {
			host = h.Hostname
		}
		if host == "" {
			host = "this machine"
		}
		skip.Check = "signed-in"
		skip.Reason = fmt.Sprintf("harness %s is not signed in on %s", c.Harness, host)
		return skip, true
	}
	if facts.AuthBlocked[c.Key()] {
		skip.Check = "auth"
		skip.Reason = "provider authentication failed"
		return skip, true
	}
	if limit, ok := facts.Limits[c.QuotaProvider()]; ok && limit.Until > facts.Now.Unix() {
		skip.Check = "limit"
		skip.Reason = fmt.Sprintf("%s is limited until %s (a live session reported it)",
			c.QuotaProvider(), time.Unix(limit.Until, 0).UTC().Format("2006-01-02 15:04 UTC"))
		if limit.Reason != "" {
			skip.Reason += ": " + limit.Reason
		}
		return skip, true
	}
	if reason := protocol.QuotaRefusal(facts.Quotas, c.Harness, c.Model); reason != "" {
		skip.Check = "exhausted"
		skip.Reason = reason
		return skip, true
	}
	return Skip{}, false
}

// optionalSkip applies the steps the caller's policy turned on.
func optionalSkip(c Candidate, checks Checks, facts Facts) (Skip, bool) {
	skip := Skip{Candidate: c}
	if checks.Floor {
		if under, reason, _ := protocol.CheckWorkerFloor(facts.Quotas, facts.Floors, c.Harness, c.Model); under {
			skip.Check = "floor"
			skip.Reason = reason
			return skip, true
		}
	}
	if checks.Block90 {
		if _, reason, _, blocked := protocol.QuotaRoomForProvider(facts.Quotas, c.QuotaProvider(), c.Model); blocked {
			skip.Check = "block90"
			skip.Reason = reason
			return skip, true
		}
	}
	if checks.Burn {
		if reason, hold := protocol.QuotaBurnHold(facts.Quotas, c.QuotaProvider(), c.Model); hold {
			skip.Check = "burn"
			skip.Reason = reason
			return skip, true
		}
	}
	if checks.Cooling {
		if cool, ok := facts.Cooling[c.Key()]; ok && cool.Until.After(facts.Now) {
			skip.Check = "cooling"
			skip.Reason = fmt.Sprintf("cooling down after %d infrastructure failures (until %s UTC)",
				cool.Failures, cool.Until.UTC().Format("15:04"))
			return skip, true
		}
	}
	return Skip{}, false
}

// skipSummary writes the skip reasons for an error. It stays short: the first
// three candidates, then a count.
func skipSummary(skips []Skip) string {
	if len(skips) == 0 {
		return "the list was empty"
	}
	parts := make([]string, 0, 3)
	for i, s := range skips {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("and %d more", len(skips)-3))
			break
		}
		parts = append(parts, s.Candidate.Display()+": "+s.Reason)
	}
	return strings.Join(parts, "; ")
}
