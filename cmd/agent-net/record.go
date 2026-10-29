package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/niparis/agent-net/internal/adminclient"
	"github.com/niparis/agent-net/internal/protocol"
)

// cmdProjects lists the known projects: registered ones and those where
// sessions ran. The subcommands add, scan and remove change the registry.
func cmdProjects(ctx context.Context, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "add":
			return cmdProjectsAdd(ctx, args[1:], false)
		case "scan":
			return cmdProjectsAdd(ctx, args[1:], true)
		case "remove":
			return cmdProjectsRemove(ctx, args[1:])
		}
	}
	fs := flag.NewFlagSet("projects", flag.ExitOnError)
	name := fs.String("name", "", "only the project with this name")
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Parse(args)
	cl, err := dialCLI(ctx)
	if err != nil {
		return err
	}
	defer cl.Close()
	raw, err := cl.Call(ctx, "projects", protocol.QueryProjects{Name: *name})
	if err != nil {
		return err
	}
	if *asJSON {
		printJSON(raw)
		return nil
	}
	var r struct {
		Projects []protocol.ProjectView `json:"projects"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return err
	}
	printProjects(os.Stdout, r.Projects, time.Local)
	return nil
}

func printProjects(w io.Writer, ps []protocol.ProjectView, loc *time.Location) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "PROJECT\tMACHINE\tPATH\tREMOTE\tSOURCE\tHARNESSES\tWORKTREES\tONLINE\tLAST SEEN")
	for _, p := range ps {
		source, seen := p.Source, "-"
		if source == "" { // an older gateway only knows sessions
			source = protocol.SourceSeen
		}
		if p.LastSeen > 0 {
			seen = time.Unix(p.LastSeen, 0).In(loc).Format("2006-01-02 15:04")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\n", p.Name, p.Hostname, p.Path, dash(p.Remote), source,
			dash(strings.Join(p.Harnesses, ",")), dash(strings.Join(p.Worktrees, ",")), p.Online, seen)
	}
	tw.Flush()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// cmdEvents shows the gateway's events table. It runs on the gateway machine.
func cmdEvents(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("events", flag.ExitOnError)
	node := fs.String("node", "", "only events of this node (ID or host name), its sessions and their messages")
	session := fs.String("session", "", "only events of this session and its messages")
	kind := fs.String("kind", "", "only this kind, or a group like msg (matches msg.stored, msg.failed, ...)")
	since := fs.String("since", "", "only events after this: a duration like 2h or 30m, or a time like 2026-10-29T12:00:00Z or \"2026-10-29 12:00\"")
	limit := fs.Int("limit", 100, "how many events (the newest)")
	follow := fs.Bool("follow", false, "keep running and print new events")
	fs.BoolVar(follow, "f", false, "same as --follow")
	asJSON := fs.Bool("json", false, "print one JSON object per line")
	data := fs.String("data", defaultGatewayData(), "gateway data directory")
	fs.Parse(args)
	var sinceT time.Time
	if *since != "" {
		t, err := parseSince(*since, time.Now(), time.Local)
		if err != nil {
			return err
		}
		sinceT = t
	}
	ac, err := adminclient.Open(*data)
	if err != nil {
		return err
	}
	q := url.Values{}
	for k, v := range map[string]string{"node": *node, "session": *session, "kind": *kind} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if !sinceT.IsZero() {
		q.Set("since", sinceT.UTC().Format(time.RFC3339))
	}
	q.Set("limit", strconv.Itoa(*limit))
	fetch := func(afterID int64) ([]protocol.Event, error) {
		qq := url.Values{}
		for k, v := range q {
			qq[k] = v
		}
		if afterID > 0 {
			qq.Set("after_id", strconv.FormatInt(afterID, 10))
		}
		var r struct {
			Events []protocol.Event `json:"events"`
		}
		return r.Events, ac.Get(ctx, "/v1/admin/events", qq, &r)
	}
	evs, err := fetch(0)
	if err != nil {
		return err
	}
	out := newEventPrinter(os.Stdout, *asJSON, time.Local)
	out.print(evs)
	if !*follow {
		return nil
	}
	last := int64(0)
	if len(evs) > 0 {
		last = evs[len(evs)-1].ID
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
		more, err := fetch(last)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		out.print(more)
		if len(more) > 0 {
			last = more[len(more)-1].ID
		}
	}
}

// parseSince reads a --since value: a duration back from now, an RFC 3339
// time, or a local date and time.
func parseSince(s string, now time.Time, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	if d, err := time.ParseDuration(s); err == nil {
		if d < 0 {
			return time.Time{}, errors.New("--since: a duration must not be negative")
		}
		return now.Add(-d), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("--since: cannot read %q; use a duration like 2h or a time like 2026-10-29T12:00:00Z", s)
}

type eventPrinter struct {
	w      io.Writer
	asJSON bool
	loc    *time.Location
	header bool
}

func newEventPrinter(w io.Writer, asJSON bool, loc *time.Location) *eventPrinter {
	return &eventPrinter{w: w, asJSON: asJSON, loc: loc}
}

// print writes events in a table with a header (the first time) or as JSON lines.
// Columns are left to the terminal: fields are tab separated, then aligned per call.
func (p *eventPrinter) print(evs []protocol.Event) {
	if p.asJSON {
		enc := json.NewEncoder(p.w)
		for _, e := range evs {
			enc.Encode(e)
		}
		return
	}
	tw := tabwriter.NewWriter(p.w, 0, 4, 2, ' ', 0)
	if !p.header {
		fmt.Fprintln(tw, "TIME\tKIND\tNODE\tSESSION\tMESSAGE\tDETAIL")
		p.header = true
	}
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].ID < evs[j].ID })
	for _, e := range evs {
		node := e.Hostname
		if node == "" {
			node = e.NodeID
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", e.Time.In(p.loc).Format("2006-01-02 15:04:05"), e.Kind,
			dash(node), dash(e.SessionID), dash(e.MessageID), e.Detail)
	}
	tw.Flush()
}

// withoutDormant drops dormant sessions unless the caller asked for them. They
// are reachable, but have had no activity for a long time. --json keeps them.
func withoutDormant(agents []protocol.AgentView, include bool) []protocol.AgentView {
	if include {
		return agents
	}
	out := make([]protocol.AgentView, 0, len(agents))
	for _, a := range agents {
		if a.Status != protocol.StatusDormant {
			out = append(out, a)
		}
	}
	return out
}

// printAgents is the table of `agent-net list`. FUNCTION is the project
// description the daemon read from AGENTS.md ("-" when there is none). TASK
// (wide only) is what the agent announced. PROJECT is the project the
// machine found from the agent's folder; an agent that announced a project
// but has no repo info shows its announced one.
func printAgents(w io.Writer, agents []protocol.AgentView, wide bool) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if wide {
		fmt.Fprintln(tw, "SESSION\tNAME\tHARNESS\tHOST\tPROJECT\tBRANCH\tSTATUS\tMODE\tMODEL\tONLINE\tFUNCTION\tTASK\tREMOTE\tFOLDER\tNODE\tWORKTREE")
	} else {
		fmt.Fprintln(tw, "SESSION\tNAME\tHARNESS\tHOST\tPROJECT\tSTATUS\tMODE\tMODEL\tONLINE\tFUNCTION")
	}
	for _, a := range agents {
		project := a.Repo.Project
		if project == "" {
			project = a.Profile.Project
		}
		if wide {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%v\t%s\t%s\t%s\t%s\t%s\t%s\n", a.SessionID, a.Profile.Name, a.Harness,
				a.Hostname, dash(project), dash(a.Repo.Branch), a.Status, dash(a.Mode), dash(a.Model), a.Online, dash(a.Profile.Function),
				dash(a.Profile.Task), dash(a.Repo.Remote), dash(a.Cwd), a.NodeID, dash(a.Repo.Worktree))
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%v\t%s\n", a.SessionID, a.Profile.Name, a.Harness, a.Hostname,
			dash(project), a.Status, dash(a.Mode), dash(a.Model), a.Online, dash(a.Profile.Function))
	}
	return tw.Flush()
}
