package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/protocol"
)

// This file is `agent-net top` (CPU and memory of every machine and session)
// and `agent-net show` (what happens inside a session). Both are for the owner:
// they ask for data that agents cannot get (decision 0007).

// usageReply is the daemon's answer to the "usage" method.
type usageReply struct {
	NodeID       string                `json:"node_id"`
	Hostname     string                `json:"hostname"`
	Local        *protocol.UsageReport `json:"local"`
	Gateway      *protocol.UsageAnswer `json:"gateway"`
	GatewayError string                `json:"gateway_error"`
}

func cmdTop(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("top", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	all := fs.Bool("all", false, "also list sessions with no figures (offline, or on a machine that reports none)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: agent-net top [--json] [--all] [SESSION]\n\nWithout SESSION: CPU and memory of every machine and session.\nWith SESSION (an ID or a name): its process tree and the 5 heaviest processes.")
	}
	fs.Parse(reorderFlags(args))
	cl, err := dialCLI(ctx)
	if err != nil {
		return err
	}
	defer cl.Close()
	if fs.NArg() > 1 {
		return errors.New("top takes at most one session")
	}
	if fs.NArg() == 1 {
		id, err := resolveSession(ctx, cl, fs.Arg(0))
		if err != nil {
			return err
		}
		raw, err := cl.Call(ctx, "usage_top", protocol.QueryUsageTop{SessionID: id})
		if err != nil {
			return err
		}
		if *asJSON {
			printJSON(raw)
			return nil
		}
		var top protocol.UsageTop
		if err := json.Unmarshal(raw, &top); err != nil {
			return err
		}
		formatTop(os.Stdout, top)
		return nil
	}

	raw, err := cl.Call(ctx, "usage", nil)
	if err != nil {
		return err
	}
	// Counts and the list are best effort: an older gateway has no counts.
	var counts *protocol.SessionCounts
	if c, err := cl.Call(ctx, "counts", nil); err == nil {
		counts = &protocol.SessionCounts{}
		if json.Unmarshal(c, counts) != nil {
			counts = nil
		}
	}
	lraw, err := cl.Call(ctx, "list", protocol.QueryAgents{OnlineOnly: !*all})
	if err != nil {
		return err
	}
	if *asJSON {
		out := map[string]any{"usage": json.RawMessage(raw), "counts": counts}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	var u usageReply
	if err := json.Unmarshal(raw, &u); err != nil {
		return err
	}
	var agents struct {
		Agents []protocol.AgentView `json:"agents"`
	}
	json.Unmarshal(lraw, &agents)
	formatUsage(os.Stdout, u, agents.Agents, counts, *all, time.Now())
	return nil
}

// reorderFlags moves flags in front of positional arguments, so
// `agent-net top mysession --json` works as well as `top --json mysession`.
func reorderFlags(args []string) []string {
	// Flags that take a value in the next argument.
	withValue := map[string]bool{"-n": true, "--n": true, "-before": true, "--before": true}
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case strings.HasPrefix(a, "-") && a != "-":
			flags = append(flags, a)
			if withValue[a] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		default:
			rest = append(rest, a)
		}
	}
	return append(flags, rest...)
}

// resolveSession turns a session ID or a name into a session ID.
func resolveSession(ctx context.Context, cl *client.Client, ref string) (string, error) {
	if strings.HasPrefix(ref, "s_") {
		return ref, nil
	}
	raw, err := cl.Call(ctx, "list", protocol.QueryAgents{OnlineOnly: false, Text: ref})
	if err != nil {
		return "", err
	}
	var r struct {
		Agents []protocol.AgentView `json:"agents"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", err
	}
	var online, offline []protocol.AgentView
	for _, a := range r.Agents {
		if a.Profile.Name == ref || a.Repo.Project == ref {
			if a.Online {
				online = append(online, a)
			} else {
				offline = append(offline, a)
			}
		}
	}
	pick := online
	if len(pick) == 0 {
		pick = offline
	}
	switch len(pick) {
	case 0:
		return "", fmt.Errorf("no session is named %q (use a session ID from `agent-net list`)", ref)
	case 1:
		return pick[0].SessionID, nil
	}
	var lines []string
	for _, a := range pick {
		lines = append(lines, fmt.Sprintf("  %s  %s on %s  %s", a.SessionID, a.Harness, a.Hostname, a.Cwd))
	}
	return "", fmt.Errorf("%d sessions match %q, give a session ID:\n%s", len(pick), ref, strings.Join(lines, "\n"))
}

// ---- formatting ----

func gib(b uint64) string { return fmt.Sprintf("%.1f", float64(b)/(1<<30)) }

func mem(b uint64) string {
	switch {
	case b >= 10<<30:
		return fmt.Sprintf("%.0fG", float64(b)/(1<<30))
	case b >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%dM", b>>20)
	}
	return fmt.Sprintf("%dK", b>>10)
}

func pct(v float64) string { return fmt.Sprintf("%.0f%%", v) }

func age(s int64) string {
	switch {
	case s < 90:
		return fmt.Sprintf("%ds", s)
	case s < 5400:
		return fmt.Sprintf("%dm", s/60)
	}
	return fmt.Sprintf("%dh", s/3600)
}

// stale tells whether a report is too old to show as current.
func stale(ageSeconds int64) bool { return ageSeconds > protocol.UsageStaleSeconds }

func formatUsage(w io.Writer, u usageReply, agents []protocol.AgentView, counts *protocol.SessionCounts, all bool, now time.Time) {
	if counts != nil {
		fmt.Fprintf(w, "Sessions: %d online, %d offline in the last 24 h (the gateway keeps %d offline sessions in all, for 30 days)\n\n",
			counts.Online, counts.Offline24h, counts.OfflineTotal)
	}
	// This machine's own report is read live; it replaces the gateway's copy.
	byNode := map[string]protocol.UsageNode{}
	if u.Gateway != nil {
		for _, n := range u.Gateway.Nodes {
			byNode[n.NodeID] = n
		}
	}
	if u.Local != nil {
		byNode[u.NodeID] = protocol.UsageNode{NodeID: u.NodeID, Hostname: u.Hostname, Usage: *u.Local}
	}
	if u.GatewayError != "" {
		fmt.Fprintf(w, "gateway: %s\n\n", u.GatewayError)
	}

	// Machines.
	hosts := map[string]string{} // node ID -> host
	for _, a := range agents {
		hosts[a.NodeID] = a.Hostname
	}
	for id, n := range byNode {
		hosts[id] = n.Hostname
	}
	ids := make([]string, 0, len(hosts))
	for id := range hosts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if (ids[i] == u.NodeID) != (ids[j] == u.NodeID) {
			return ids[i] == u.NodeID
		}
		return hosts[ids[i]] < hosts[ids[j]]
	})
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "MACHINE\tCPU\tCPU 5m\tMEMORY\tMEM 5m\tLOAD\tPROCS\tCORES\tAGE")
	for _, id := range ids {
		n, ok := byNode[id]
		name := hosts[id]
		if id == u.NodeID {
			name += " (here)"
		}
		if !ok || stale(n.AgeSeconds) {
			fmt.Fprintf(tw, "%s\tunknown\t\t\t\t\t\t\t%s\n", name, ageOrDash(ok, n.AgeSeconds))
			continue
		}
		m := n.Usage.Machine
		cpu, cpu5 := pct(m.CPU), pct(m.CPUAvg)
		if m.Samples == 0 {
			cpu, cpu5 = "...", "..."
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s/%s\t%s\t%.2f\t%d\t%d\t%s\n", name, cpu, cpu5,
			gib(m.MemUsed), gib(m.MemTotal)+"G", gib(m.MemUsedAvg)+"G", m.Load[0], m.Procs, m.Cores, age(n.AgeSeconds))
	}
	tw.Flush()
	fmt.Fprintln(w)

	// Sessions. A tree that several sessions share (the OpenCode service) is
	// listed once, after the others, never split between its sessions.
	byID := map[string]protocol.AgentView{}
	for _, a := range agents {
		byID[a.SessionID] = a
	}
	type row struct {
		a    protocol.AgentView
		tree *protocol.TreeUsage
	}
	var rows []row
	var shared []struct {
		host string
		t    protocol.TreeUsage
	}
	have := map[string]bool{}
	for id, n := range byNode {
		fresh := !stale(n.AgeSeconds)
		for i := range n.Usage.Trees {
			t := n.Usage.Trees[i]
			if !fresh {
				continue
			}
			if t.Shared {
				shared = append(shared, struct {
					host string
					t    protocol.TreeUsage
				}{hosts[id], t})
				for _, s := range t.Sessions {
					have[s] = true
				}
				continue
			}
			for _, s := range t.Sessions {
				if a, ok := byID[s]; ok {
					tt := t
					rows = append(rows, row{a: a, tree: &tt})
					have[s] = true
				}
			}
		}
	}
	for _, a := range agents {
		if !have[a.SessionID] && (all || a.Online) {
			rows = append(rows, row{a: a})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		ci, cj := -1.0, -1.0
		if rows[i].tree != nil {
			ci = rows[i].tree.CPU
		}
		if rows[j].tree != nil {
			cj = rows[j].tree.CPU
		}
		return ci > cj
	})
	tw = tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if len(rows) > 0 {
		fmt.Fprintln(tw, "SESSION\tNAME\tHARNESS\tHOST\tCPU\tCPU 5m\tRSS\tRSS 5m\tPROCS")
	}
	for _, r := range rows {
		a := r.a
		if r.tree == nil {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\tunknown\t\t\t\t\n", a.SessionID, a.Profile.Name, a.Harness, a.Hostname)
			continue
		}
		t := r.tree
		cpu, cpu5 := pct(t.CPU), pct(t.CPUAvg)
		if t.Samples == 0 {
			cpu, cpu5 = "...", "..."
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\n", a.SessionID, a.Profile.Name, a.Harness, a.Hostname,
			cpu, cpu5, mem(t.RSS), mem(t.RSSAvg), t.Procs)
	}
	tw.Flush()
	for _, s := range shared {
		var names []string
		for _, id := range s.t.Sessions {
			if a, ok := byID[id]; ok && a.Profile.Name != "" {
				names = append(names, a.Profile.Name)
			} else {
				names = append(names, id)
			}
		}
		cpu, cpu5 := pct(s.t.CPU), pct(s.t.CPUAvg)
		if s.t.Samples == 0 {
			cpu, cpu5 = "...", "..."
		}
		fmt.Fprintf(w, "\nShared by %d %s sessions on %s (pid %d): CPU %s (5m %s), RSS %s (5m %s), %d processes.\n",
			len(s.t.Sessions), s.t.Harness, s.host, s.t.PID, cpu, cpu5, mem(s.t.RSS), mem(s.t.RSSAvg), s.t.Procs)
		fmt.Fprintf(w, "  One process serves all of them, so there is no figure per session: %s\n", strings.Join(names, ", "))
	}
	fmt.Fprintln(w, "\nCPU of a session: percent of one core (100% = one full core), all its processes together. CPU of a machine: percent of all cores.")
}

func ageOrDash(ok bool, s int64) string {
	if !ok {
		return "-"
	}
	return age(s)
}

func formatTop(w io.Writer, top protocol.UsageTop) {
	t := top.Tree
	cpu, cpu5 := pct(t.CPU), pct(t.CPUAvg)
	if t.Samples == 0 {
		cpu, cpu5 = "...", "..."
	}
	fmt.Fprintf(w, "Session %s (%s): the tree of process %d, %d process(es) in all\n", top.SessionID, t.Harness, t.PID, t.Procs)
	fmt.Fprintf(w, "CPU %s (5m average %s) of one core, RSS %s (5m average %s)\n", cpu, cpu5, mem(t.RSS), mem(t.RSSAvg))
	if t.Shared {
		fmt.Fprintf(w, "Shared: this is the process of %d sessions, so the figures are for all of them.\n", len(t.Sessions))
	}
	list := func(title string, ps []protocol.ProcUsage) {
		fmt.Fprintf(w, "\n%s\n", title)
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "PID\tCPU\tRSS\tCOMMAND\tARGS")
		for _, p := range ps {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", p.PID, pct(p.CPU), mem(p.RSS), p.Cmd, p.Args)
		}
		tw.Flush()
	}
	list("Top by CPU (over the last 5 seconds)", top.ByCPU)
	list("Top by memory", top.ByRSS)
}

// ---- show ----

func cmdShow(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	n := fs.Int("n", protocol.TextDefaultLimit, "how many entries")
	follow := fs.Bool("follow", false, "keep showing new entries (ctrl+c to stop)")
	fs.BoolVar(follow, "f", false, "same as --follow")
	before := fs.String("before", "", "show the entries before this cursor (the `older` value of an earlier --json answer)")
	side := fs.Bool("sidechain", false, "also show the entries of sub-agents (Claude Code)")
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: agent-net show SESSION [-n N] [--follow] [--before CURSOR] [--sidechain] [--json]\n\nShows what happens inside a session: its prompts, replies and tool calls, newest last.\nSecrets that look like keys or tokens are masked. Each entry is cut to 2000 characters.")
	}
	fs.Parse(reorderFlags(args))
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("show needs one session (an ID or a name)")
	}
	cl, err := dialCLI(ctx)
	if err != nil {
		return err
	}
	defer cl.Close()
	id, err := resolveSession(ctx, cl, fs.Arg(0))
	if err != nil {
		return err
	}
	q := protocol.TextQuery{SessionID: id, Limit: *n, Before: *before, Sidechain: *side}
	page, err := fetchText(ctx, cl, q)
	if err != nil {
		return err
	}
	if *asJSON && !*follow {
		b, _ := json.MarshalIndent(page, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	printText(os.Stdout, page, *asJSON)
	if page.Older != "" && !*follow && !*asJSON {
		fmt.Fprintf(os.Stderr, "-- older entries exist: agent-net show %s --before %s\n", fs.Arg(0), page.Older)
	}
	if !*follow {
		return nil
	}
	after := page.Newer
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
		fq := protocol.TextQuery{SessionID: id, Limit: protocol.TextMaxLimit, After: after, Sidechain: *side}
		if after == "" {
			fq.After, fq.Limit = "", *n
		}
		next, err := fetchText(ctx, cl, fq)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if len(next.Entries) > 0 {
			printText(os.Stdout, next, *asJSON)
		}
		if next.Newer != "" {
			after = next.Newer
		}
	}
}

func fetchText(ctx context.Context, cl *client.Client, q protocol.TextQuery) (protocol.TextPage, error) {
	raw, err := cl.Call(ctx, "session_text", q)
	if err != nil {
		return protocol.TextPage{}, err
	}
	var page protocol.TextPage
	return page, json.Unmarshal(raw, &page)
}

var roleLabel = map[string]string{
	protocol.RoleUser: "user", protocol.RoleAssistant: "assistant",
	protocol.RoleToolCall: "tool", protocol.RoleToolResult: "result",
}

func printText(w io.Writer, page protocol.TextPage, asJSON bool) {
	for _, e := range page.Entries {
		if asJSON {
			b, _ := json.Marshal(e)
			fmt.Fprintln(w, string(b))
			continue
		}
		ts := "--:--:--"
		if e.Time > 0 {
			ts = time.UnixMilli(e.Time).Local().Format("15:04:05")
		}
		label := roleLabel[e.Role]
		if e.Sidechain {
			label += "*"
		}
		text := e.Text
		if e.Cut {
			text += " [cut]"
		}
		lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
		fmt.Fprintf(w, "%s  %-10s %s\n", ts, label, lines[0])
		for _, l := range lines[1:] {
			fmt.Fprintf(w, "%s  %-10s %s\n", strings.Repeat(" ", 8), "", l)
		}
	}
}
