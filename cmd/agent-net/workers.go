package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/niparis/agent-net/internal/daemon"
	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

// The owner's commands for workers and messages. They use the operator
// interface (decision 0008), like the TUI: the daemon treats the caller as the
// person at this machine, and refuses it when it runs inside an agent session.

func opClient() *operator.Client { return operator.New(daemon.SocketPath()) }

// cmdStart: agent-net start --project NAME --harness opencode --model M "task".
func cmdStart(ctx context.Context, args []string) error {
	return runStart(ctx, opClient(), os.Stdout, args)
}

func runStart(ctx context.Context, op operator.Operator, out io.Writer, args []string) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	project := fs.String("project", "", "project name (see `agent-net projects`)")
	machine := fs.String("machine", "", "machine, when the project is on more than one")
	harness := fs.String("harness", "opencode", "harness to run: "+strings.Join(protocol.WorkerHarnesses, ", "))
	model := fs.String("model", "", "model: a loose name such as \"deepseek 4.1\" is resolved against the machine's model list (opencode); an alias like haiku for claude; a name for pi and codex (default: the harness default)")
	isolation := fs.String("isolation", "", "worktree (the default for a git repository: the worker gets its own worktree and branch) or none (work in the project folder)")
	asJSON := fs.Bool("json", false, "print JSON")
	words, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	task := strings.TrimSpace(strings.Join(words, " "))
	if *project == "" || task == "" {
		return errors.New("usage: agent-net start --project <name> [--harness opencode|claude|pi|codex] [--model M] [--machine host] [--isolation worktree|none] \"<task>\"")
	}
	cctx, cancel := context.WithTimeout(ctx, operator.StartTimeout)
	defer cancel()
	res, err := op.StartWorker(cctx, protocol.WorkerStartRequest{Project: *project, Machine: *machine,
		Harness: *harness, Model: *model, Task: task, Isolation: *isolation})
	if err != nil {
		return err
	}
	if *asJSON {
		b, _ := json.MarshalIndent(res, "", "  ")
		fmt.Fprintln(out, string(b))
		return nil
	}
	if res.Status == "queued" {
		fmt.Fprintf(out, "%s\n", res.QueueReason)
		fmt.Fprintf(out, "start %s (%s%s, project %s on %s) waits for room. A notice comes to you when it starts.\n",
			res.QueueID, res.Harness, modelSuffix(res.Model), res.Project, res.Hostname)
		if res.MachineNote != "" {
			fmt.Fprintf(out, "machine: %s\n", res.MachineNote)
		}
		fmt.Fprintf(out, "cancel it with: agent-net stop %s\n", res.QueueID)
		return nil
	}
	fmt.Fprintf(out, "started worker %s (%s%s, project %s on %s)\n", res.SessionID, res.Harness, modelSuffix(res.Model), res.Project, res.Hostname)
	if res.MachineNote != "" {
		fmt.Fprintf(out, "machine: %s\n", res.MachineNote)
	}
	if res.ModelNote != "" {
		fmt.Fprintf(out, "model: %s\n", res.ModelNote)
	}
	if res.Worktree != "" {
		fmt.Fprintf(out, "worktree %s on branch %s\n", res.Worktree, res.Branch)
	}
	switch {
	case res.Queued:
		fmt.Fprintln(out, "the task is queued: the gateway link dropped, and it is sent when the link is back")
	case res.Task != nil:
		fmt.Fprintf(out, "task sent as %s. The reply comes to you: see `agent-net tui` or `agent-net messages`\n", res.Task.ID)
	}
	fmt.Fprintf(out, "stop it with: agent-net stop %s\n", res.SessionID)
	return nil
}

func modelSuffix(m string) string {
	if m == "" {
		return ""
	}
	return ", " + m
}

// cmdStop: agent-net stop <session>.
func cmdStop(ctx context.Context, args []string) error {
	return runStop(ctx, opClient(), os.Stdout, args)
}

func runStop(ctx context.Context, op operator.Operator, out io.Writer, args []string) error {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return errors.New("usage: agent-net stop <worker session id> (see `agent-net workers`)")
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := op.StopWorker(cctx, args[0]); err != nil {
		return err
	}
	fmt.Fprintf(out, "stopped worker %s\n", args[0])
	return nil
}

// cmdWorkers: agent-net workers.
func cmdWorkers(ctx context.Context, args []string) error {
	return runWorkers(ctx, opClient(), os.Stdout, args)
}

func runWorkers(ctx context.Context, op operator.Operator, out io.Writer, args []string) error {
	fs := flag.NewFlagSet("workers", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	ownerClaude := fs.String("owner-claude", "", "only workers owned by this machine's Claude Code session with this native id")
	recent := fs.Bool("recent", false, "also list workers that ended in the last two minutes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, operator.Timeout)
	defer cancel()
	ws, err := op.Workers(cctx, protocol.WorkersQuery{OwnerClaude: *ownerClaude, Recent: *recent})
	if err != nil {
		return err
	}
	if *asJSON {
		b, _ := json.MarshalIndent(protocol.WorkersResult{Workers: ws}, "", "  ")
		fmt.Fprintln(out, string(b))
		return nil
	}
	machineLoadLines(cctx, op, out)
	if len(ws) == 0 {
		fmt.Fprintln(out, "no workers")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION\tMACHINE\tPROJECT\tHARNESS\tMODEL\tOWNER\tSTATE\tSTARTED")
	for _, w := range ws {
		state := w.Status
		if !w.Online {
			state = "offline"
		}
		owner := w.OwnerName
		if owner == "" {
			owner = w.Owner
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", w.SessionID, w.Hostname, w.Project, w.Harness, dash(w.Model),
			owner, state, time.Unix(w.StartedAt, 0).Local().Format("Jan 02 15:04"))
	}
	return tw.Flush()
}

// machineLoadLines prints one line per machine that reports its worker load:
// "mac: workers 2 running, 1 queued · room: ok". It prints nothing when the
// usage report is not available.
func machineLoadLines(ctx context.Context, op operator.Operator, out io.Writer) {
	u, err := op.Usage(ctx)
	if err != nil {
		return
	}
	seen := map[string]bool{}
	if u.Local != nil && u.Local.Workers != nil {
		fmt.Fprintf(out, "%s: %s\n", u.Hostname, u.Local.Workers.RoomText())
		seen[u.NodeID] = true
	}
	if u.Gateway != nil {
		for _, n := range u.Gateway.Nodes {
			if !seen[n.NodeID] && n.Usage.Workers != nil {
				fmt.Fprintf(out, "%s: %s\n", n.Hostname, n.Usage.Workers.RoomText())
			}
		}
	}
}

// cmdSend: agent-net send <session or name> <text...>. It sends as the owner.
func cmdSend(ctx context.Context, args []string) error {
	return runSend(ctx, opClient(), os.Stdout, args)
}

func runSend(ctx context.Context, op operator.Operator, out io.Writer, args []string) error {
	if len(args) < 2 {
		return errors.New("usage: agent-net send <session id or name> <message...>")
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := op.SendAsOwner(cctx, args[0], strings.Join(args[1:], " "))
	if err != nil {
		return err
	}
	if res.Queued {
		fmt.Fprintln(out, "queued: the gateway is not reachable, the message is sent when it is back")
		return nil
	}
	if res.Message != nil {
		fmt.Fprintf(out, "sent %s to %s as you (%s)\n", res.Message.ID, res.Message.To, res.Message.State)
	}
	return nil
}
