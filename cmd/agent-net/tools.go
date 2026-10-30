package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/niparis/agent-net/internal/protocol"
)

func printTools(out io.Writer, items []protocol.ToolItem) {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tKIND\tSCOPE\tHARNESSES\tMACHINES\tVARIANTS\tSTATE")
	for _, item := range items {
		var scope []string
		if item.Global {
			scope = append(scope, "global")
		}
		scope = append(scope, item.Projects...)
		if item.Unassigned {
			scope = append(scope, "unassigned")
		}
		if len(scope) == 0 {
			scope = append(scope, "unassigned")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n", item.Name, item.Kind, strings.Join(scope, ", "), strings.Join(item.Harnesses, ", "), strings.Join(item.Machines, ", "), item.Variants, item.State)
	}
	w.Flush()
}

type toolsClient interface {
	Call(context.Context, string, any) (json.RawMessage, error)
}

func readTools(ctx context.Context, c toolsClient, out io.Writer) error {
	offset := 0
	var items []protocol.ToolItem
	for {
		raw, err := c.Call(ctx, "tools_items", map[string]int{"offset": offset})
		if err != nil {
			return err
		}
		var page protocol.ToolsPage
		if err = json.Unmarshal(raw, &page); err != nil {
			return err
		}
		items = append(items, page.Items...)
		if page.Next == 0 {
			break
		}
		if page.Next <= offset {
			return errors.New("invalid tools page cursor")
		}
		offset = page.Next
	}
	printTools(out, items)
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
