package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

// stat counts the launched briefs of one group and their outcome signs.
type stat struct {
	N       int
	Fix     int
	Early   int
	Rated   int
	Low     int
	Defect  int
	NotDone int
	Bad     int
}

func (s *stat) add(o outcome) {
	s.N++
	if o.FixRounds > 0 {
		s.Fix++
	}
	if o.EarlyQuit {
		s.Early++
	}
	if o.Rated {
		s.Rated++
		if o.LowRating() {
			s.Low++
		}
	}
	if o.Defects > 0 {
		s.Defect++
	}
	if o.NotComplete() {
		s.NotDone++
	}
	if o.Bad() {
		s.Bad++
	}
}

// refusedBy reports whether the live check, or a rule of it, refuses the brief.
// An empty rule means any rule.
func refusedBy(list []string, rule string) bool {
	if rule == "" {
		return len(list) > 0
	}
	for _, r := range list {
		if r == rule {
			return true
		}
	}
	return false
}

// split returns the launched briefs refused and passed by a predicate.
func split(briefs []scored, refused func(scored) bool) (ref, pass stat) {
	for _, s := range briefs {
		if !s.Outcome.Launched {
			continue
		}
		if refused(s) {
			ref.add(s.Outcome)
		} else {
			pass.add(s.Outcome)
		}
	}
	return ref, pass
}

// stratifiedBy is the Mantel-Haenszel risk difference of the bad outcome,
// refused minus passed, with one stratum per key. Only strata with both
// refused and passed launched briefs add to it. Positive means the rule's
// refusals go with more bad outcomes, which is what a useful refusal predicts.
func stratifiedBy(briefs []scored, refused func(scored) bool, key func(scored) string) (rd float64, strata, nRef, nPass int) {
	type cell struct{ n1, a, n0, c int }
	byKey := map[string]*cell{}
	for _, s := range briefs {
		if !s.Outcome.Launched {
			continue
		}
		cl := byKey[key(s)]
		if cl == nil {
			cl = &cell{}
			byKey[key(s)] = cl
		}
		if refused(s) {
			cl.n1++
			if s.Outcome.Bad() {
				cl.a++
			}
		} else {
			cl.n0++
			if s.Outcome.Bad() {
				cl.c++
			}
		}
	}
	// Sorted keys keep the floating-point sum in the same order on every run.
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var num, den float64
	for _, k := range keys {
		cl := byKey[k]
		nRef += cl.n1
		nPass += cl.n0
		if cl.n1 == 0 || cl.n0 == 0 {
			continue
		}
		strata++
		w := float64(cl.n1) * float64(cl.n0) / float64(cl.n1+cl.n0)
		num += w * (float64(cl.a)/float64(cl.n1) - float64(cl.c)/float64(cl.n0))
		den += w
	}
	if den == 0 {
		return 0, strata, nRef, nPass
	}
	return num / den, strata, nRef, nPass
}

// zScore is the two-proportion z value for x1/n1 against x2/n2.
func zScore(x1, n1, x2, n2 int) float64 {
	if n1 == 0 || n2 == 0 {
		return 0
	}
	p1 := float64(x1) / float64(n1)
	p2 := float64(x2) / float64(n2)
	p := float64(x1+x2) / float64(n1+n2)
	se := math.Sqrt(p * (1 - p) * (1/float64(n1) + 1/float64(n2)))
	if se == 0 {
		return 0
	}
	return (p1 - p2) / se
}

func rate(num, den int) string {
	if den == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%% (%d/%d)", 100*float64(num)/float64(den), num, den)
}

func pct(num, den int) string {
	if den == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(num)/float64(den))
}

// ruleLabel names a rule row. The empty rule is the overall row.
func ruleLabel(rule string) string {
	if rule == "" {
		return "**Any rule**"
	}
	return ruleItem[rule]
}

// writeCSV writes one row per brief. It holds every value the report uses, so
// a reader can check any number in the report.
func writeCSV(w io.Writer, briefs []scored) error {
	cw := csv.NewWriter(w)
	header := []string{"brief_id", "session", "sender", "sent_at_unix_ns", "launched", "runs", "fix_rounds",
		"continue_rounds", "early_quit", "final_outcome", "worker_complete_last", "struggle_total",
		"rated", "score", "defects", "max_severity", "bad", "strict_failed", "loose_failed", "unmapped_failures", "difficulty", "opening_line"}
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, s := range briefs {
		o := s.Outcome
		complete := ""
		if o.Complete.Valid {
			complete = strconv.FormatInt(o.Complete.Int64, 10)
		}
		row := []string{
			s.ID, s.Session, s.Sender, strconv.FormatInt(s.SentAt, 10), yesNo(o.Launched),
			strconv.Itoa(o.Runs), strconv.Itoa(o.FixRounds), strconv.Itoa(o.Continues), yesNo(o.EarlyQuit),
			o.Final, complete, strconv.Itoa(o.Struggle), yesNo(o.Rated), strconv.Itoa(o.Score),
			strconv.Itoa(o.Defects), strconv.Itoa(o.MaxSeverity), yesNo(o.Bad()),
			strings.Join(s.Strict, " "), strings.Join(s.Loose, " "), strconv.Itoa(len(s.Unmapped)),
			o.Difficulty, openingLine(s.Task),
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// openingLine returns the first non-empty line of a task, cut to 160 runes.
func openingLine(task string) string {
	line := firstLines(task, 1)
	r := []rune(strings.TrimSpace(line))
	if len(r) > 160 {
		return string(r[:160]) + "..."
	}
	return string(r)
}

// renderReport writes the markdown report. Every table names its denominator.
func renderReport(res *replay, source string) string {
	var b strings.Builder
	launched := 0
	for _, s := range res.Briefs {
		if s.Outcome.Launched {
			launched++
		}
	}
	fmt.Fprintf(&b, "# Brief check replay\n\n")
	fmt.Fprintf(&b, "Source: `%s` (a copy, opened read-only). Overlap is not replayed.\n\n", source)

	fmt.Fprintf(&b, "## Totals\n\n")
	fmt.Fprintf(&b, "| Item | Count |\n|---|---|\n")
	fmt.Fprintf(&b, "| Brief messages replayed | %d |\n", len(res.Briefs))
	fmt.Fprintf(&b, "| Brief messages without the task marker (skipped) | %d |\n", res.NoMarker)
	fmt.Fprintf(&b, "| Briefs with a launched worker (joined to runs) | %d |\n", launched)
	fmt.Fprintf(&b, "| Briefs never launched (no runs, no outcome) | %d |\n", len(res.Briefs)-launched)
	fmt.Fprintf(&b, "| Worker sessions with runs in the copy | %d |\n", res.Runs)
	fmt.Fprintf(&b, "| Launched briefs with a rating | %d |\n", countRated(res.Briefs))
	fmt.Fprintf(&b, "| Launched briefs with a defect found later | %d |\n", countDefected(res.Briefs))
	fmt.Fprintf(&b, "| Live refusal rows (failure lines only, not replayed) | %d |\n", res.Rejections.Rows)
	fmt.Fprintf(&b, "| Failure lines from the live check that match no rule | %d |\n\n", countUnmapped(res.Briefs))

	fmt.Fprintf(&b, "Bad outcome = the last run did not complete, or an early quit, or a fix round, or a rating of 2 or less, or a defect found later. Rates are over launched briefs only. Refused = the live check fails the rule.\n\n")

	fmt.Fprintf(&b, "## Live check: refused share and outcomes\n\n")
	fmt.Fprintf(&b, "| Rule | Refused, all briefs | Refused, launched | Passed, launched | Bad, refused | Bad, passed | z |\n")
	fmt.Fprintf(&b, "|---|---|---|---|---|---|---|\n")
	for _, rule := range append(append([]string{}, rules...), "") {
		allRefused := 0
		for _, s := range res.Briefs {
			if refusedBy(s.Strict, rule) {
				allRefused++
			}
		}
		ref, pass := split(res.Briefs, func(s scored) bool { return refusedBy(s.Strict, rule) })
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %s | %s | %.2f |\n",
			ruleLabel(rule), rate(allRefused, len(res.Briefs)), ref.N, pass.N,
			rate(ref.Bad, ref.N), rate(pass.Bad, pass.N), zScore(ref.Bad, ref.N, pass.Bad, pass.N))
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "### Outcome signs, refused against passed (launched briefs)\n\n")
	fmt.Fprintf(&b, "Passed = not refused by that rule. The any-rule row compares against briefs that pass every rule.\n\n")
	fmt.Fprintf(&b, "| Rule | Fix rounds | Early quits | Low ratings (of rated) | Defects found later | Last run not complete |\n")
	fmt.Fprintf(&b, "|---|---|---|---|---|---|\n")
	for _, rule := range append(append([]string{}, rules...), "") {
		ref, pass := split(res.Briefs, func(s scored) bool { return refusedBy(s.Strict, rule) })
		fmt.Fprintf(&b, "| %s | %s vs %s | %s vs %s | %s vs %s | %s vs %s | %s vs %s |\n",
			ruleLabel(rule),
			rate(ref.Fix, ref.N), rate(pass.Fix, pass.N),
			rate(ref.Early, ref.N), rate(pass.Early, pass.N),
			rate(ref.Low, ref.Rated), rate(pass.Low, pass.Rated),
			rate(ref.Defect, ref.N), rate(pass.Defect, pass.N),
			rate(ref.NotDone, ref.N), rate(pass.NotDone, pass.N))
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "## Looser candidate matchers\n\n")
	fmt.Fprintf(&b, "Released = refused by the live rule, passed by its looser matcher. Bad rate of released briefs is set against the briefs that pass the live rule. A rule that releases many briefs with the same bad rate as the passed group adds refusals without signal.\n\n")
	fmt.Fprintf(&b, "| Rule | Refused, launched | Still refused by looser matcher | Released | Bad, released | Bad, passed live rule | Difference | z |\n")
	fmt.Fprintf(&b, "|---|---|---|---|---|---|---|---|\n")
	for _, rule := range rules {
		ref, pass := split(res.Briefs, func(s scored) bool { return refusedBy(s.Strict, rule) })
		var released stat
		for _, s := range res.Briefs {
			if s.Outcome.Launched && refusedBy(s.Strict, rule) && !refusedBy(s.Loose, rule) {
				released.add(s.Outcome)
			}
		}
		still := ref.N - released.N
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %s | %s | %s | %.2f |\n",
			ruleLabel(rule), ref.N, still, released.N,
			rate(released.Bad, released.N), rate(pass.Bad, pass.N),
			pct(released.Bad, released.N)+" vs "+pct(pass.Bad, pass.N),
			zScore(released.Bad, released.N, pass.Bad, pass.N))
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "## Robustness: first and second half of the period\n\n")
	fmt.Fprintf(&b, "The briefs are split at the median send time. A rule whose sign flips between halves is not trusted.\n\n")
	fmt.Fprintf(&b, "| Rule | First half: bad, refused vs passed | Second half: bad, refused vs passed |\n|---|---|---|\n")
	first, second := halves(res.Briefs)
	for _, rule := range append(append([]string{}, rules...), "") {
		pred := func(s scored) bool { return refusedBy(s.Strict, rule) }
		r1, p1 := split(first, pred)
		r2, p2 := split(second, pred)
		fmt.Fprintf(&b, "| %s | %s vs %s | %s vs %s |\n", ruleLabel(rule),
			pct(r1.Bad, r1.N), pct(p1.Bad, p1.N), pct(r2.Bad, r2.N), pct(p2.Bad, p2.N))
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "## Robustness: within each caller\n\n")
	fmt.Fprintf(&b, "Most briefs come from one caller, and the lead session sends most of the passing ones. The crude split above mixes callers and task difficulty. These columns compare refused and passed briefs inside each caller, and inside each difficulty level, then pool the difference (Mantel-Haenszel risk difference in points, bad outcome). Positive means refusals go with more bad outcomes.\n\n")
	fmt.Fprintf(&b, "| Rule | Within caller (points) | Callers used | Within difficulty (points) | Difficulty levels used |\n|---|---|---|---|---|\n")
	for _, rule := range append(append([]string{}, rules...), "") {
		refused := func(s scored) bool { return refusedBy(s.Strict, rule) }
		byCaller, nCaller, _, _ := stratifiedBy(res.Briefs, refused, func(s scored) string { return s.Sender })
		byDifficulty, nDifficulty, _, _ := stratifiedBy(res.Briefs, refused, func(s scored) string { return s.Outcome.Difficulty })
		fmt.Fprintf(&b, "| %s | %+.1f | %d | %+.1f | %d |\n", ruleLabel(rule), 100*byCaller, nCaller, 100*byDifficulty, nDifficulty)
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "## Live refusal rows already stored\n\n")
	fmt.Fprintf(&b, "These %d rows keep the failure lines only. Their brief text is not stored, so they are not replayed.\n\n", res.Rejections.Rows)
	fmt.Fprintf(&b, "| Rule | Failure lines |\n|---|---|\n")
	for _, rule := range rules {
		fmt.Fprintf(&b, "| %s | %d |\n", ruleLabel(rule), res.Rejections.ByRule[rule])
	}
	fmt.Fprintf(&b, "| Overlap or other lines (not replayed) | %d |\n\n", res.Rejections.Unmapped)

	good, bad := pickExamples(res.Briefs)
	fmt.Fprintf(&b, "## Examples: refused, and went well\n\n")
	writeExamples(&b, good)
	fmt.Fprintf(&b, "## Examples: refused, and went badly\n\n")
	writeExamples(&b, bad)
	return b.String()
}

func countRated(briefs []scored) int {
	n := 0
	for _, s := range briefs {
		if s.Outcome.Launched && s.Outcome.Rated {
			n++
		}
	}
	return n
}

func countDefected(briefs []scored) int {
	n := 0
	for _, s := range briefs {
		if s.Outcome.Launched && s.Outcome.Defects > 0 {
			n++
		}
	}
	return n
}

func countUnmapped(briefs []scored) int {
	n := 0
	for _, s := range briefs {
		n += len(s.Unmapped)
	}
	return n
}

// halves splits the launched briefs at the median send time.
func halves(briefs []scored) (first, second []scored) {
	var launched []scored
	for _, s := range briefs {
		if s.Outcome.Launched {
			launched = append(launched, s)
		}
	}
	mid := len(launched) / 2
	return launched[:mid], launched[mid:]
}

// isGood is a launched brief that finished with no sign of trouble.
func isGood(o outcome) bool {
	return o.Launched && o.Final == "complete" && o.FixRounds == 0 && !o.EarlyQuit &&
		o.Defects == 0 && (!o.Rated || o.Score >= 4)
}

// isStrongBad is a launched brief that did not finish, quit early, or got a low rating.
func isStrongBad(o outcome) bool {
	return o.Launched && (o.NotComplete() || o.EarlyQuit || o.LowRating())
}

// pickExamples returns the three newest refused briefs that went well and the
// three newest refused briefs that went badly. The order is deterministic.
func pickExamples(briefs []scored) (good, bad []scored) {
	for i := len(briefs) - 1; i >= 0; i-- {
		s := briefs[i]
		if len(s.Strict) == 0 {
			continue
		}
		if isGood(s.Outcome) && len(good) < 3 {
			good = append(good, s)
		}
		if isStrongBad(s.Outcome) && len(bad) < 3 {
			bad = append(bad, s)
		}
	}
	return good, bad
}

func writeExamples(b *strings.Builder, list []scored) {
	if len(list) == 0 {
		fmt.Fprintf(b, "None.\n\n")
		return
	}
	for _, s := range list {
		o := s.Outcome
		fmt.Fprintf(b, "- Session `%s`, caller `%s`. Refused by: %s. Last run: %s, fix rounds %d, early quit %s, rating %s, defects %d.\n",
			s.Session, s.Sender, strings.Join(ruleNames(s.Strict), ", "), o.Final, o.FixRounds,
			yesNo(o.EarlyQuit), ratingText(o), o.Defects)
		fmt.Fprintf(b, "  > %s\n", openingLine(s.Task))
	}
	fmt.Fprintf(b, "\n")
}

func ruleNames(list []string) []string {
	out := make([]string, 0, len(list))
	for _, r := range list {
		out = append(out, ruleItem[r])
	}
	return out
}

func ratingText(o outcome) string {
	if !o.Rated {
		return "none"
	}
	return strconv.Itoa(o.Score)
}
