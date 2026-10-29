package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/protocol"
)

// parseInterspersed parses flags that may come before, between or after the
// positional arguments, as in `projects scan ~/code --depth 2`.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

// absPath makes a path absolute against the current folder, as the daemon
// has another one. ~ is left for the daemon to expand.
func absPath(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || filepath.IsAbs(p) {
		return p
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// daemonCall explains the error of a daemon that is older than this command.
func daemonCall(ctx context.Context, cl *client.Client, method string, params any) (json.RawMessage, error) {
	raw, err := cl.Call(ctx, method, params)
	if err != nil && (strings.Contains(err.Error(), "not available to cli clients") || strings.Contains(err.Error(), "unknown method")) {
		return nil, errors.New("this machine's daemon is too old for project registration; run `agent-net upgrade`")
	}
	return raw, err
}

// cmdProjectsAdd is `projects add <path>...`; with scan it is `projects scan <dir>`.
func cmdProjectsAdd(ctx context.Context, args []string, scan bool) error {
	name := "add"
	if scan {
		name = "scan"
	}
	fs := flag.NewFlagSet("projects "+name, flag.ExitOnError)
	dry := fs.Bool("dry-run", false, "list what would be registered and change nothing")
	asJSON := fs.Bool("json", false, "print JSON")
	depth := fs.Int("depth", 0, fmt.Sprintf("scan: how many folder levels to go down (default %d, at most %d)", protocol.DefaultScanDepth, protocol.MaxScanDepth))
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	req := protocol.ProjectAddRequest{DryRun: *dry}
	switch {
	case scan && len(pos) != 1:
		return errors.New("usage: agent-net projects scan <dir> [--depth N] [--dry-run]")
	case scan:
		req.Dir, req.Scan, req.Depth = absPath(pos[0]), true, *depth
	case len(pos) == 0:
		return errors.New("usage: agent-net projects add <path>... [--dry-run]")
	default:
		for _, p := range pos {
			req.Paths = append(req.Paths, absPath(p))
		}
	}
	cl, err := dialCLI(ctx)
	if err != nil {
		return err
	}
	defer cl.Close()
	raw, err := daemonCall(ctx, cl, "project_add", req)
	if err != nil {
		return err
	}
	if *asJSON {
		printJSON(raw)
		return nil
	}
	var out protocol.ProjectAddOutcome
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	printAddOutcome(os.Stdout, out)
	return nil
}

func printAddOutcome(w io.Writer, o protocol.ProjectAddOutcome) {
	add := "added"
	if o.DryRun {
		add = "would add"
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if len(o.Added)+len(o.Existing)+len(o.Skipped) > 0 {
		fmt.Fprintln(tw, "RESULT\tNAME\tPATH\tDETAIL")
	}
	for _, e := range o.Added {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", add, e.Name, e.Path, dash(e.Remote))
	}
	for _, e := range o.Existing {
		fmt.Fprintf(tw, "exists\t%s\t%s\t%s\n", e.Name, e.Path, dash(e.Remote))
	}
	for _, s := range o.Skipped {
		fmt.Fprintf(tw, "skipped\t-\t%s\t%s\n", s.Path, s.Reason)
	}
	tw.Flush()
	if o.Overflow > 0 {
		fmt.Fprintf(w, "%d more repositories were found and not looked at (the limit is %d per call); scan a smaller folder\n",
			o.Overflow, protocol.ProjectMaxPerCall)
	}
	verb := "added"
	if o.DryRun {
		verb = "would be added"
	}
	fmt.Fprintf(w, "%d %s on %s, %d already registered, %d skipped", len(o.Added), verb, o.Machine, len(o.Existing), len(o.Skipped))
	if o.DryRun {
		fmt.Fprint(w, " (dry run: nothing was changed)")
	}
	fmt.Fprintln(w)
}

// cmdProjectsRemove is `projects remove <name|path>`.
func cmdProjectsRemove(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("projects remove", flag.ExitOnError)
	on := fs.String("on", "", "remove the project of another machine (host name or node ID), for example one that is gone")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: agent-net projects remove <name|path> [--on <machine>]")
	}
	p := protocol.ProjectRemove{On: *on}
	if a := pos[0]; strings.HasPrefix(a, "/") || strings.HasPrefix(a, "~") || a == "." || a == ".." ||
		strings.HasPrefix(a, "./") || strings.HasPrefix(a, "../") {
		p.Path = absPath(a)
	} else {
		p.Name = a
	}
	cl, err := dialCLI(ctx)
	if err != nil {
		return err
	}
	defer cl.Close()
	raw, err := daemonCall(ctx, cl, "project_remove", p)
	if err != nil {
		return err
	}
	var r struct {
		Removed protocol.ProjectEntry `json:"removed"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return err
	}
	fmt.Printf("removed %s (%s) from the registry\n", r.Removed.Name, r.Removed.Path)
	return nil
}
