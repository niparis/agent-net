package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

// The worker tools (decision 0002). An agent starts a worker in a project and
// stops its own workers. The daemon enforces the rules: a worker cannot start
// workers, the machine must accept them, and at most four run on a machine.

const workerNote = "A worker is an agent session that agent-net starts for you with nobody watching it. It uses the project's own permission settings. " +
	"It ends on its own after 60 minutes without messages, after 4 hours, or when you end. Only start one when your user asks."

type startIn struct {
	Project   string `json:"project,omitempty" jsonschema:"The name of a project from agent_project_list. Leave it empty to use the project you are working in (on the machine that has the harness)"`
	Task      string `json:"task" jsonschema:"what the worker should do, with everything it needs to know; it cannot ask you questions except by agent_reply"`
	Harness   string `json:"harness,omitempty" jsonschema:"opencode (the default), claude, pi or codex. agent_nodes shows which are installed on the machine. A pi worker needs isolation worktree."`
	Model     string `json:"model,omitempty" jsonschema:"The model, as the harness names it. opencode: a loose name is enough, such as deepseek 4.1 or gpt 6.1; agent-net resolves it against the machine's model list and tells you the result (exact ids are provider/model, see agent_nodes). If several models match equally, the error lists them. claude: an alias such as haiku, sonnet or opus, or a full model name. pi: provider/id, with an optional :thinking level. codex: a model name. Empty means the harness default."`
	Machine   string `json:"machine,omitempty" jsonschema:"the machine, only when the project is on more than one"`
	Isolation string `json:"isolation,omitempty" jsonschema:"Default for a git repository: the worker gets its own git worktree on a new branch agent-net/<id>, so it never edits the user's checkout. none: it works in the project folder; use it for read-only or non-code tasks"`
	Base      string `json:"base,omitempty" jsonschema:"Existing local branch to start the worktree from."`
}

type handbackIn struct {
	Report string `json:"report" jsonschema:"your final report: what you did, what you found, absolute paths. It is the message; do not write report files"`
	Status string `json:"status,omitempty" jsonschema:"done (default), blocked (you need something you cannot get) or failed"`
}

type stopIn struct {
	Session string `json:"session" jsonschema:"the session_id of a worker you started"`
}

func addWorkerTools(s *mcp.Server, run func(context.Context, string, any) (any, error)) {
	tool := func(name, desc string) *mcp.Tool { return &mcp.Tool{Name: name, Description: desc} }

	mcp.AddTool(s, tool("agent_start",
		"Start a new agent session (a worker) in a project on one of your user's machines, and give it a task. "+
			"The project must be known (agent_project_list). The machine that holds it must have the harness, and a model must be in its model list (agent_nodes). "+
			"The worker gets your task as a message from you and answers with agent_reply: its answer arrives as a message in your session. "+
			workerNote+" A worker cannot start workers. The project's .agent-net/worker.md, if present, is added to the brief. "+
			"A codex worker has no agent-net tools: the last message of each of its turns is delivered to you as its report, and it takes messages from you and from your user only. "+
			"Defaults: the worker gets its own git worktree when the project is a git repository (isolation none opts out), and its notice ends with an agent-net checklist that tells you to read the diff and run the tests before you report. A short request is enough, for example \"implement X with an opencode worker using deepseek 4.1\": leave project empty to use the project you work in, and pass the model as your user said it."),
		func(ctx context.Context, _ *mcp.CallToolRequest, in startIn) (*mcp.CallToolResult, any, error) {
			if in.Harness == "" {
				in.Harness = "opencode"
			}
			out, err := run(ctx, protocol.OpWorkerStart, protocol.WorkerStartRequest{Project: in.Project, Machine: in.Machine,
				Harness: in.Harness, Model: in.Model, Task: in.Task, Isolation: in.Isolation, Base: in.Base})
			return nil, out, err
		})
	mcp.AddTool(s, tool("agent_handback",
		"Only for a worker that agent-net started: hand your final report to the agent that started you, and mark your work done. "+
			"Call it once when the task is finished, or with status blocked or failed when you cannot finish. "+
			"The caller receives the report with facts (status, run time, tokens, changes). Text you leave at the end of your run is not delivered. "+
			"You stay available for follow-up messages. If you were not started through agent-net, this fails: answer with agent_reply."),
		func(ctx context.Context, _ *mcp.CallToolRequest, in handbackIn) (*mcp.CallToolResult, any, error) {
			out, err := run(ctx, protocol.OpWorkerHandback, protocol.WorkerHandbackRequest{Report: in.Report, Status: in.Status})
			return nil, out, err
		})
	mcp.AddTool(s, tool("agent_stop",
		"Stop a worker that you started, by its session_id. Its process ends at once. The person at the command line can stop any worker."),
		func(ctx context.Context, _ *mcp.CallToolRequest, in stopIn) (*mcp.CallToolResult, any, error) {
			out, err := run(ctx, protocol.OpWorkerStop, protocol.WorkerStopRequest{Session: in.Session})
			return nil, out, err
		})
}
