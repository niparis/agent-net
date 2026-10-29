package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

const mcpUsage = `usage: agent-net mcp [subcommand]

  (none)                        run the stdio MCP server for a Claude Code session
  add NAME [flags] -- CMD ARGS  register a stdio server, run on this machine (or --on HOST)
  add NAME --url URL [flags]    register a remote server
  list [--wide]                 list registered servers (--wide: full command lines)
  test NAME                     connect to a server now and list its tools
  call NAME TOOL [JSON]         call one tool with JSON arguments (for checks)
  remove NAME                   remove a server
  enable NAME | disable NAME    turn a server on or off

add flags:
  --on HOST          host a stdio server on another machine (host name or node ID)
  --env NAME         pass this environment variable to the process (value read on the host); repeatable
  --url URL          remote server endpoint
  --transport T      http (default) or sse
  --header-env 'Authorization=VAR'   header value read from env var VAR on the host; repeatable
  --header-file 'Authorization=PATH' header value read from a file on the host; repeatable
  --timeout 60s      per-call timeout (default 60s, at most 10m)

A change applies to every agent on every machine. Secret values are never given
on the command line: only env var names and file paths.
`

// cmdMCP runs `agent-net mcp <subcommand>`. With no subcommand the caller
// (Claude Code) wants the stdio server, which main handles.
func cmdMCP(ctx context.Context, args []string) error {
	switch args[0] {
	case "add":
		return mcpAdd(ctx, args[1:])
	case "list":
		return mcpList(ctx, args[1:])
	case "test":
		return mcpTest(ctx, args[1:])
	case "call":
		return mcpCallTool(ctx, args[1:])
	case "remove", "rm":
		return mcpRemove(ctx, args[1:])
	case "enable":
		return mcpEnable(ctx, args[1:], true)
	case "disable":
		return mcpEnable(ctx, args[1:], false)
	case "-h", "--help", "help":
		fmt.Print(mcpUsage)
		return nil
	}
	return fmt.Errorf("unknown mcp subcommand %q\n\n%s", args[0], mcpUsage)
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// splitPair splits "Name=source" on the first equals sign.
func splitPair(s string) (name, source string, err error) {
	name, source, ok := strings.Cut(s, "=")
	if !ok || name == "" || source == "" {
		return "", "", errors.New("expected 'Header-Name=SOURCE'")
	}
	return name, source, nil
}

// buildMCPServer turns the add command line into a registry entry. It is a
// function of its arguments so tests can call it.
func buildMCPServer(args []string) (srv protocol.MCPServer, on string, err error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return srv, "", errors.New("usage: agent-net mcp add NAME [flags] -- CMD ARGS   (the name comes first)")
	}
	srv.Name = args[0]
	fs := flag.NewFlagSet("mcp add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var envs, hEnv, hFile multiFlag
	fs.StringVar(&on, "on", "", "host a stdio server on this host or node")
	fs.Var(&envs, "env", "environment variable NAME to pass through")
	url := fs.String("url", "", "endpoint of a remote server")
	transport := fs.String("transport", "", "http or sse")
	fs.Var(&hEnv, "header-env", "Header=ENVVAR")
	fs.Var(&hFile, "header-file", "Header=PATH")
	timeout := fs.Duration("timeout", 0, "per-call timeout")
	if err := fs.Parse(args[1:]); err != nil {
		return srv, "", err
	}
	srv.Env = envs
	srv.TimeoutSec = int((*timeout + time.Second - 1) / time.Second)
	if *url != "" {
		srv.URL = *url
		srv.Transport = *transport
		if srv.Transport == "" {
			srv.Transport = protocol.MCPHTTP
		}
		if fs.NArg() > 0 {
			return srv, "", errors.New("a remote server takes no command")
		}
		if on != "" {
			return srv, "", errors.New("--on is for stdio servers; a remote server is reached from the machine that runs this command")
		}
		for _, h := range hEnv {
			n, v, err := splitPair(h)
			if err != nil {
				return srv, "", fmt.Errorf("--header-env: %w", err)
			}
			srv.Headers = append(srv.Headers, protocol.MCPHeader{Name: n, Env: v})
		}
		for _, h := range hFile {
			n, v, err := splitPair(h)
			if err != nil {
				return srv, "", fmt.Errorf("--header-file: %w", err)
			}
			srv.Headers = append(srv.Headers, protocol.MCPHeader{Name: n, File: v})
		}
	} else {
		if len(hEnv)+len(hFile) > 0 || *transport != "" {
			return srv, "", errors.New("--header-env, --header-file and --transport need --url")
		}
		if fs.NArg() == 0 {
			return srv, "", errors.New("give a command after `--`, or --url for a remote server")
		}
		srv.Transport = protocol.MCPStdio
		srv.Command = fs.Arg(0)
		srv.Args = fs.Args()[1:]
	}
	if err := srv.Validate(); err != nil {
		return srv, "", err
	}
	return srv, on, nil
}

func mcpAdd(ctx context.Context, args []string) error {
	srv, on, err := buildMCPServer(args)
	if err != nil {
		return err
	}
	cl, err := dialCLI(ctx)
	if err != nil {
		return err
	}
	defer cl.Close()
	raw, err := cl.Call(ctx, "mcp_add", protocol.MCPAdd{Server: srv, On: on})
	if err != nil {
		return err
	}
	var r struct {
		Server protocol.MCPServer `json:"server"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return err
	}
	var names []string
	for _, t := range r.Server.Tools {
		names = append(names, r.Server.Name+"__"+t.Name)
	}
	fmt.Printf("added %s on %s with %d tools: %s\n", r.Server.Name, r.Server.Hostname, len(names), strings.Join(names, ", "))
	fmt.Println("This applies to every agent on every machine.")
	return nil
}

func authSummary(s protocol.MCPServer) string {
	switch s.Transport {
	case protocol.MCPStdio:
		if len(s.Env) == 0 {
			return "-"
		}
		return "env " + strings.Join(s.Env, ",")
	}
	var parts []string
	for _, h := range s.Headers {
		if h.Env != "" {
			parts = append(parts, h.Name+" <- env "+h.Env)
		} else {
			parts = append(parts, h.Name+" <- file "+h.File)
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, "; ")
}

func mcpList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("mcp list", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	wide := fs.Bool("wide", false, "print the full command line in TARGET")
	fs.Parse(args)
	cl, err := dialCLI(ctx)
	if err != nil {
		return err
	}
	defer cl.Close()
	raw, err := cl.Call(ctx, "mcp_list", nil)
	if err != nil {
		return err
	}
	if *asJSON {
		printJSON(raw)
		return nil
	}
	var r struct {
		Servers []protocol.MCPServer `json:"servers"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tHOST\tTRANSPORT\tTARGET\tAUTH\tENABLED\tHEALTH\tTOOLS\tADDED BY")
	for _, s := range r.Servers {
		target := mcpTarget(s, *wide)
		host := s.Hostname
		if !s.HostOnline {
			host += " (offline)"
		}
		health := s.Health
		if s.HealthDetail != "" {
			health += ": " + s.HealthDetail
		}
		by := mcpAddedBy(s)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%v\t%s\t%d\t%s\n", s.Name, host, s.Transport, target,
			authSummary(s), s.Enabled, health, len(s.Tools), by)
	}
	return tw.Flush()
}

func oneName(args []string, sub string) (string, error) {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return "", fmt.Errorf("usage: agent-net mcp %s NAME", sub)
	}
	return args[0], nil
}

func mcpTest(ctx context.Context, args []string) error {
	name, err := oneName(args, "test")
	if err != nil {
		return err
	}
	cl, err := dialCLI(ctx)
	if err != nil {
		return err
	}
	defer cl.Close()
	raw, err := cl.Call(ctx, "mcp_test", protocol.MCPName{Name: name})
	if err != nil {
		return err
	}
	var r struct {
		OK     bool               `json:"ok"`
		Detail string             `json:"detail"`
		Tools  []protocol.MCPTool `json:"tools"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return err
	}
	if !r.OK {
		return fmt.Errorf("%s failed: %s", name, r.Detail)
	}
	var names []string
	for _, t := range r.Tools {
		names = append(names, name+"__"+t.Name)
	}
	fmt.Printf("%s is healthy, %d tools: %s\n", name, len(names), strings.Join(names, ", "))
	return nil
}

func mcpRemove(ctx context.Context, args []string) error {
	name, err := oneName(args, "remove")
	if err != nil {
		return err
	}
	cl, err := dialCLI(ctx)
	if err != nil {
		return err
	}
	defer cl.Close()
	if _, err := cl.Call(ctx, "mcp_remove", protocol.MCPName{Name: name}); err != nil {
		return err
	}
	fmt.Printf("removed %s. This applies to every agent on every machine.\n", name)
	return nil
}

func mcpEnable(ctx context.Context, args []string, enabled bool) error {
	sub := "enable"
	if !enabled {
		sub = "disable"
	}
	name, err := oneName(args, sub)
	if err != nil {
		return err
	}
	cl, err := dialCLI(ctx)
	if err != nil {
		return err
	}
	defer cl.Close()
	if _, err := cl.Call(ctx, "mcp_enable", protocol.MCPSetEnabled{Name: name, Enabled: enabled}); err != nil {
		return err
	}
	fmt.Printf("%sd %s. This applies to every agent on every machine.\n", sub, name)
	return nil
}

func mcpCallTool(ctx context.Context, args []string) error {
	if len(args) < 2 || len(args) > 3 {
		return errors.New("usage: agent-net mcp call NAME TOOL [JSON-ARGUMENTS]")
	}
	call := protocol.MCPCall{Server: args[0], Tool: args[1]}
	if len(args) == 3 {
		if !json.Valid([]byte(args[2])) {
			return errors.New("the arguments are not valid JSON")
		}
		call.Arguments = json.RawMessage(args[2])
	}
	cl, err := dialCLI(ctx)
	if err != nil {
		return err
	}
	defer cl.Close()
	raw, err := cl.Call(ctx, "mcp_call", call)
	if err != nil {
		return err
	}
	var r struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return err
	}
	for _, c := range r.Content {
		if c.Type == "text" {
			fmt.Println(c.Text)
		} else {
			fmt.Printf("[%s content]\n", c.Type)
		}
	}
	if r.IsError {
		return errors.New("the tool reported an error")
	}
	return nil
}

const targetMax = 40

// mcpTarget returns the TARGET cell. A URL is kept whole. A stdio command is
// shortened to the program base name plus the last argument, skipping a
// leading `env VAR=value` wrapper, and cut to targetMax characters. wide
// returns the full command line.
func mcpTarget(s protocol.MCPServer, wide bool) string {
	if s.Transport != protocol.MCPStdio {
		return s.URL
	}
	full := strings.Join(append([]string{s.Command}, s.Args...), " ")
	if wide {
		return full
	}
	cmd, args := s.Command, s.Args
	if filepath.Base(cmd) == "env" {
		for len(args) > 0 && (strings.Contains(args[0], "=") || strings.HasPrefix(args[0], "-")) {
			args = args[1:]
		}
		if len(args) > 0 {
			cmd, args = args[0], args[1:]
		}
	}
	short := filepath.Base(cmd)
	if len(args) > 0 {
		short += " " + args[len(args)-1]
	}
	if r := []rune(short); len(r) > targetMax {
		short = string(r[:targetMax-3]) + "..."
	}
	return short
}

// mcpAddedBy says who registered a server. A CLI add has no session, so it
// names the machine instead.
func mcpAddedBy(s protocol.MCPServer) string {
	if s.AddedByHarness == "cli" || (s.AddedByHarness == "" && s.AddedBySession == "") {
		where := s.AddedByNode
		if where == s.NodeID && s.Hostname != "" {
			where = s.Hostname
		}
		if where == "" {
			return "cli"
		}
		return "cli on " + where
	}
	by := s.AddedByHarness
	if s.AddedBySession != "" {
		by += " " + s.AddedBySession
	}
	return by
}
