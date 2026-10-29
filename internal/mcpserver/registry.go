package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

// The MCP registry in this server: four tools to manage it, and the registered
// servers' tools, added and removed at run time as `<server>__<tool>`. The
// SDK sends notifications/tools/list_changed to the client on every change.

const (
	appliesToAll = "This change applies to every agent on every machine."
	secretNote   = " Never put a secret value in any argument: give an environment variable NAME or a file PATH that exists on this machine."
)

type mcpHeaderIn struct {
	Name string `json:"name" jsonschema:"header name, for example Authorization"`
	Env  string `json:"env,omitempty" jsonschema:"NAME of an environment variable on this machine that holds the value"`
	File string `json:"file,omitempty" jsonschema:"PATH of a file on this machine that holds the value"`
}

type mcpAddIn struct {
	Name           string        `json:"name" jsonschema:"server name: lowercase letters, numbers and hyphens, at most 24 characters"`
	Command        string        `json:"command,omitempty" jsonschema:"stdio server: the program to run on this machine"`
	Args           []string      `json:"args,omitempty" jsonschema:"stdio server: arguments for the program (stored in the registry and shown to everyone: no secrets)"`
	Env            []string      `json:"env,omitempty" jsonschema:"stdio server: NAMES of environment variables to pass to the process, read on this machine"`
	URL            string        `json:"url,omitempty" jsonschema:"remote server: its MCP endpoint URL"`
	Transport      string        `json:"transport,omitempty" jsonschema:"remote server: http (streamable HTTP, default) or sse"`
	Headers        []mcpHeaderIn `json:"headers,omitempty" jsonschema:"remote server: headers to send, each with the value's source (env var NAME or file PATH on this machine)"`
	TimeoutSeconds int           `json:"timeout_seconds,omitempty" jsonschema:"per-call timeout in seconds (default 60, at most 600)"`
}

type mcpNameIn struct {
	Name string `json:"name" jsonschema:"the server name from agent_mcp_list"`
}

type mcpListIn struct{}

func addRegistryTools(s *mcp.Server, run func(context.Context, string, any) (any, error)) {
	tool := func(name, desc string) *mcp.Tool { return &mcp.Tool{Name: name, Description: desc} }

	mcp.AddTool(s, tool("agent_mcp_add",
		"Register an MCP server so that EVERY agent session on EVERY machine can use its tools, named `<server>__<tool>`. "+
			"A stdio server (command, args) runs on THIS machine. A remote server (url) is reached from this machine. "+
			"The server is connected and its tools listed before it is saved; the result names the tools. "+
			"Ask your user before adding a server."+secretNote),
		func(ctx context.Context, _ *mcp.CallToolRequest, in mcpAddIn) (*mcp.CallToolResult, any, error) {
			srv := protocol.MCPServer{Name: in.Name, Command: in.Command, Args: in.Args, Env: in.Env,
				URL: in.URL, TimeoutSec: in.TimeoutSeconds}
			switch {
			case in.URL != "":
				srv.Transport = in.Transport
				if srv.Transport == "" {
					srv.Transport = protocol.MCPHTTP
				}
				for _, h := range in.Headers {
					srv.Headers = append(srv.Headers, protocol.MCPHeader{Name: h.Name, Env: h.Env, File: h.File})
				}
			default:
				srv.Transport = protocol.MCPStdio
			}
			if err := srv.Validate(); err != nil {
				return nil, nil, err
			}
			out, err := run(ctx, "mcp_add", protocol.MCPAdd{Server: srv})
			if err != nil {
				return nil, nil, err
			}
			return nil, summarize(out, "added"), nil
		})
	mcp.AddTool(s, tool("agent_mcp_list",
		"List the registered MCP servers: host machine, transport, auth source (a name or path, never a value), enabled, health, who added it, and tool names."),
		func(ctx context.Context, _ *mcp.CallToolRequest, _ mcpListIn) (*mcp.CallToolResult, any, error) {
			out, err := run(ctx, "mcp_list", nil)
			if err != nil {
				return nil, nil, err
			}
			return nil, summarize(out, ""), nil
		})
	mcp.AddTool(s, tool("agent_mcp_test",
		"Check a registered MCP server now: its host connects, lists the tools and reports health."),
		func(ctx context.Context, _ *mcp.CallToolRequest, in mcpNameIn) (*mcp.CallToolResult, any, error) {
			out, err := run(ctx, "mcp_test", in)
			return nil, out, err
		})
	mcp.AddTool(s, tool("agent_mcp_remove",
		"Remove a registered MCP server. Its tools disappear for every agent on every machine. Ask your user first."),
		func(ctx context.Context, _ *mcp.CallToolRequest, in mcpNameIn) (*mcp.CallToolResult, any, error) {
			_, err := run(ctx, "mcp_remove", in)
			if err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"removed": in.Name, "note": appliesToAll}, nil
		})
}

// summarize shortens registry answers for a model: tool names instead of
// schemas, and a reminder that changes are global.
func summarize(out any, verb string) any {
	raw, _ := json.Marshal(out)
	var r struct {
		Server  *protocol.MCPServer  `json:"server"`
		Servers []protocol.MCPServer `json:"servers"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return out
	}
	view := func(s protocol.MCPServer) map[string]any {
		names := make([]string, 0, len(s.Tools))
		for _, t := range s.Tools {
			names = append(names, s.Name+"__"+t.Name)
		}
		m := map[string]any{"name": s.Name, "host": s.Hostname, "transport": s.Transport, "enabled": s.Enabled,
			"health": s.Health, "host_online": s.HostOnline, "tools": names,
			"added_by": fmt.Sprintf("node %s, session %s, %s", s.AddedByNode, s.AddedBySession, s.AddedByHarness)}
		if s.HealthDetail != "" {
			m["health_detail"] = s.HealthDetail
		}
		if s.Transport == protocol.MCPStdio {
			m["command"] = strings.Join(append([]string{s.Command}, s.Args...), " ")
			if len(s.Env) > 0 {
				m["env"] = s.Env
			}
		} else {
			m["url"] = s.URL
			var hs []string
			for _, h := range s.Headers {
				if h.Env != "" {
					hs = append(hs, h.Name+" from env "+h.Env)
				} else {
					hs = append(hs, h.Name+" from file "+h.File)
				}
			}
			m["headers"] = hs
		}
		return m
	}
	if r.Server != nil {
		return map[string]any{verb: view(*r.Server), "note": appliesToAll}
	}
	list := make([]map[string]any, 0, len(r.Servers))
	for _, s := range r.Servers {
		list = append(list, view(s))
	}
	return map[string]any{"servers": list}
}

// ---- dynamic tools ----

// Dynamic keeps the registered servers' tools in step with the registry.
type Dynamic struct {
	s    *mcp.Server
	call Caller

	mu   sync.Mutex
	have map[string]string // tool name -> fingerprint
}

func NewDynamic(s *mcp.Server, call Caller) *Dynamic {
	return &Dynamic{s: s, call: call, have: map[string]string{}}
}

// Refresh reads the registry and adds, updates and removes tools to match.
// On an error it changes nothing.
func (d *Dynamic) Refresh(ctx context.Context) error {
	raw, err := d.call(ctx, "mcp_tools", nil)
	if err != nil {
		return err
	}
	var r struct {
		Servers []protocol.MCPServer `json:"servers"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return err
	}
	want := map[string]*mcp.Tool{}
	fp := map[string]string{}
	for _, srv := range r.Servers {
		for _, t := range srv.Tools {
			name := srv.Name + "__" + t.Name
			desc := fmt.Sprintf("[%s on %s] %s", srv.Name, srv.Hostname, t.Description)
			want[name] = &mcp.Tool{Name: name, Description: desc, InputSchema: objectSchema(t.InputSchema)}
			fp[name] = desc + string(t.InputSchema)
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	var gone []string
	for name := range d.have {
		if _, ok := want[name]; !ok {
			gone = append(gone, name)
		}
	}
	sort.Strings(gone)
	if len(gone) > 0 {
		d.s.RemoveTools(gone...)
		for _, n := range gone {
			delete(d.have, n)
		}
	}
	for _, srv := range r.Servers {
		for _, t := range srv.Tools {
			name := srv.Name + "__" + t.Name
			if d.have[name] == fp[name] {
				continue
			}
			server, tool := srv.Name, t.Name
			d.s.AddTool(want[name], func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return d.invoke(ctx, server, tool, req.Params.Arguments), nil
			})
			d.have[name] = fp[name]
		}
	}
	return nil
}

func (d *Dynamic) invoke(ctx context.Context, server, tool string, args json.RawMessage) *mcp.CallToolResult {
	raw, err := d.call(ctx, "mcp_call", protocol.MCPCall{Server: server, Tool: tool, Arguments: args})
	if err != nil {
		return errResult(err.Error())
	}
	var res mcp.CallToolResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return errResult("unreadable answer from the MCP server: " + err.Error())
	}
	return &res
}

func errResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}

// objectSchema makes sure the schema the SDK sees is an object schema.
func objectSchema(raw json.RawMessage) any {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil || m == nil || m["type"] != "object" {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return m
}

// Watch refreshes on every trigger and every interval, until ctx ends. After
// a failed refresh (the daemon is down, or has not found this session yet) it
// retries sooner, with a growing delay.
func (d *Dynamic) Watch(ctx context.Context, trigger <-chan struct{}, every time.Duration) {
	retry := time.Second
	wait := time.Duration(0)
	for {
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-trigger:
		case <-t.C:
		}
		t.Stop()
		if err := d.Refresh(ctx); err != nil {
			wait, retry = min(retry, every), min(retry*2, every)
		} else {
			wait, retry = every, time.Second
		}
	}
}
