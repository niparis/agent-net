package mcpserver

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

// The worker tools (decision 0002). An agent starts a worker in a project and
// stops its own workers. The daemon enforces the rules: a worker cannot start
// workers, the machine must accept them, and a start waits in a queue when the
// machine has no room (decision 0018).

const workerNote = "A worker is an agent session that agent-net starts for you with nobody watching it. It uses the project's own permission settings. " +
	"It ends on its own after 60 minutes without messages, after 4 hours, or when you end. Only start one when your user asks."

type startIn struct {
	Project   string `json:"project,omitempty" jsonschema:"The name of a project from agent_project_list, or the absolute path of its folder (use a path when two folders share a name). Leave it empty to use the folder you are working in (on the machine that has the harness)"`
	Task      string `json:"task,omitempty" jsonschema:"what the worker should do, with everything it needs to know; it cannot ask you questions except by agent_reply. Optional only with continue"`
	Harness   string `json:"harness,omitempty" jsonschema:"opencode (the default; with continue, the old worker's harness), claude, pi or codex. agent_nodes shows which are installed on the machine. A pi worker needs isolation worktree."`
	Model     string `json:"model,omitempty" jsonschema:"The model, as the harness names it. A loose name works for every harness (opencode, claude, pi, codex): agent-net resolves it against the model list of the machine that runs the worker and tells you the result, for example luna 6 -> gpt-6-luna (exact ids are shown by agent_nodes). An exact id wins. Claude also takes an alias such as haiku, sonnet, opus or fable. Pi keeps a :thinking suffix such as :high. If several models match equally, the error lists them; if none matches, it lists the closest names. Empty means the harness default."`
	Machine   string `json:"machine,omitempty" jsonschema:"the machine, only when the project is on more than one"`
	Isolation string `json:"isolation,omitempty" jsonschema:"Default for a git repository: the worker gets its own git worktree on a new branch agent-net/<id>, so it never edits the user's checkout. none: it works in the project folder; use it for read-only or non-code tasks"`
	Base      string `json:"base,omitempty" jsonschema:"Existing local branch to start the worktree from."`
	Continue  string `json:"continue,omitempty" jsonschema:"The session_id or worker ID of an ended worker that you started. The new worker runs on the same machine, in the same project, with the same harness and model unless you give others, in a new worktree on a new branch made from the old worker's branch (a worker that ended with no hand-back has its unfinished changes saved as the last commit there), and gets the old worker's full first task. task is then optional and is added as an extra. project, machine, base and isolation must be empty."`
}

type handbackIn struct {
	Report string `json:"report" jsonschema:"your final report: what you did, what you found, absolute paths. It is the message; do not write report files"`
	Status string `json:"status,omitempty" jsonschema:"done (default), blocked (you need something you cannot get) or failed"`
}

type stopIn struct {
	Session string `json:"session" jsonschema:"the session_id of a worker you started, or the queue_id of a queued start"`
}

func addWorkerTools(s *mcp.Server, run func(context.Context, string, any) (any, error)) {
	tool := func(name, desc string) *mcp.Tool { return &mcp.Tool{Name: name, Description: desc} }

	mcp.AddTool(s, tool("agent_start",
		"Start a new agent session (a worker) in a project on one of your user's machines, and give it a task. "+
			"The project must be known (agent_project_list). The machine that holds it must have the harness, and a model must be in that harness's model list (agent_nodes; an exact id is used as given). "+
			"The owner sets a worker level per project, and the result shows it. "+
			"The worker gets your task as a message from you and answers with agent_reply: its answer arrives as a message in your session. "+
			workerNote+" A worker cannot start workers. The project's .agent-net/worker.md, if present, is added to the brief."+
			" If the machine has no room (CPU or memory is busy), the start is queued, not refused: the answer has status queued, a queue_id, the place in the queue and the reason, and a worker notice arrives in your session when the worker starts (or when it cannot). Cancel a queued start with agent_stop and its queue_id."+
			" A codex worker has no agent-net tools: the last message of each of its turns is delivered to you as its report, and it takes messages from you and from your user only. "+
			"When a worker ends with no hand-back (stopped, failed, timed out, lost, owner gone) and its worktree has uncommitted changes, the machine commits them on the worker's branch and the notice says so. "+
			"To go on with such a worker, call agent_start with continue set to its session_id: the new worker starts from that branch with the old first task, so nothing is lost. A continued start can be queued like any start. Only the session that started a worker can continue it, and its machine must be online. "+
			"Defaults: the worker gets its own git worktree when the project is a git repository (isolation none opts out), and its notice ends with an agent-net checklist that tells you to read the diff and run the tests before you report. A short request is enough, for example \"implement X with an opencode worker using deepseek 4.1\": leave project empty to use the project you work in, and pass the model as your user said it."),
		func(ctx context.Context, _ *mcp.CallToolRequest, in startIn) (*mcp.CallToolResult, any, error) {
			if in.Harness == "" && in.Continue == "" {
				in.Harness = "opencode"
			}
			out, err := run(ctx, protocol.OpWorkerStart, protocol.WorkerStartRequest{Project: in.Project, Machine: in.Machine,
				Harness: in.Harness, Model: in.Model, Task: in.Task, Isolation: in.Isolation, Base: in.Base, Continue: in.Continue})
			if err != nil {
				return nil, nil, err
			}
			if m, ok := out.(map[string]any); ok {
				level, _ := m["permissions"].(string)
				harness, _ := m["harness"].(string)
				raw, _ := json.Marshal(out)
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw) + "\n" + protocol.WorkerPermissionsText(level, harness)}}}, out, nil
			}
			return nil, out, nil
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
		"Stop a worker that you started, by its session_id. Its process ends at once. A start that is still queued (status queued) is cancelled with its queue_id. The person at the command line can stop any worker."),
		func(ctx context.Context, _ *mcp.CallToolRequest, in stopIn) (*mcp.CallToolResult, any, error) {
			out, err := run(ctx, protocol.OpWorkerStop, protocol.WorkerStopRequest{Session: in.Session})
			return nil, out, err
		})
}
