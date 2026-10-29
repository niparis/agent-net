package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

// The project registry tools. An agent registers folders on its own machine
// only. Removing a project is not a tool: the owner does it with
// `agent-net projects remove`.

const projectNote = "Registered projects are the folders where workers may start sessions on this machine."

type projectAddIn struct {
	Paths  []string `json:"paths,omitempty" jsonschema:"folders on THIS machine to register: absolute paths, or starting with ~. A folder inside a repository registers the repository's main tree; a git worktree registers its main tree"`
	Dir    string   `json:"dir,omitempty" jsonschema:"with scan=true: a folder on THIS machine to scan for repositories, for example ~/code"`
	Scan   bool     `json:"scan,omitempty" jsonschema:"scan dir for repositories and register them all"`
	Depth  int      `json:"depth,omitempty" jsonschema:"scan: how many folder levels to go down (default 3, at most 8)"`
	DryRun bool     `json:"dry_run,omitempty" jsonschema:"only list what would be registered, change nothing"`
}

type projectListIn struct {
	Name string `json:"name,omitempty" jsonschema:"only projects with this name"`
}

func addProjectTools(s *mcp.Server, run func(context.Context, string, any) (any, error)) {
	tool := func(name, desc string) *mcp.Tool { return &mcp.Tool{Name: name, Description: desc} }

	mcp.AddTool(s, tool("agent_project_add",
		"Register git repositories on THIS machine as projects of the agent network, so they are known even if no session ran there. "+
			"Give paths, or a dir with scan=true to register every repository under a folder (a scan goes 3 levels down by default, "+
			"does not enter a repository once found, skips hidden folders, node_modules, vendor and worktree folders, and registers at most 200). "+
			"You can only register folders on your own machine. The result lists what was added, what already existed, and what was skipped with the reason. "+
			"Use dry_run to preview. "+projectNote+" Only do this when your user asks."),
		func(ctx context.Context, _ *mcp.CallToolRequest, in projectAddIn) (*mcp.CallToolResult, any, error) {
			out, err := run(ctx, "project_add", protocol.ProjectAddRequest{Paths: in.Paths, Dir: in.Dir, Scan: in.Scan,
				Depth: in.Depth, DryRun: in.DryRun})
			if err != nil {
				return nil, nil, err
			}
			if m, ok := out.(map[string]any); ok && !in.DryRun {
				m["note"] = projectNote
			}
			return nil, out, nil
		})
	mcp.AddTool(s, tool("agent_project_list",
		"List the projects of the agent network: one row per repository per machine, with its path, remote, and source "+
			"(registered, seen because a session ran there, or both)."),
		func(ctx context.Context, _ *mcp.CallToolRequest, in projectListIn) (*mcp.CallToolResult, any, error) {
			out, err := run(ctx, "projects", protocol.QueryProjects{Name: in.Name})
			return nil, out, err
		})
}
