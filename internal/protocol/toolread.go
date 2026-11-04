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
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Kind         string  `json:"kind"`
	Machine      string  `json:"machine"`
	Project      string  `json:"project"`
	Harness      string  `json:"harness"`
	Model        string  `json:"model"`
	Online       bool    `json:"online"`
	Status       string  `json:"status"`
	Availability string  `json:"availability,omitempty"`
	Task         string  `json:"task"`
	LastSeen     string  `json:"last_seen"`
	TaskStarted  *string `json:"task_started"`
	// Waiting is set while the session waits for its user. Status then says
	// StatusWaitingForUser.
	Waiting *WaitingRow `json:"waiting,omitempty"`

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
	Purpose         string   `json:"purpose,omitempty"`
	PurposeFallback bool     `json:"purpose_fallback,omitempty"`
	Accepts         []string `json:"accepts,omitempty"`
	Refuses         []string `json:"refuses,omitempty"`
	FeatureRequests string   `json:"feature_requests,omitempty"`
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
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Online         bool         `json:"online"`
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
type MachineModelRow struct {
	Harness string `json:"harness"`
	ID      string `json:"id"`
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

// MachineGetResult answers agent_machine_get: the machine and the one
// requested category.
type MachineGetResult struct {
	Machine     MachineRow           `json:"machine"`
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
