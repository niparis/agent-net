package protocol

// The read tools of the tool redesign (docs/design/tool-redesign.md). These
// requests travel from the MCP server through the local daemon to the gateway,
// which does the filtering and paging where the data is.

// SessionMatch is one alternative of match_any: its populated fields are ANDed.
type SessionMatch struct {
	Harness string `json:"harness,omitempty"`
	Model   string `json:"model,omitempty"`
}

// SessionsFindRequest is agent_sessions_find. Caller is the session that made
// the call; the daemon sets it and the gateway binds the cursor to it.
type SessionsFindRequest struct {
	Machine  string         `json:"machine,omitempty"`
	Project  string         `json:"project,omitempty"`
	Harness  string         `json:"harness,omitempty"`
	Model    string         `json:"model,omitempty"`
	Kind     []string       `json:"kind,omitempty"`
	Online   string         `json:"online,omitempty"`
	Text     string         `json:"text,omitempty"`
	MatchAny []SessionMatch `json:"match_any,omitempty"`
	Detail   string         `json:"detail,omitempty"`
	Limit    int            `json:"limit,omitempty"`
	Cursor   string         `json:"cursor,omitempty"`
	Caller   string         `json:"caller,omitempty"`
}

// SessionGetRequest is agent_session_get. Session is an ID or an exact name.
type SessionGetRequest struct {
	Session string `json:"session"`
	Caller  string `json:"caller,omitempty"`
}

// MachinesFindRequest is agent_machines_find.
type MachinesFindRequest struct {
	Online string `json:"online,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
	Detail string `json:"detail,omitempty"`
	Caller string `json:"caller,omitempty"`
}

// MachineGetRequest is agent_machine_get. Include holds at most one category.
type MachineGetRequest struct {
	Machine     string   `json:"machine"`
	Sort        string   `json:"sort,omitempty"`
	Include     []string `json:"include,omitempty"`
	Harness     string   `json:"harness,omitempty"`
	ModelFilter string   `json:"model_filter,omitempty"`
	Limit       int      `json:"limit,omitempty"`
	Cursor      string   `json:"cursor,omitempty"`
	Caller      string   `json:"caller,omitempty"`
}

// ProjectsFindRequest is agent_projects_find.
type ProjectsFindRequest struct {
	Name    string `json:"name,omitempty"`
	Machine string `json:"machine,omitempty"`
	Text    string `json:"text,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	Cursor  string `json:"cursor,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Caller  string `json:"caller,omitempty"`
}

// SessionRow is one row of agent_sessions_find and the session of
// agent_session_get. The required fields are always present; the optional
// ones are added by detail "full".
type SessionRow struct {
	AgentID          string  `json:"agent_id,omitempty"`
	AgentName        string  `json:"agent_name,omitempty"`
	RepoLead         bool    `json:"repo_lead,omitempty"`
	RepoLeadExplicit bool    `json:"repo_lead_explicit,omitempty"`
	TeamLead         bool    `json:"team_lead,omitempty"`
	RepoMaxAgents    int     `json:"repo_max_agents,omitempty"`
	RepoAgentCount   int     `json:"repo_agent_count,omitempty"`
	RepoOverMax      bool    `json:"repo_over_max,omitempty"`
	Type             string  `json:"type,omitempty"`
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	Kind             string  `json:"kind"`
	Machine          string  `json:"machine"`
	Project          string  `json:"project"`
	Harness          string  `json:"harness"`
	Model            string  `json:"model"`
	Online           bool    `json:"online"`
	Status           string  `json:"status"`
	Availability     string  `json:"availability,omitempty"`
	Task             string  `json:"task"`
	LastSeen         string  `json:"last_seen"`
	TaskStarted      *string `json:"task_started"`
	// Waiting is set while the session waits for its user. Status then says
	// StatusWaitingForUser.
	Waiting *WaitingRow `json:"waiting,omitempty"`
	// Tools is "disconnected" when the session has lost its agent-net tools,
	// so it cannot reply with agent tools. Absent otherwise.
	Tools string `json:"tools,omitempty"`

	LastSeenAgo  string            `json:"last_seen_ago,omitempty"`
	Folder       string            `json:"folder,omitempty"`
	Branch       string            `json:"branch,omitempty"`
	Remote       string            `json:"remote,omitempty"`
	Function     string            `json:"function,omitempty"`
	Expertise    string            `json:"expertise,omitempty"`
	Capabilities []string          `json:"capabilities,omitempty"`
	Purpose      string            `json:"purpose,omitempty"`
	Charter      *ReadCharter      `json:"charter,omitempty"`
	Rules        map[string]string `json:"rules,omitempty"`
	Delegation   string            `json:"delegation,omitempty"`
	Worker       *SessionWorker    `json:"worker,omitempty"`
	// Loop is the standing loop of the session's node (decision 0094).
	Loop *LoopView `json:"loop,omitempty"`
}

// WaitingRow says that a session waits for its user: the kind (permission,
// question or input) and when the wait began (RFC 3339). The question itself
// is for the owner only and is never in an agent read.
type WaitingRow struct {
	Kind  string `json:"kind"`
	Since string `json:"since"`
}

// ReadCharter is the agent-facing charter subset carried by full read rows.
// Accepts and refuses are lists split on the charter's semicolon separators.
type ReadCharter struct {
	Maturity        string          `json:"maturity,omitempty"`
	MaturityNote    string          `json:"maturity_note,omitempty"`
	Escalate        string          `json:"escalate,omitempty"`
	Rules           string          `json:"rules,omitempty"`
	Sources         *CharterSources `json:"sources,omitempty"`
	Purpose         string          `json:"purpose,omitempty"`
	PurposeFallback bool            `json:"purpose_fallback,omitempty"`
	Accepts         []string        `json:"accepts,omitempty"`
	Refuses         []string        `json:"refuses,omitempty"`
	FeatureRequests string          `json:"feature_requests,omitempty"`
	// WorkMode is the effective work mode when a level sets one: workers_code
	// or on_request. Sources["work_mode"] says which level set it.
	WorkMode string `json:"work_mode,omitempty"`
}

// SessionWorker is the worker part of a full session row.
type SessionWorker struct {
	ID         string        `json:"id"`
	State      string        `json:"state"`
	Model      string        `json:"model"`
	Difficulty string        `json:"difficulty"`
	Caller     SessionRefRow `json:"caller"`
	RunTime    string        `json:"run_time"`
}

// SessionRefRow names the other end of a session relation.
type SessionRefRow struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// SessionsPage answers agent_sessions_find.
type SessionsPage struct {
	Items      []SessionRow `json:"items"`
	Total      int          `json:"total"`
	NextCursor string       `json:"next_cursor,omitempty"`
	Note       string       `json:"note,omitempty"`
}

// SessionGetResult answers agent_session_get.
type SessionGetResult struct {
	Session SessionRow `json:"session"`
}

// MachineRow is one row of agent_machines_find. OS and LastSeen are optional
// fields, added by detail "full".
type MachineRow struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Online bool   `json:"online"`
	// State is "online", "asleep" or "silent". Since says when an asleep or
	// silent machine last connected, or when it went silent (RFC 3339).
	State          string       `json:"state"`
	Since          string       `json:"since,omitempty"`
	Version        string       `json:"version"`
	Harnesses      []HarnessRow `json:"harnesses"`
	SessionsOnline int          `json:"sessions_online"`
	WorkersRunning int          `json:"workers_running"`

	OS       string `json:"os,omitempty"`
	LastSeen string `json:"last_seen,omitempty"`
}

// HarnessRow is one harness installed on a machine.
type HarnessRow struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// MachinesPage answers agent_machines_find.
type MachinesPage struct {
	Items      []MachineRow `json:"items"`
	Total      int          `json:"total"`
	NextCursor string       `json:"next_cursor,omitempty"`
	Note       string       `json:"note,omitempty"`
}

// MachineModelRow is one model in the models category of agent_machine_get.
// Scores lists the capability score at each matching effort, when the score
// table has one.
type MachineModelRow struct {
	Harness string             `json:"harness"`
	ID      string             `json:"id"`
	Scores  []ModelEffortScore `json:"scores,omitempty"`
}

// MachineModelPage pages the models category.
type MachineModelPage struct {
	Items      []MachineModelRow `json:"items"`
	Total      int               `json:"total"`
	NextCursor string            `json:"next_cursor,omitempty"`
	Note       string            `json:"note,omitempty"`
}

// MachineQuotaRow is one quota window of a machine.
type MachineQuotaRow struct {
	Provider    string  `json:"provider"`
	Window      string  `json:"window"`
	PercentUsed float64 `json:"percent_used"`
	ResetsAt    string  `json:"resets_at"`
}

// MachineQuotaPage pages the quotas category.
type MachineQuotaPage struct {
	Items      []MachineQuotaRow `json:"items"`
	Total      int               `json:"total"`
	NextCursor string            `json:"next_cursor,omitempty"`
	Note       string            `json:"note,omitempty"`
}

// MachineTrackRow is one track record of a machine.
type MachineTrackRow struct {
	EarlyQuits    int     `json:"early_quits"`
	Harness       string  `json:"harness"`
	Model         string  `json:"model"`
	Runs          int     `json:"runs"`
	Complete      int     `json:"complete"`
	Partial       int     `json:"partial"`
	Failed        int     `json:"failed"`
	MedianMinutes float64 `json:"median_minutes,omitempty"`
}

// MachineTrackPage pages the track_record category.
type MachineTrackPage struct {
	Items      []MachineTrackRow `json:"items"`
	Total      int               `json:"total"`
	NextCursor string            `json:"next_cursor,omitempty"`
	Note       string            `json:"note,omitempty"`
}

// MachinePlaybookRow is one playbook note of a machine.
type MachinePlaybookRow struct {
	Subject string `json:"subject"`
	Note    string `json:"note"`
}

// MachinePlaybookPage pages the playbook category.
type MachinePlaybookPage struct {
	Items      []MachinePlaybookRow `json:"items"`
	Total      int                  `json:"total"`
	NextCursor string               `json:"next_cursor,omitempty"`
	Note       string               `json:"note,omitempty"`
}

// MachineHealth is the daemon's most recent diagnostic report. Disk space may
// be absent when the filesystem could not be inspected.
type MachineHealth struct {
	Status         string            `json:"status"`
	RestartTime    string            `json:"restart_time,omitempty"`
	ReportedAt     string            `json:"reported_at,omitempty"`
	UptimeSeconds  *int64            `json:"uptime_seconds,omitempty"`
	Path           string            `json:"path,omitempty"`
	Helpers        map[string]string `json:"helpers,omitempty"`
	StateFreeBytes *uint64           `json:"state_free_bytes,omitempty"`
}

// MachineProcessRow adds process ownership and the stop refusal to a sample.
type MachineProcessRow struct {
	MachineProcRow
	User    string `json:"user"`
	Refusal string `json:"refusal,omitempty"`
}

// MachineProcessPage is one page of the target machine process table.
type MachineProcessPage struct {
	Items      []MachineProcessRow `json:"items"`
	Total      int                 `json:"total"`
	NextCursor string              `json:"next_cursor,omitempty"`
	// Roster says whether the Claude session roster was fresh when the table was read.
	Roster *ClaudeRosterStatus `json:"claude_roster,omitempty"`
}

// MachineProcessReply is what a daemon answers to node.machine_processes. An
// older daemon answers with the bare list of rows.
type MachineProcessReply struct {
	Items  []MachineProcessRow `json:"items"`
	Roster *ClaudeRosterStatus `json:"claude_roster,omitempty"`
}

// Values of ClaudeRosterStatus.State.
const (
	// ClaudeRosterFresh: the roster can be trusted to list every live Claude
	// background session.
	ClaudeRosterFresh = "fresh"
	// ClaudeRosterStale: an old roster with no supervisor to vouch for it.
	ClaudeRosterStale = "stale"
	// ClaudeRosterUnverified: the supervisor named in the roster is not alive,
	// not ours, or started after the roster was written.
	ClaudeRosterUnverified = "unverified"
	// ClaudeRosterMissing: the roster file is absent or unreadable.
	ClaudeRosterMissing = "missing"
)

// ClaudeRosterStatus is the state of Claude Code's session roster, the file its
// supervisor writes when its background sessions change. While it is not fresh,
// agent-net cannot tell which Claude processes are live, so it refuses to stop
// any of them. A roster with a living supervisor is fresh however old its file
// is: the file is only rewritten when the sessions change.
type ClaudeRosterStatus struct {
	State string `json:"state"`
	Note  string `json:"note,omitempty"`
	// UpdatedAt is when the file was last written (RFC 3339, UTC), and
	// AgeSeconds how long ago that was. Both are empty when there is no file.
	UpdatedAt     string `json:"updated_at,omitempty"`
	AgeSeconds    int64  `json:"age_s,omitempty"`
	SupervisorPID int    `json:"supervisor_pid,omitempty"`
	// Sessions is how many background sessions the file lists.
	Sessions int `json:"sessions"`
}

// MachineGetResult answers agent_machine_get with the requested category.
type MachineGetResult struct {
	Processes *MachineProcessPage `json:"processes,omitempty"`
	Machine   MachineRow          `json:"machine"`
	Health    MachineHealth       `json:"health"`
	// Usage is set when no category is asked for.
	Usage *MachineUsageNow `json:"usage,omitempty"`
	// Requests is set with Usage. It is the gateway's own request counters,
	// not the machine's.
	Requests    *GatewayRequests     `json:"requests,omitempty"`
	Models      *MachineModelPage    `json:"models,omitempty"`
	Quotas      *MachineQuotaPage    `json:"quotas,omitempty"`
	TrackRecord *MachineTrackPage    `json:"track_record,omitempty"`
	Playbook    *MachinePlaybookPage `json:"playbook,omitempty"`
}

// ProjectRow is one row of agent_projects_find. Remote and DefaultBranch are
// optional fields, added by detail "full".
type ProjectRow struct {
	Name           string `json:"name"`
	DisplayName    string `json:"display_name"`
	Machine        string `json:"machine"`
	Folder         string `json:"folder"`
	SessionsOnline int    `json:"sessions_online"`
	SessionsTotal  int    `json:"sessions_total"`
	Source         string `json:"source"`

	Remote        string `json:"remote,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
}

// ProjectsPage answers agent_projects_find.
type ProjectsPage struct {
	Items      []ProjectRow `json:"items"`
	Total      int          `json:"total"`
	NextCursor string       `json:"next_cursor,omitempty"`
	Note       string       `json:"note,omitempty"`
}

// Status values of MachineUsageNow.
const (
	MachineUsageOK          = "ok"
	MachineUsageOffline     = "offline"
	MachineUsageUnavailable = "unavailable"
)

// MachineUsageNow is the load and the busiest processes of one machine, read
// from the daemon's own sampler (decision 0007). Status is "ok", "offline" or
// "unavailable"; Note then says why. Memory is in bytes.
type MachineUsageNow struct {
	Status     string `json:"status"`
	Note       string `json:"note,omitempty"`
	AgeSeconds int    `json:"age_s"`
	Cores      int    `json:"cores,omitempty"`
	// Load is the 1, 5 and 15 minute load average.
	Load []float64 `json:"load,omitempty"`
	// CPUPct is the share of all cores in use over the last interval, 0 to 100.
	CPUPct float64 `json:"cpu_pct"`
	// Split is user, sys, nice, idle, iowait and steal in percent of all
	// cores. It is empty on a platform that gives no split (macOS).
	Split     *MachineCPUSplit `json:"cpu_split,omitempty"`
	MemUsed   uint64           `json:"mem_used,omitempty"`
	MemTotal  uint64           `json:"mem_total,omitempty"`
	SwapUsed  uint64           `json:"swap_used,omitempty"`
	SwapTotal uint64           `json:"swap_total,omitempty"`
	// Top lists the processes that used the most CPU, busiest first.
	Top []MachineProcRow `json:"top,omitempty"`
	// Roster is the state of Claude Code's session roster on this machine.
	Roster *ClaudeRosterStatus `json:"claude_roster,omitempty"`
}

// MachineCPUSplit is the CPU time by kind, in percent of all cores.
type MachineCPUSplit struct {
	User   float64 `json:"user"`
	Sys    float64 `json:"sys"`
	Nice   float64 `json:"nice"`
	Idle   float64 `json:"idle"`
	IOWait float64 `json:"iowait"`
	Steal  float64 `json:"steal"`
}

// Kinds of MachineProcRow.OwnerKind.
const (
	ProcOwnerSession = "session"
	ProcOwnerWorker  = "worker"
	ProcOwnerDaemon  = "daemon"
	ProcOwnerGateway = "gateway"
	ProcOwnerMCP     = "mcp"
	ProcOwnerOther   = "other"
)

// MachineProcRow is one busy process. CPU is a percent of one core: 100 is one
// full core. Cmd is the command line, cut to 120 characters with secrets masked.
// Owner is the session or worker id (OwnerID) and name (Owner) whose process
// tree holds the process, or "agent-net daemon", "agent-net gateway", "mcp
// server <name>" or "other". OwnerRule is the fact that gave the owner, such
// as "cwd in worker worktree" or "Claude roster pid". It is empty for "other".
type MachineProcRow struct {
	PID            int     `json:"pid"`
	PPID           int     `json:"ppid"`
	CPU            float64 `json:"cpu"`
	RSS            uint64  `json:"rss"`
	ElapsedSeconds int64   `json:"elapsed_s,omitempty"`
	Cmd            string  `json:"cmd"`
	OwnerKind      string  `json:"owner_kind"`
	Owner          string  `json:"owner"`
	OwnerID        string  `json:"owner_id,omitempty"`
	OwnerRule      string  `json:"owner_rule,omitempty"`
}

// MachineUsageTopN is how many processes MachineUsageNow lists.
const MachineUsageTopN = 10

// MachineUsageMaxAgeSeconds is how old a reading may be before the daemon takes a
// new one for agent_machine_get.
const MachineUsageMaxAgeSeconds = 10
