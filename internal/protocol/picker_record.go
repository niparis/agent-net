package protocol

import (
	"fmt"
	"sort"
	"strings"
)

// Rule names of a pick: the cell rule (decision 0162) or the level rule that
// the picker falls back to when no cell rule pick is possible.
const (
	PickRuleCell  = "cell"
	PickRuleLevel = "level"
)

// PickCandidate is one candidate's row in one automatic pick: the terms the
// picker read for it and what the pick did with it. A pick has one row per
// candidate, in roster order. The gateway stores the rows with the start, and
// the picker note of the pick is written from them (PickNote).
type PickCandidate struct {
	Name     string `json:"name"`
	Harness  string `json:"harness"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model"`
	Effort   string `json:"effort,omitempty"`
	// Rule is PickRuleCell or PickRuleLevel.
	Rule string `json:"rule"`
	// BucketLo and BucketHi are the D bucket the pick read.
	BucketLo float64 `json:"bucket_lo"`
	BucketHi float64 `json:"bucket_hi"`
	// Fallback is why the level rule made the pick, with its reason in
	// parentheses. Empty for a cell pick.
	Fallback string `json:"fallback,omitempty"`
	// Cell is the candidate's cell in the bucket: its shrunk score, the 90
	// percent interval and its task count. It is zero for a level pick.
	Cell PickerCell `json:"cell"`
	// Allowed is true when the rule allowed the candidate, Qualified when it is
	// in the qualifying set, Prior when the benchmark allowed it, Best when it
	// has the best cell and Chosen when the start tried it first.
	Allowed   bool `json:"allowed"`
	Qualified bool `json:"qualified"`
	Prior     bool `json:"prior"`
	Best      bool `json:"best"`
	Chosen    bool `json:"chosen"`
	// Winner is true for the candidate with the lowest adjusted cost in the
	// qualifying set. An exploration pick can choose another candidate.
	Winner bool `json:"winner"`
	// LeftOutReason is why the rule left the candidate out. Empty when allowed.
	LeftOutReason string `json:"left_out_reason,omitempty"`
	// CostKnown is false when the candidate had no run and no price. Its cost
	// is then the average of the known costs, or 1 when none is known.
	// CostRecorded is false when the cost is a price times an average run, and
	// PriceUnknown when no price was listed.
	CostKnown    bool `json:"cost_known"`
	CostRecorded bool `json:"cost_recorded"`
	PriceUnknown bool `json:"price_unknown"`
	// CostPerRun is the cost of one run. PClean is p, the clean share of the
	// candidate's tasks. RescueCost is the run cost of the next dearer model.
	// ExpectedCost is CostPerRun + (1 - PClean) x RescueCost.
	CostPerRun   float64 `json:"cost_per_run"`
	PClean       float64 `json:"p_clean"`
	RescueCost   float64 `json:"rescue_cost"`
	ExpectedCost float64 `json:"expected_cost"`
	// PaceRoom is the quota room in percentage points, nil when no window has a
	// reading. PaceFactor is the factor the room gave (1 without a reading).
	PaceRoom   *float64 `json:"pace_room,omitempty"`
	PaceFactor float64  `json:"pace_factor"`
	// AdjustedCost is ExpectedCost over PaceFactor. The winner has the lowest.
	AdjustedCost float64 `json:"adjusted_cost"`
	// Weight is the rotation weight the candidate had in the pick's rotation.
	// It is zero when the cost rule decided without a rotation.
	Weight float64 `json:"weight"`
	// Probability is the candidate's share of the rotation: its weight over the
	// total weight it rotates with. A cost pick has one candidate in its rotation,
	// so its winner has 1 and the rest 0. On an exploration pick the winner is
	// not in the rotation and has 0.
	Probability float64 `json:"probability"`
	// SkippedReason is why the gate refused the candidate before it granted one.
	// The gateway fills it; the picker does not know the gate.
	SkippedReason string `json:"skipped_reason,omitempty"`
	// Explore is true for an exploration pick, and the credit fields hold the
	// exploration credit before and after the pick. They are nil when the pick
	// had no thin cell, so the credit did not move.
	Explore             bool     `json:"explore"`
	ExploreCreditBefore *float64 `json:"explore_credit_before,omitempty"`
	ExploreCreditAfter  *float64 `json:"explore_credit_after,omitempty"`
	// Margin is the runner-up's adjusted cost over the winner's. It is set on
	// the winner only, and only when another candidate qualified.
	Margin *float64 `json:"margin,omitempty"`
}

// PickNote writes the picker note of a pick from its rows. A level pick names
// its fallback. A cell pick names the chosen candidate, then either the
// qualifying set with the thin cells that were open to exploration, or the
// allowed set when none qualified. Every candidate left out is named with its
// reason. exploreShare is the setting the note quotes for the thin cells.
func PickNote(rows []PickCandidate, exploreShare float64) string {
	if len(rows) == 0 {
		return ""
	}
	header := fmt.Sprintf("bucket %.2f-%.2f", rows[0].BucketLo, rows[0].BucketHi)
	if rows[0].Rule == PickRuleLevel {
		return header + ": " + rows[0].Fallback
	}
	for _, r := range rows {
		if r.Qualified {
			return header + ": " + pickNoteQualifying(rows, exploreShare)
		}
	}
	return header + ": " + pickNoteAllowed(rows)
}

// pickNoteAllowed is the note of a cell pick made from the allowed set, with
// no qualifying candidate. At most three allowed and five left out candidates
// are named, the rest counted.
func pickNoteAllowed(rows []PickCandidate) string {
	var chosen *PickCandidate
	var allowed, left []PickCandidate
	for i := range rows {
		r := rows[i]
		switch {
		case r.Chosen:
			chosen = &rows[i]
		case r.LeftOutReason != "":
			left = append(left, r)
		default:
			allowed = append(allowed, r)
		}
	}
	byScore := func(list []PickCandidate) {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].Cell.Score != list[j].Cell.Score {
				return list[i].Cell.Score > list[j].Cell.Score
			}
			return list[i].Name < list[j].Name
		})
	}
	byScore(allowed)
	byScore(left)

	parts := []string{}
	if chosen != nil {
		tail := ", chosen"
		if chosen.Best {
			tail = ", best"
		}
		parts = append(parts, pickItem(*chosen, tail+pickPriorTag(*chosen)))
	}
	for i, r := range allowed {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("+%d more allowed", len(allowed)-3))
			break
		}
		tail := " allowed"
		if r.Best {
			tail += ", best"
		}
		parts = append(parts, pickItem(r, tail+pickPriorTag(r)))
	}
	for i, r := range left {
		if i == 5 {
			parts = append(parts, fmt.Sprintf("+%d more left out", len(left)-5))
			break
		}
		parts = append(parts, pickItem(r, " left out ("+r.LeftOutReason+")"))
	}
	return strings.Join(parts, "; ")
}

// pickNoteQualifying is the note of a cell pick made from the qualifying set. It
// names the chosen cell, every qualifier with its expected cost, the thin cells
// that were open to the exploration share, and each candidate left out with its
// reason.
func pickNoteQualifying(rows []PickCandidate, exploreShare float64) string {
	cell := func(r PickCandidate) string {
		return strings.TrimSpace(r.Name + " " + pickerCellText(r.Cell))
	}
	var chosen *PickCandidate
	exploring := false
	for i := range rows {
		if rows[i].Chosen {
			chosen = &rows[i]
		}
		if rows[i].Explore {
			exploring = true
		}
	}
	var qualifying, thin, left []string
	for _, r := range rows {
		switch {
		case r.Qualified:
			qualifying = append(qualifying, cell(r)+pickCostText(r))
		case r.Allowed && r.Cell.N < PickerMinCellTasks && r.LeftOutReason == "":
			thin = append(thin, cell(r)+" (benchmark)"+pickCostText(r))
		case r.LeftOutReason != "":
			left = append(left, cell(r)+pickCostText(r)+" left out ("+r.LeftOutReason+")")
		}
	}
	var parts []string
	if chosen != nil {
		tag := ", lowest expected cost"
		switch {
		case exploring:
			tag = ", exploration pick"
		case chosen.Best:
			tag = ", best"
		}
		parts = append(parts, cell(*chosen)+tag)
	}
	parts = append(parts, "qualifying: "+strings.Join(qualifying, ", "))
	if len(thin) > 0 {
		parts = append(parts, fmt.Sprintf("thin cells (explore share %g): ", exploreShare)+strings.Join(thin, ", "))
	}
	if len(left) > 0 {
		parts = append(parts, strings.Join(left, "; "))
	}
	return strings.Join(parts, "; ")
}

// pickItem is one candidate in a note: its name, its cell, its cost and a tail.
func pickItem(r PickCandidate, tail string) string {
	return strings.TrimSpace(r.Name+" "+pickerCellText(r.Cell)) + pickCostText(r) + tail
}

// pickPriorTag marks a candidate the benchmark allowed for want of a record.
func pickPriorTag(r PickCandidate) string {
	if r.Prior {
		return " (benchmark)"
	}
	return ""
}

// pickCostText is the expected cost of a row for the note. Nothing when no cost
// is known; "est" when the run cost is a price times an average run.
func pickCostText(r PickCandidate) string {
	if !r.CostKnown {
		return ""
	}
	text := fmt.Sprintf(" $%.2f/run, p %.2f, $%.2f expected", r.CostPerRun, r.PClean, r.ExpectedCost)
	if !r.CostRecorded {
		text = " est" + text
	}
	if r.PriceUnknown {
		text += " (price unknown)"
	}
	return text
}

// pickName is the name a row and a note give a candidate: its display name, or
// its model without the provider prefix.
func pickName(c WorkerModelCandidate) string {
	if c.Name != "" {
		return c.Name
	}
	return PickerModelDisplay(PickerModelKey(c.Harness, c.Model))
}

// levelPickRow starts the row of one candidate of a level pick. The level rule
// reads no cell and no cost, so only the identity and the pace are set here.
func levelPickRow(c WorkerModelCandidate, room float64, measured bool) PickCandidate {
	r := PickCandidate{Name: pickName(c), Harness: c.Harness, Provider: c.Provider, Model: c.Model, Effort: c.Effort,
		Rule: PickRuleLevel, PaceFactor: quotaPaceFactor(room, measured)}
	if measured {
		r.PaceRoom = &room
	}
	return r
}
