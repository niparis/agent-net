package protocol

// The per-turn policy call (decision 0014). Every turn, the harness adapter
// asks the daemon for the context of its session: the charter, the notes index
// and a notice. The owner's delegation policy (per session or per project) was
// removed on 2026-11-21, so the answer holds no delegation text.

// Permission level of unattended workers (decision 0020). The owner sets it per
// project; sandboxed is the only level.
const WorkersSandboxed = "sandboxed" // writes only in the worktree, the temp folder and the main repository's git dir; network on

// WorkersLevels lists the levels a form shows.
var WorkersLevels = []string{WorkersSandboxed}

// WorkerPermissionsOrDefault maps "" and a level retired from an older record
// to the default.
func WorkerPermissionsOrDefault(s string) string {
	if s != WorkersSandboxed {
		return WorkersSandboxed
	}
	return s
}

// MethodPolicy is the local method of an adapter that asks for its own
// session's context. It is not an operator method.
const MethodPolicy = "policy"

// MethodCompaction is the local method of the Claude Code hook that says a
// session's transcript was compacted. The daemon sends the charter block
// again on the next prompt, because the summary may have dropped it.
const MethodCompaction = "compaction"

// CompactionRequest is MethodCompaction. The Claude Code hook names the
// session it runs for.
type CompactionRequest struct {
	Harness  string `json:"harness,omitempty"`
	NativeID string `json:"native_id,omitempty"`
}

// CharterChangedNotice is the one line before a charter block that changed
// since the last one the session saw. The hook sends the full new text after
// it, because the model must see the values, not only that they changed.
const CharterChangedNotice = "[agent-net] The charter changed since it was last sent. The full text follows."

// PolicyGet is the gateway frame "policy.get": the effective policy of one
// session. The answer is PolicyGot.
type PolicyGet struct {
	SessionID string `json:"session_id"`
	// AgentsHash is the hash of the session's AGENTS.md, read by the daemon on
	// the session's machine. Only the per-turn path sends it; the gateway uses
	// it to ask for a charter draft once per version of the file (decision 0088).
	AgentsHash string `json:"agents_hash,omitempty"`
	// Turn says the answer is for a model turn, so it may carry the one-time
	// charter draft request. Other callers of policy.get leave it false.
	Turn bool `json:"turn,omitempty"`
}

// PolicyGot answers "policy.get".
type PolicyGot struct {
	Harness string `json:"harness"`
	// Project is the node the session speaks for, so the daemon can read its
	// charter from the graph (decision 0022).
	Project string `json:"project,omitempty"`
	// Charter is the node's per-turn context, filled by the daemon after it
	// read the graph. Empty when the node has no charter.
	Charter string `json:"charter,omitempty"`
	// Notes is the titles of the session's gateway notes (decision 0158). It
	// is kept apart from Charter, which is hashed to decide when to send it
	// again. Empty when the session has no notes.
	Notes string `json:"notes,omitempty"`
	// DraftRequest is the one-time request to draft the charter from AGENTS.md
	// (decision 0088). It is set once, for a turn, and is never cached.
	DraftRequest string `json:"draft_request,omitempty"`
	// Scopes are the scopes of the role node the session speaks for (a worker
	// speaks for its caller's node). ScopeRepos maps a team: or agent: over
	// value to the repos it covers. The daemon answers tool_decide from them
	// (decision 0165) and keeps them with the cached policy.
	Scopes     []Scope             `json:"scopes,omitempty"`
	ScopeRepos map[string][]string `json:"scope_repos,omitempty"`
}

// PolicyRequest is MethodPolicy. A plugin is bound to its session and leaves
// the fields empty. The Claude Code hook (the command line) names the session.
type PolicyRequest struct {
	Harness  string `json:"harness,omitempty"`
	NativeID string `json:"native_id,omitempty"`
	Prompt   string `json:"prompt,omitempty"`
	// Event is "Stop" when a Claude Code turn ends; empty for a turn start.
	// LastMessage is then the turn's final assistant text.
	Event       string `json:"event,omitempty"`
	LastMessage string `json:"last_message,omitempty"`
	// PromptID and TranscriptPath are the Claude Code hook's prompt_id and
	// transcript_path. The daemon reads the transcript entry of the prompt to
	// tell whether the owner typed it or a schedule or a peer sent it.
	PromptID       string `json:"prompt_id,omitempty"`
	TranscriptPath string `json:"transcript_path,omitempty"`
}

// PolicyResult answers MethodPolicy. Charter is the node's charter context for
// this turn (decision 0022); it is empty when the node has none. Notice is one
// line for the turn text when the session's agent-net tools are disconnected
// (ToolsDisconnectedNotice).
type PolicyResult struct {
	Charter string `json:"charter,omitempty"`
	Notice  string `json:"notice,omitempty"`
}
