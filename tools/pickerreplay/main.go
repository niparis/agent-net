// Command pickerreplay replays the worker picker against a read-only snapshot
// of the gateway store. It runs today's method and the opt-in rule at several
// alpha values and clean-rate floors, and prints the tables decision 0173 reports. It is not
// wired into the daemon or the gateway.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

func main() {
	db := flag.String("db", "", "path to a read-only copy of the gateway store (required)")
	alphas := flag.String("alphas", "", "comma separated alpha values for option A; empty disables them")
	floors := flag.String("floors", "", "comma separated floor sets A, B and C; empty disables them")
	exhausted := flag.String("exhausted", "opencode-go", "comma separated quota providers that are exhausted; they do not compete in any method (empty keeps every recorded candidate)")
	protected := flag.String("protected", "alibaba-token-plan", "comma separated protected providers; the live picker never starts on them, so no method competes with them (empty keeps every recorded candidate)")
	fallback := flag.Bool("fallback", false, "replay the fallback draw by expected cost over the recorded picks that no candidate qualified for, and print it instead of the picker report")
	flag.Parse()
	if *db == "" {
		fmt.Fprintln(os.Stderr, "pickerreplay: -db is required; pass a copy of the gateway store, never the live one")
		os.Exit(2)
	}
	var list []float64
	for _, part := range strings.Split(*alphas, ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "pickerreplay: bad alpha %q\n", part)
			os.Exit(2)
		}
		list = append(list, v)
	}
	selected, err := parseFloorSets(*floors)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pickerreplay:", err)
		os.Exit(2)
	}
	s, err := OpenSnapshot(*db)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pickerreplay:", err)
		os.Exit(1)
	}
	s.Exhausted = map[string]bool{}
	for _, name := range strings.Split(*exhausted, ",") {
		if name = strings.TrimSpace(name); name != "" {
			s.Exhausted[name] = true
		}
	}
	s.Protected = map[string]bool{}
	for _, name := range strings.Split(*protected, ",") {
		if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
			s.Protected[name] = true
		}
	}
	if *fallback {
		printFallback(RunFallback(s))
		return
	}
	report := RunReplay(s, list, selected)
	printReport(report)
}

func day(sec int64) string {
	if sec == 0 {
		return "-"
	}
	return time.Unix(sec, 0).UTC().Format("2006-01-02")
}

func printReport(r *Report) {
	s := r.Snapshot
	fmt.Printf("# Picker replay\n\n")
	fmt.Printf("Snapshot tasks: %d. Recorded picks: %d. Prices: %d models.\n", len(s.Tasks), len(s.Picks), s.PriceCount)
	if len(s.Picks) > 0 {
		fmt.Printf("Picks from %s to %s.\n", day(s.Picks[0].At), day(s.Picks[len(s.Picks)-1].At))
	}

	fmt.Printf("\n## Replay check: today's rule against the recorded pick\n\n")
	c := r.Check
	fmt.Printf("Match: %d/%d (%.0f%%).\n", c.Match, c.Picks, 100*c.matchRate())
	if c.CellRows > 0 {
		fmt.Printf("Rebuilt cell score within 0.01 of the recorded one: %d/%d (%.0f%%).\n", c.CellMatch, c.CellRows, 100*float64(c.CellMatch)/float64(c.CellRows))
	}
	if c.CleanRows > 0 {
		fmt.Printf("Rebuilt clean share within 0.01: %d/%d (%.0f%%).\n", c.CleanMatch, c.CleanRows, 100*float64(c.CleanMatch)/float64(c.CleanRows))
	}
	if c.CostRows > 0 {
		fmt.Printf("Rebuilt run cost within $0.01: %d/%d (%.0f%%).\n", c.CostMatch, c.CostRows, 100*float64(c.CostMatch)/float64(c.CostRows))
	}
	if len(c.Mismatches) > 0 {
		fmt.Printf("\nMismatches (recorded pick, then the rebuilt rule's pick):\n\n")
		show := min(12, len(c.Mismatches))
		for _, m := range c.Mismatches[:show] {
			fmt.Printf("- D %.2f: recorded %s, rebuilt %s. %s\n", m.D, m.Recorded, m.Ours, m.Reason)
		}
		if len(c.Mismatches) > show {
			fmt.Printf("- ... %d more\n", len(c.Mismatches)-show)
		}
	}

	fmt.Printf("\n## What each method picks\n\n")
	fmt.Printf("Every row prices its winner from the V2 table: run cost, clean p, and cost per accepted result (run cost / clean p). The last two columns are the row's own rule and its mean cost under that rule.\n\n")
	fmt.Printf("| method | picks | no pick | mean quality (today's score) | mean clean p | mean run cost | mean cost per accepted | mean adjusted cost per accepted | fallbacks | own rule | mean own-rule cost |\n")
	fmt.Printf("|---|---|---|---|---|---|---|---|---|---|---|\n")
	all := []*summary{r.Today, r.V2, r.V2Ceiling}
	all = append(all, r.OptionsA...)
	all = append(all, r.Floors...)
	for _, m := range all {
		a := m.averages()
		fmt.Printf("| %s | %d | %d | %.2f | %.2f | $%.2f | $%.2f | $%.2f | %d | %s | $%.2f |\n",
			m.Name, m.Picks, m.NoPick, a.Qual, a.P, a.Run, a.PerAcc, a.Adj, m.Fallback, m.Rule, a.Own)
	}
	fmt.Printf("\n## Picks with no proven candidate left\n\n")
	if len(s.Protected) > 0 {
		var names []string
		for name := range s.Protected {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Printf("Protected providers, left out of every method for every pick (the live picker never starts on them): %s.\n\n", strings.Join(names, ", "))
	}
	if len(s.Exhausted) > 0 {
		var names []string
		for name := range s.Exhausted {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Printf("Exhausted providers, left out of every method for every pick (the snapshot keeps no quota reading from the time of a pick): %s.\n\n", strings.Join(names, ", "))
	}
	fmt.Printf("A pick with no qualified candidate falls back: the row then uses today's pick for its cost. None allowed means no candidate was allowed at all, so the live picker uses the level rule.\n\n")
	fmt.Printf("| level | picks | V2 no qualified | V2 none allowed | V2 + ceiling no qualified | V2 + ceiling none allowed |\n")
	fmt.Printf("|---|---|---|---|---|---|\n")
	for _, level := range []string{"trivial", "simple", "normal", "hard", "very_complex"} {
		if r.V2.PicksByLevel[level] == 0 {
			continue
		}
		fmt.Printf("| %s | %d | %d | %d | %d | %d |\n", level, r.V2.PicksByLevel[level],
			r.V2.NoQualified[level], r.V2.NoneAllowed[level], r.V2Ceiling.NoQualified[level], r.V2Ceiling.NoneAllowed[level])
	}
	fmt.Printf("\nWhat the ceiling rule changed: it cut a candidate V2 allowed in %d of %d picks, and changed the winner in %d.\n", r.CeilingPicksCut, r.V2.Picks, r.CeilingMoved)
	var cuts []string
	for name, n := range r.CeilingCuts {
		cuts = append(cuts, fmt.Sprintf("%s %d", name, n))
	}
	sort.Strings(cuts)
	if len(cuts) > 0 {
		fmt.Printf("\nCandidates cut (picks): %s.\n", strings.Join(cuts, ", "))
	}
	fmt.Printf("\nModels picked by difficulty level:\n\n")
	for _, m := range all {
		for _, level := range []string{"trivial", "simple", "normal", "hard", "very_complex"} {
			counts := m.ByLevel[level]
			if len(counts) == 0 {
				continue
			}
			var names []string
			for name := range counts {
				names = append(names, name)
			}
			sort.Strings(names)
			var parts []string
			for _, name := range names {
				parts = append(parts, fmt.Sprintf("%s %d", name, counts[name]))
			}
			fmt.Printf("- %s / %s: %s\n", m.Name, level, strings.Join(parts, ", "))
		}
	}

	if cs := r.Case; cs != nil {
		fmt.Printf("\n## Case D %.2f\n\n", cs.Pick.D)
		fmt.Printf("Session %s, bucket %.2f-%.2f, recorded pick: ", cs.Pick.Session, cs.Pick.Rows[0].BucketLo, cs.Pick.Rows[0].BucketHi)
		for _, row := range cs.Pick.Rows {
			if row.Chosen {
				fmt.Printf("%s\n", row.Name)
			}
		}
		if len(cs.Exhausted) > 0 {
			fmt.Printf("Left out as exhausted or protected: %s.\n", strings.Join(cs.Exhausted, ", "))
		}
		fmt.Printf("\n| method | winner | quality | clean p | run cost | cost per accepted | adjusted per accepted | own-rule cost | rank |\n")
		fmt.Printf("|---|---|---|---|---|---|---|---|---|\n")
		for _, v := range cs.Variants {
			fmt.Printf("| %s | %s | %.2f | %.2f | $%.2f | $%.2f | $%.2f | $%.2f | %.2f |\n",
				v.Variant, v.Winner, v.Quality, v.View.Clean, v.View.Run, v.View.PerAccepted, v.View.Adjusted, v.Own, v.Rank)
		}
		fmt.Printf("\nThe profiles the owner compared, and the winner, under each method:\n\n")
		fmt.Printf("| method | profile | quality | clean p | run cost | cost per accepted | adjusted per accepted |\n")
		fmt.Printf("|---|---|---|---|---|---|---|\n")
		for _, v := range cs.Variants {
			for _, e := range v.Cands {
				fmt.Printf("| %s | %s | %.2f | %.2f | $%.2f | $%.2f | $%.2f |\n",
					v.Variant, e.Name, e.Quality, e.View.Clean, e.View.Run, e.View.PerAccepted, e.View.Adjusted)
			}
		}
		fmt.Printf("\nCandidates in the rebuilt table, sorted by adjusted cost per accepted (quality and n from today's table):\n\n")
		fmt.Printf("| name | model | quality | n | allowed | clean p | run cost | cost per accepted | pace | adjusted per accepted |\n")
		fmt.Printf("|---|---|---|---|---|---|---|---|---|---|\n")
		rows := append([]CaseRoster(nil), cs.Roster...)
		sort.Slice(rows, func(i, j int) bool { return rows[i].View.Adjusted < rows[j].View.Adjusted })
		for _, e := range rows {
			fmt.Printf("| %s | %s | %.2f | %d | %t | %.2f | $%.2f | $%.2f | %.2f | $%.2f |\n",
				e.Name, e.Model, e.Quality, e.N, e.Allowed, e.View.Clean, e.View.Run, e.View.PerAccepted, e.Pace, e.View.Adjusted)
		}
	}
}
