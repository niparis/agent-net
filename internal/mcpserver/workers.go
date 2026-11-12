package mcpserver

// The worker start input the redesigned agent_worker_start tool fills from the
// catalog arguments.

type startIn struct {
	Provider     string   `json:"provider,omitempty" jsonschema:"Provider ID; omit to resolve from the model."`
	Effort       string   `json:"effort,omitempty" jsonschema:"Reasoning effort or thinking level."`
	Touches      []string `json:"touches,omitempty" jsonschema:"Deprecated and ignored: the level comes from the difficulty judge."`
	Project      string   `json:"project,omitempty" jsonschema:"The name of a project from agent_projects_find, or the absolute path of its folder (use a path when two folders share a name). Leave it empty to use the folder you are working in (on the machine that has the harness)"`
	Task         string   `json:"task,omitempty" jsonschema:"what the worker should do, with everything it needs to know; it cannot ask you questions except by agent_message_reply. Optional only with continue"`
	Problem      string   `json:"problem,omitempty" jsonschema:"What is wrong or wanted, in your owner's words, with who asked. Required for a new start."`
	Evidence     []string `json:"evidence,omitempty" jsonschema:"The facts you already have, one line each."`
	Done         []string `json:"done,omitempty" jsonschema:"Outcomes the worker can check itself, one line each. Required with problem. The accept and review tasks check exactly this list."`
	Hypothesis   string   `json:"hypothesis,omitempty" jsonschema:"Your guess at the cause or the fix. The worker may drop it if wrong."`
	Scope        string   `json:"scope,omitempty" jsonschema:"What not to touch and the known traps."`
	Title        string   `json:"title,omitempty" jsonschema:"One short line naming the worker, at most 60 characters, with no newlines. Leave it empty to derive a title from the task."`
	Harness      string   `json:"harness,omitempty" jsonschema:"claude, opencode, pi or codex; empty lets automatic selection choose a harness (with continue, the old worker's harness). agent_machines_find shows which are installed on the machine. A pi worker needs isolation worktree."`
	Difficulty   string   `json:"difficulty,omitempty" jsonschema:"Deprecated and ignored: the level comes from the difficulty judge."`
	Model        string   `json:"model,omitempty" jsonschema:"Model ID, loose name or worker profile; omit for automatic selection."`
	Machine      string   `json:"machine,omitempty" jsonschema:"the machine, only when the project is on more than one"`
	Isolation    string   `json:"isolation,omitempty" jsonschema:"Default for a git repository: the worker gets its own git worktree on a new branch agent-net/<id>, so it never edits the user's checkout. none: it works in the project folder; use it for read-only or non-code tasks"`
	Base         string   `json:"base,omitempty" jsonschema:"Existing local branch to start the worktree from."`
	Continue     string   `json:"continue,omitempty" jsonschema:"The session_id or worker ID of an ended worker that you started. The new worker runs on the same machine, in the same project, with the same harness and model unless you give others, in a new worktree on a new branch made from the old worker's branch (a worker that ended with no hand-back has its unfinished changes saved as the last commit there), and gets the old worker's full first task. task is then optional and is added as an extra. project, machine, base and isolation must be empty."`
	MCPServers   string   `json:"mcp_servers,omitempty" jsonschema:"approve or disable. For a claude worker: record that choice for the .mcp.json servers in its folder before it starts, on top of the choice copied from the project's main checkout. Use it when the start fails with mcp_approval_needed. Not with continue."`
	AutoContinue *bool    `json:"auto_continue,omitempty" jsonschema:"Default true. When the worker stops early (a hand-back with complete: false, or idle without a hand-back), agent-net sends it up to 2 continues itself before it tells you, and does not count them as your rework. Set false to be told at once."`
}
