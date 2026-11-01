package mcpserver

// The worker tool inputs the old agent_start and agent_stop names keep. The
// new tools take the catalog's schemas; the aliases take these.

type startIn struct {
	Touches      []string `json:"touches,omitempty" jsonschema:"Areas touched: db, hot-path, ui, docs, tests, config or other."`
	Project      string   `json:"project,omitempty" jsonschema:"The name of a project from agent_project_list, or the absolute path of its folder (use a path when two folders share a name). Leave it empty to use the folder you are working in (on the machine that has the harness)"`
	Task         string   `json:"task,omitempty" jsonschema:"what the worker should do, with everything it needs to know; it cannot ask you questions except by agent_reply. Optional only with continue"`
	Harness      string   `json:"harness,omitempty" jsonschema:"claude, opencode, pi or codex; empty lets automatic selection choose a harness (with continue, the old worker's harness). agent_nodes shows which are installed on the machine. A pi worker needs isolation worktree."`
	Difficulty   string   `json:"difficulty,omitempty" jsonschema:"Task difficulty for automatic model selection; defaults to normal."`
	Model        string   `json:"model,omitempty" jsonschema:"The model, as the harness names it. A loose name works for every harness (opencode, claude, pi, codex): agent-net resolves it against the model list of the machine that runs the worker and tells you the result, for example luna 6 -> gpt-6-luna (exact ids are shown by agent_nodes). An exact id wins. Claude also takes an alias such as haiku, sonnet, opus or fable. Pi keeps a :thinking suffix such as :high, and Codex keeps an :effort suffix such as :high. If several models match equally, the error lists them; if none matches, it lists the closest names. Empty or auto selects a registered worker profile by task difficulty and quota; an empty roster uses the harness default."`
	Machine      string   `json:"machine,omitempty" jsonschema:"the machine, only when the project is on more than one"`
	Isolation    string   `json:"isolation,omitempty" jsonschema:"Default for a git repository: the worker gets its own git worktree on a new branch agent-net/<id>, so it never edits the user's checkout. none: it works in the project folder; use it for read-only or non-code tasks"`
	Base         string   `json:"base,omitempty" jsonschema:"Existing local branch to start the worktree from."`
	Continue     string   `json:"continue,omitempty" jsonschema:"The session_id or worker ID of an ended worker that you started. The new worker runs on the same machine, in the same project, with the same harness and model unless you give others, in a new worktree on a new branch made from the old worker's branch (a worker that ended with no hand-back has its unfinished changes saved as the last commit there), and gets the old worker's full first task. task is then optional and is added as an extra. project, machine, base and isolation must be empty."`
	AutoContinue *bool    `json:"auto_continue,omitempty" jsonschema:"Default true. When the worker stops early (a hand-back with complete: false, or idle without a hand-back), agent-net sends it up to 2 continues itself before it tells you, and does not count them as your rework. Set false to be told at once."`
}

type handbackIn struct {
	Report   string `json:"report" jsonschema:"your final report: what you did, what you found, absolute paths. It is the message; do not write report files"`
	Status   string `json:"status,omitempty" jsonschema:"done (default), blocked (you need something you cannot get) or failed"`
	Complete *bool  `json:"complete,omitempty" jsonschema:"true when you did the whole task (the default). Set it to false when you hand back partial work, so the record shows it"`
}

type stopIn struct {
	Session string `json:"session" jsonschema:"the session_id of a worker you started, or the queue_id of a queued start"`
}
