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

	"github.com/niparis/agent-net/internal/protocol"
)

func main() {
	db := flag.String("db", "", "path to a read-only copy of the gateway store (required)")
	alphas := flag.String("alphas", "0,2,4,6,8,10", "comma separated alpha values for option A")
	flag.Parse()
	if *db == "" {
		fmt.Fprintln(os.Stderr, "pickerreplay: -db is required; pass a copy of the gateway store, never the live one")
		os.Exit(2)
	}
	var list []float64
	for _, part := range strings.Split(*alphas, ",") {
		v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "pickerreplay: bad alpha %q\n", part)
			os.Exit(2)
		}
		list = append(list, v)
	}
	s, err := OpenSnapshot(*db)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pickerreplay:", err)
		os.Exit(1)
	}
	report := RunReplay(s, list)
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
	fmt.Printf("| method | picks | no pick | mean quality (today's score) | mean clean p | mean expected cost | mean adjusted cost | fallbacks |\n")
	fmt.Printf("|---|---|---|---|---|---|---|---|\n")
	all := []*summary{r.Today}
	all = append(all, r.OptionsA...)
	all = append(all, r.Floors...)
	for _, m := range all {
		p, cost, adj, qual := m.averages()
		fmt.Printf("| %s | %d | %d | %.2f | %.2f | $%.2f | $%.2f | %d |\n", m.Name, m.Picks, m.NoPick, qual, p, cost, adj, m.Fallback)
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
		fmt.Printf("\n| method | winner | quality | clean p | run cost | expected cost | adjusted | rank |\n")
		fmt.Printf("|---|---|---|---|---|---|---|---|\n")
		for _, v := range cs.Variants {
			fmt.Printf("| %s | %s | %.2f | %.2f | $%.2f | $%.2f | $%.2f | %.2f |\n", v.Variant, v.Winner, v.Quality, v.P, v.Cost, v.Expected, v.Adjusted, v.Rank)
		}
		fmt.Printf("\nThe two profiles the owner compared, under each method:\n\n")
		fmt.Printf("| method | profile | quality | clean p | run cost | expected cost | adjusted | rank |\n")
		fmt.Printf("|---|---|---|---|---|---|---|---|\n")
		for _, v := range cs.Variants {
			for _, e := range v.Cands {
				fmt.Printf("| %s | %s | %.2f | %.2f | $%.2f | $%.2f | $%.2f | %.2f |\n",
					v.Variant, e.Name, e.Quality, e.P, e.Cost, e.Expected, e.Adjusted, e.Rank)
			}
		}
		fmt.Printf("\nCandidates in the rebuilt table:\n\n")
		fmt.Printf("| name | model | quality | n | allowed | clean p | run cost | rescue | expected | adjusted |\n")
		fmt.Printf("|---|---|---|---|---|---|---|---|---|---|\n")
		rows := append([]*candEval(nil), cs.Today...)
		sort.Slice(rows, func(i, j int) bool { return rows[i].adjusted < rows[j].adjusted })
		for _, e := range rows {
			fmt.Printf("| %s | %s | %.2f | %d | %t | %.2f | $%.2f | $%.2f | $%.2f | $%.2f |\n",
				e.row.Name, protocol.PickerModelDisplay(e.key), e.cell.Score, e.cell.N, e.allowed, e.pass, e.cost, e.rescue, e.expected, e.adjusted)
		}
	}
}
