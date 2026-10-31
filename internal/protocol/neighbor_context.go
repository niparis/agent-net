package protocol

import (
	"fmt"
	"sort"
	"strings"
)

// NeighborCharters describes the charters of directly connected nodes.
// NeighborFieldChars is how much of a neighbour's accepts and refuses a
// connected agent sees. The charter guide quotes it, so the two cannot drift.
const NeighborFieldChars = 40

func NeighborCharters(g Graph, name string) string {
	type candidate struct {
		name     string
		priority int
	}
	best := map[string]int{}
	for _, e := range g.Edges {
		if e.From == "*" {
			continue
		}
		other := ""
		if e.From == name {
			other = e.To
		} else if e.To == name {
			other = e.From
		}
		if other == "" || other == name {
			continue
		}
		n := nodeNamed(g, other)
		if n == nil || !charterSet(n.Charter) {
			continue
		}
		p := 4
		switch e.Kind {
		case "governs":
			p = 1
		case "deploys":
			p = 2
		case "depends":
			p = 3
		}
		if e.Rule != "" && e.Rule != "normal" {
			p = 0
		}
		if old, ok := best[other]; !ok || p < old {
			best[other] = p
		}
	}
	var candidates []candidate
	for n, p := range best {
		candidates = append(candidates, candidate{n, p})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].priority != candidates[j].priority {
			return candidates[i].priority < candidates[j].priority
		}
		return candidates[i].name < candidates[j].name
	})
	var lines []string
	for _, c := range candidates {
		n := nodeNamed(g, c.name)
		short := func(s string, limit int) string {
			s = strings.Join(strings.Fields(s), " ")
			r := []rune(s)
			if len(r) > limit {
				return string(r[:limit-1]) + "…"
			}
			return s
		}
		line := fmt.Sprintf("%s: accepts %s; refuses %s; ask it for those", short(c.name, 40), short(n.Charter.Accepts, NeighborFieldChars), short(n.Charter.Refuses, NeighborFieldChars))
		if len(strings.Join(append(lines, line), "\n")) > 1500 {
			break
		}
		lines = append(lines, line)
		if len(lines) == 8 {
			break
		}
	}
	return strings.Join(lines, "\n")
}
