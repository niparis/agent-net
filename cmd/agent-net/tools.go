package main

import (
	"bytes"
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
	fmt.Fprintln(w, "NAME\tKIND\tSCOPE\tHARNESSES\tMACHINES\tVARIANTS\tSTATE\tDESIRED\tAPPLIED")
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
		desired, applied := "-", "-"
		if item.Desired != nil {
			desired = item.Desired.Assignment.Scope
			if desired == "projects" {
				desired = strings.Join(item.Desired.Assignment.Projects, ", ")
			}
		}
		if item.Applied != "" {
			applied = item.Applied
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s\n", item.Name, item.Kind, strings.Join(scope, ", "), strings.Join(item.Harnesses, ", "), strings.Join(item.Machines, ", "), item.Variants, item.State, desired, applied)
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

func runTools(ctx context.Context, c toolsClient, out io.Writer, args []string) error {
	if len(args) == 0 {
		return readTools(ctx, c, out)
	}
	const usage = "usage: agent-net tools [plan ITEM global|uninstalled|PROJECT... [--on MACHINE]... | apply PLAN_ID | history ITEM]"
	var method string
	var params any
	switch args[0] {
	case "plan":
		if len(args) < 3 {
			return errors.New(usage)
		}
		target := protocol.ToolAssignment{}
		// --on MACHINE (repeatable) limits the change to chosen machines.
		rest := []string{args[0], args[1]}
		for i := 2; i < len(args); i++ {
			if args[i] == "--on" {
				if i+1 >= len(args) || args[i+1] == "" {
					return errors.New(usage)
				}
				target.Machines = append(target.Machines, args[i+1])
				i++
				continue
			}
			rest = append(rest, args[i])
		}
		args = rest
		if len(args) < 3 {
			return errors.New(usage)
		}
		switch args[2] {
		case "global", "uninstalled":
			if len(args) != 3 {
				return errors.New(usage)
			}
			target.Scope = args[2]
		default:
			target.Scope = "projects"
			target.Projects = args[2:]
			for _, name := range target.Projects {
				if name == "global" || name == "uninstalled" || name == "" {
					return errors.New(usage)
				}
			}
		}
		method = "tools_plan"
		params = protocol.ToolPlanRequest{Item: args[1], Target: target}
	case "apply":
		if len(args) != 2 {
			return errors.New(usage)
		}
		method = "tools_apply"
		params = protocol.ToolPlanID{ID: args[1]}
	case "history":
		if len(args) != 2 {
			return errors.New(usage)
		}
		method = "tools_history"
		params = protocol.ToolHistoryRequest{Item: args[1]}
	default:
		return errors.New(usage)
	}
	raw, err := c.Call(ctx, method, params)
	if err != nil {
		return err
	}
	var pretty bytes.Buffer
	if err = json.Indent(&pretty, raw, "", "  "); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, pretty.String())
	return err
}
func cmdTools(ctx context.Context, args []string) error {
	c, err := dialCLI(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	return runTools(ctx, c, os.Stdout, args)
}
