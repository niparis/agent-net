package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/niparis/agent-net/internal/protocol"
)

// harnessSummary is the HARNESSES column of `nodes`: names, "none" when the
// node reported an empty list, "unknown" when it never reported (older daemon).
func harnessSummary(n protocol.NodeView) string {
	if n.Harnesses == nil {
		return "unknown"
	}
	if len(n.Harnesses) == 0 {
		return "none"
	}
	var names []string
	for _, h := range n.Harnesses {
		names = append(names, h.Name)
	}
	return strings.Join(names, ",")
}

// modelSummary says how many models a harness lists and from which providers.
func modelSummary(models []string) string {
	if len(models) == 0 {
		return "-"
	}
	seen := map[string]bool{}
	var providers []string
	for _, m := range models {
		p, _, _ := strings.Cut(m, "/")
		if !seen[p] {
			seen[p] = true
			providers = append(providers, p)
		}
	}
	sort.Strings(providers)
	more := ""
	if len(providers) > 8 {
		providers, more = providers[:8], ", ..."
	}
	return fmt.Sprintf("%d (%s%s)", len(models), strings.Join(providers, ", "), more)
}

// printHarnessDetails is the --wide part of `nodes`: one line per harness.
func printHarnessDetails(w io.Writer, nodes []protocol.NodeView) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "\nHOST\tHARNESS\tVERSION\tMODELS\tPATH")
	for _, n := range nodes {
		if n.Harnesses == nil {
			fmt.Fprintf(tw, "%s\t-\t-\t-\tnot reported (older agent-net)\n", n.Hostname)
		}
		for _, h := range n.Harnesses {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", n.Hostname, h.Name, dash(h.Version), modelSummary(h.Models), h.Path)
		}
	}
	tw.Flush()
}
