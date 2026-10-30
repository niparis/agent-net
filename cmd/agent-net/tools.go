package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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

type toolsClient interface {
	Call(context.Context, string, any) (json.RawMessage, error)
}

func readTools(ctx context.Context, c toolsClient, out io.Writer) error {
	raw, err := c.Call(ctx, "inventory_get", struct{}{})
	if err != nil {
		return err
	}
	var reports []protocol.InventoryReport
	if err = json.Unmarshal(raw, &reports); err != nil {
		return err
	}
	printTools(out, reports)
	return nil
}

func cmdTools(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return errors.New("usage: agent-net tools")
	}
	c, err := dialCLI(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	return readTools(ctx, c, os.Stdout)
}
