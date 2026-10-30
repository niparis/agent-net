package main

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/niparis/agent-net/internal/protocol"
)

func printTools(out io.Writer, reports []protocol.InventoryReport) {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tKIND\tHARNESS\tSCOPE\tPROJECT\tMACHINE\tSTATE")
	for _, report := range reports {
		for _, item := range report.Items {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", item.Name, item.Kind, item.Harness, item.Scope, item.Project, report.Machine, item.State)
		}
	}
	w.Flush()
}
