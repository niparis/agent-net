package protocol

import (
	"fmt"
	"strings"
	"time"
)

// PickQuota is what one automatic pick read of the quota windows (decision
// 0191). A pick's rows (PickCandidate) already hold each candidate's pace room,
// factor and the gate's reason; this holds what they were read from: each
// provider's windows once, and for each candidate which windows serve its
// model, which one decided and which gate step held it back.
type PickQuota struct {
	// At is when the pick ran, in Unix seconds.
	At int64 `json:"at"`
	// ReadAt is the time of the reading the pace rule measured against, in
	// Unix seconds: elapsed time in a window is counted up to it, not to At.
	ReadAt     int64                `json:"read_at,omitempty"`
	Providers  []PickQuotaProvider  `json:"providers,omitempty"`
	Candidates []PickQuotaCandidate `json:"candidates,omitempty"`
}

// PickQuotaProvider is one provider's reading as the pick saw it.
type PickQuotaProvider struct {
	Provider string `json:"provider"`
	// ReadAt is when this provider's reading was made, in Unix seconds. It can
	// be older than the pick's reading when another machine's was borrowed.
	ReadAt  int64             `json:"read_at,omitempty"`
	Stale   bool              `json:"stale,omitempty"`
	Error   string            `json:"error,omitempty"`
	Windows []PickQuotaWindow `json:"windows,omitempty"`
}

// PickQuotaWindow is one quota window as read.
type PickQuotaWindow struct {
	ID        string  `json:"id"`
	Label     string  `json:"label,omitempty"`
	Kind      string  `json:"kind,omitempty"`
	Used      float64 `json:"used"`
	ResetsAt  int64   `json:"resets_at,omitempty"`
	Exhausted bool    `json:"exhausted,omitempty"`
}

// PickQuotaCandidate is what the pick read for the candidate at Pos, the
// position of its row.
type PickQuotaCandidate struct {
	Pos int `json:"pos"`
	// Provider is the quota provider read for it; empty when it has none.
	Provider string `json:"provider,omitempty"`
	// Windows are the IDs of the provider's windows that serve its model.
	Windows []string `json:"windows,omitempty"`
	// Decided is the ID of the window that decided: the one that held it
	// back (floor, 90 percent, used up), else the one that set its pace room.
	Decided string `json:"decided,omitempty"`
	// Check is the gate step that held it back: floor, block90, burn,
	// exhausted, limit and the others of the gate. Empty when none.
	Check string `json:"check,omitempty"`
	// Floor is the owner's worker floor for its provider, in percent left.
	Floor int `json:"floor,omitempty"`
}

// PickRunEnd is what the run that the pick chose used: its tokens by kind,
// and its provider's windows as read when it ended.
type PickRunEnd struct {
	EndedAt  int64     `json:"ended_at"`
	Provider string    `json:"provider,omitempty"`
	Tokens   RunTokens `json:"tokens"`
	// After is the provider's reading at the end, nil when none existed.
	After *PickQuotaProvider `json:"after,omitempty"`
}

// PickQuotaProviderOf copies one provider's reading.
func PickQuotaProviderOf(p QuotaProvider) PickQuotaProvider {
	out := PickQuotaProvider{Provider: p.Provider, ReadAt: p.ReadAt, Stale: p.Stale, Error: p.Error}
	for _, w := range p.Windows {
		out.Windows = append(out.Windows, PickQuotaWindow{ID: w.ID, Label: w.Label, Kind: w.Kind, Used: w.PercentUsed, ResetsAt: w.ResetsAt, Exhausted: w.Exhausted})
	}
	return out
}

// PickQuotaRead builds what a pick read, from the start's one reading, the
// owner's floors and the pick's rows. checks names, by row position, the gate
// step that held each candidate back.
func PickQuotaRead(q *Quotas, floors WorkerFloors, rows []PickCandidate, checks map[int]string, at int64) *PickQuota {
	if len(rows) == 0 {
		return nil
	}
	out := &PickQuota{At: at}
	if q != nil {
		out.ReadAt = q.GeneratedAt
	}
	seen := map[string]bool{}
	for pos, r := range rows {
		c := Candidate{Harness: r.Harness, Provider: r.Provider, Model: r.Model, Effort: r.Effort}
		provider := c.QuotaProvider()
		read := PickQuotaCandidate{Pos: pos, Check: checks[pos]}
		floor := WorkerFloorRead(q, floors, r.Harness, r.Model)
		read.Floor = floor.Floor
		if p, ok := q.Provider(provider); ok {
			read.Provider = provider
			if !seen[provider] {
				seen[provider] = true
				out.Providers = append(out.Providers, PickQuotaProviderOf(p))
			}
			for _, w := range p.Windows {
				if QuotaWindowServes(w, r.Model) {
					read.Windows = append(read.Windows, w.ID)
				}
			}
			// A row recorded before the level rule was removed reads its pace
			// without the blocked windows; a cell row and the gate read them.
			if w, ok := QuotaPaceWindow(q, provider, r.Model, r.Rule == PickRuleLevel); ok {
				read.Decided = w.ID
			}
		}
		if read.Check == "floor" && floor.Under {
			read.Decided = floor.Window.ID
		}
		out.Candidates = append(out.Candidates, read)
	}
	return out
}

// PickQuotaLines says in plain words what a pick read and what its run used.
// rows are the pick's rows, for the names, the pace room and the gate's reason.
func PickQuotaLines(q *PickQuota, end *PickRunEnd, rows []PickCandidate) []string {
	if q == nil {
		return nil
	}
	var lines []string
	if q.ReadAt > 0 {
		lines = append(lines, fmt.Sprintf("quota reading of %s, %s before the pick", quotaClock(q.ReadAt), quotaAge(q.At-q.ReadAt)))
	} else {
		lines = append(lines, "no quota reading at the pick")
	}
	byProvider := map[string]PickQuotaProvider{}
	for _, p := range q.Providers {
		byProvider[p.Provider] = p
		lines = append(lines, p.Provider+": "+pickProviderText(p, q.At))
	}
	for _, c := range q.Candidates {
		name := fmt.Sprintf("candidate %d", c.Pos+1)
		var row *PickCandidate
		if c.Pos < len(rows) {
			row = &rows[c.Pos]
			name = row.Name
		}
		lines = append(lines, name+": "+pickCandidateText(c, byProvider[c.Provider], row))
	}
	if end != nil {
		lines = append(lines, pickRunEndText(*end, byProvider[end.Provider]))
	}
	return lines
}

func pickProviderText(p PickQuotaProvider, at int64) string {
	var parts []string
	switch {
	case p.Error != "":
		parts = append(parts, "reading failed ("+p.Error+")")
	case p.Stale:
		parts = append(parts, "stale reading, not used")
	}
	if p.ReadAt > 0 && at > 0 {
		parts = append(parts, fmt.Sprintf("read %s before the pick", quotaAge(at-p.ReadAt)))
	}
	if len(p.Windows) == 0 {
		parts = append(parts, "no windows")
	}
	for _, w := range p.Windows {
		parts = append(parts, pickWindowText(w))
	}
	return strings.Join(parts, "; ")
}

func pickWindowText(w PickQuotaWindow) string {
	text := fmt.Sprintf("%s window %.0f%% used", pickWindowName(w), w.Used)
	if w.Exhausted {
		text += " (used up)"
	}
	if w.ResetsAt > 0 {
		text += ", resets " + quotaClock(w.ResetsAt)
	}
	return text
}

func pickWindowName(w PickQuotaWindow) string {
	return QuotaWindowName(QuotaWindow{ID: w.ID, Label: w.Label, Kind: w.Kind})
}

func pickCandidateText(c PickQuotaCandidate, p PickQuotaProvider, row *PickCandidate) string {
	var parts []string
	switch {
	case c.Provider == "":
		parts = append(parts, "no quota reading for its provider")
	case len(c.Windows) == 0:
		parts = append(parts, c.Provider+", no window serves it")
	default:
		parts = append(parts, c.Provider+" "+strings.Join(c.Windows, ", ")+" read")
	}
	if c.Decided != "" {
		decided := c.Decided
		for _, w := range p.Windows {
			if w.ID == c.Decided {
				decided = fmt.Sprintf("%s (%.0f%% used)", pickWindowName(w), w.Used)
			}
		}
		text := "decided by " + decided
		if row != nil && row.PaceRoom != nil && c.Check == "" {
			text += fmt.Sprintf(", %s, pace factor %.2f", paceRoomText(*row.PaceRoom), row.PaceFactor)
		}
		parts = append(parts, text)
	}
	if c.Floor > 0 {
		parts = append(parts, fmt.Sprintf("worker floor %d%% left", c.Floor))
	}
	if c.Check != "" {
		held := "held back by the gate (" + c.Check + ")"
		if row != nil {
			if reason := firstNonEmptyText(row.SkippedReason, row.LeftOutReason); reason != "" {
				held += ": " + reason
			}
		}
		parts = append(parts, held)
	}
	return strings.Join(parts, "; ")
}

func paceRoomText(room float64) string {
	switch {
	case room > 0.05:
		return fmt.Sprintf("%.0f points behind pace", room)
	case room < -0.05:
		return fmt.Sprintf("%.0f points ahead of pace", -room)
	}
	return "on pace"
}

func pickRunEndText(end PickRunEnd, before PickQuotaProvider) string {
	t := end.Tokens
	text := fmt.Sprintf("run ended %s: tokens %.0f in, %.0f out, %.0f reasoning, %.0f cache read, %.0f cache write",
		quotaClock(end.EndedAt), t.In, t.Out, t.Reasoning, t.CacheRead, t.CacheWrite)
	if end.Provider == "" {
		return text
	}
	if end.After == nil {
		return text + "; no " + end.Provider + " reading at the end"
	}
	used := map[string]float64{}
	for _, w := range before.Windows {
		used[w.ID] = w.Used
	}
	var parts []string
	for _, w := range end.After.Windows {
		if b, ok := used[w.ID]; ok {
			parts = append(parts, fmt.Sprintf("%s %.0f%% to %.0f%% used", pickWindowName(w), b, w.Used))
		} else {
			parts = append(parts, fmt.Sprintf("%s %.0f%% used at the end (no reading at the pick)", pickWindowName(w), w.Used))
		}
	}
	if len(parts) == 0 {
		return text + "; " + end.Provider + " reading at the end has no windows"
	}
	return text + "; " + end.Provider + " " + strings.Join(parts, ", ")
}

func firstNonEmptyText(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// quotaClock writes a Unix time as a UTC date and time.
func quotaClock(unix int64) string {
	return time.Unix(unix, 0).UTC().Format("2006-01-02 15:04 UTC")
}

// quotaAge writes a number of seconds as minutes or hours.
func quotaAge(seconds int64) string {
	switch {
	case seconds < 60:
		return "under a minute"
	case seconds < 2*3600:
		return fmt.Sprintf("%d min", seconds/60)
	}
	return fmt.Sprintf("%.1f h", float64(seconds)/3600)
}

// QueryPickQuota is "query.pick_quota": what the latest recorded pick of each
// named worker session read of the quota windows.
type QueryPickQuota struct {
	Sessions []string `json:"sessions"`
}

// PickQuotaView is one worker's answer: the plain words, and what they were
// written from.
type PickQuotaView struct {
	RunID  string      `json:"run_id"`
	Lines  []string    `json:"lines"`
	Read   *PickQuota  `json:"read,omitempty"`
	RunEnd *PickRunEnd `json:"run_end,omitempty"`
}

// PickQuotaResult is the answer to "query.pick_quota", by session ID.
type PickQuotaResult struct {
	Picks map[string]PickQuotaView `json:"picks"`
}
