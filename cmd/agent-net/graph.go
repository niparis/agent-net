package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

type graphCaller interface {
	Call(context.Context, string, any) (json.RawMessage, error)
}

func cmdGraph(ctx context.Context, cmd string, args []string) error {
	return runGraphCommand(ctx, opClient(), os.Stdout, cmd, args)
}

func runGraphCommand(ctx context.Context, op graphCaller, out io.Writer, cmd string, args []string) error {
	ctx, cancel := context.WithTimeout(ctx, operator.Timeout)
	defer cancel()
	method := ""
	var params any = struct{}{}
	usage := func() error {
		return fmt.Errorf("usage: agent-net charter show|set|history ...; graph [undo [id]]; node add-architect|remove <name>|settings <name> [setting value...]; edge set|remove <from> <kind> <to> [mode] [rule]")
	}
	switch cmd {
	case "charter":
		if len(args) < 2 {
			return usage()
		}
		switch args[0] {
		case "show":
			if len(args) != 2 {
				return usage()
			}
			method = protocol.OpGraphGet
		case "set":
			if len(args) < 3 {
				return usage()
			}
			method = protocol.OpCharterSet
			params = protocol.CharterSetRequest{Node: args[1], Attr: args[2], Value: strings.Join(args[3:], " ")}
		case "history":
			if len(args) != 2 {
				return usage()
			}
			method = protocol.OpCharterHistory
			params = protocol.GraphHistoryRequest{Node: args[1]}
		default:
			return usage()
		}
	case "graph":
		if len(args) == 0 {
			method = protocol.OpGraphGet
		} else if args[0] == "undo" && len(args) <= 2 {
			method = protocol.OpGraphUndo
			var id int64
			if len(args) == 2 {
				var err error
				id, err = strconv.ParseInt(args[1], 10, 64)
				if err != nil || id <= 0 {
					return usage()
				}
			}
			params = protocol.GraphUndoRequest{ID: id}
		} else {
			return usage()
		}
	case "node":
		if len(args) < 2 {
			return usage()
		}
		switch args[0] {
		case "add-architect", "remove":
			if len(args) != 2 {
				return usage()
			}
			params = protocol.GraphNodeRequest{Name: args[1]}
			if args[0] == "add-architect" {
				method = protocol.OpNodeAdd
			} else {
				method = protocol.OpNodeRemove
			}
		case "settings":
			if len(args) == 2 {
				method = protocol.OpNodeSettingsGet
				params = protocol.NodeSettingsGetRequest{Node: args[1]}
			} else {
				if len(args) < 3 {
					return usage()
				}
				method = protocol.OpNodeSettingsSet
				params = protocol.NodeSettingsSetRequest{Node: args[1], Setting: args[2], Value: strings.Join(args[3:], " ")}
			}
		default:
			return usage()
		}
	case "edge":
		if len(args) < 4 || len(args) > 6 {
			return usage()
		}
		e := protocol.Edge{From: args[1], Kind: args[2], To: args[3]}
		if len(args) > 4 {
			e.Mode = args[4]
		}
		if len(args) > 5 {
			e.Rule = args[5]
		}
		params = e
		switch args[0] {
		case "set":
			method = protocol.OpEdgeSet
		case "remove":
			if len(args) != 4 {
				return usage()
			}
			method = protocol.OpEdgeRemove
		default:
			return usage()
		}
	default:
		return usage()
	}
	raw, err := op.Call(ctx, method, params)
	if err != nil {
		return err
	}
	if method == protocol.OpGraphGet {
		var g protocol.Graph
		if err = json.Unmarshal(raw, &g); err != nil {
			return err
		}
		found := false
		for _, n := range g.Nodes {
			if cmd == "charter" && n.Name != args[1] {
				continue
			}
			found = true
			fmt.Fprintf(out, "%s (%s)\n", n.Name, n.Kind)
			printCharter(out, n.Charter)
		}
		if cmd == "charter" && !found {
			return fmt.Errorf("unknown node %q", args[1])
		}
		if cmd == "graph" {
			for _, e := range g.Edges {
				fmt.Fprintf(out, "%s %s %s mode=%s rule=%s\n", e.From, e.Kind, e.To, e.Mode, e.Rule)
			}
		}
	} else if method == protocol.OpCharterHistory {
		var changes []protocol.CharterChange
		if err = json.Unmarshal(raw, &changes); err != nil {
			return err
		}
		for _, c := range changes {
			fmt.Fprintf(out, "%d %s %s %q -> %q by %s at %d\n", c.ID, c.Node, c.Attr, c.OldValue, c.NewValue, c.By, c.At)
		}
	} else if method == protocol.OpNodeSettingsGet || method == protocol.OpNodeSettingsSet {
		var res protocol.NodeSettingsResult
		if err = json.Unmarshal(raw, &res); err != nil {
			return err
		}
		printNodeSettings(out, res)
	} else {
		fmt.Fprintln(out, "ok")
	}
	return nil
}

func printCharter(out io.Writer, c protocol.Charter) {
	for _, a := range []struct{ name, value string }{{"purpose", c.Purpose}, {"maturity", c.Maturity}, {"accepts", c.Accepts}, {"refuses", c.Refuses}, {"feature_requests", c.FeatureRequests}, {"consumers", c.Consumers}, {"escalate", c.Escalate}, {"needs", c.Needs}, {"notes", c.Notes}} {
		if a.value != "" {
			fmt.Fprintf(out, "  %s: %s\n", a.name, a.value)
		}
	}
}

func printNodeSettings(out io.Writer, res protocol.NodeSettingsResult) {
	fmt.Fprintf(out, "%s (maturity %s)\n", res.Node, orDash(res.Maturity))
	for _, setting := range protocol.NodeSettingNames() {
		if value := res.Overrides[setting]; value != "" {
			fmt.Fprintf(out, "  override %s: %s\n", setting, value)
		}
	}
	for _, a := range []struct{ name, value string }{
		{protocol.SettingTestsBeforeMerge, res.Effective.TestsBeforeMerge},
		{protocol.SettingMergeAuthority, res.Effective.MergeAuthority},
		{protocol.SettingWorkerSandbox, res.Effective.WorkerSandbox},
		{protocol.SettingJudgeConsumers, res.Effective.JudgeConsumers},
		{protocol.SettingDeployRule, res.Effective.DeployRule},
	} {
		fmt.Fprintf(out, "  effective %s: %s\n", a.name, a.value)
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
