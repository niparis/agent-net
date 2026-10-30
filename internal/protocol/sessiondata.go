package protocol

// This file holds the types for the data a session owner can ask for: how
// much CPU and memory each machine and session uses, what happens inside a
// session, and how many sessions are online. See decision 0007.

// ---- CPU and memory ----

// UsageSampleSeconds and UsageWindowSeconds are how often a daemon samples and
// how far back its averages go. UsageReportSeconds is how often it reports to
// the gateway.
const (
	UsageSampleSeconds = 5
	UsageWindowSeconds = 300
	UsageReportSeconds = 10
	// UsageStaleSeconds is the age after which a reader treats a report as
	// unknown (a few missed reports).
	UsageStaleSeconds = 45
)

// MachineUsage is what a daemon reports about its machine. The "Avg" figures
// cover the last UsageWindowSeconds.
type MachineUsage struct {
	Cores int `json:"cores"`
	// CPU is the share of all cores in use, 0 to 100.
	CPU     float64 `json:"cpu"`
	CPUAvg  float64 `json:"cpu_avg"`
	MemUsed uint64  `json:"mem_used"` // bytes
	// MemUsedAvg is the average of MemUsed over the window.
	MemUsedAvg uint64     `json:"mem_used_avg"`
	MemTotal   uint64     `json:"mem_total"`
	Load       [3]float64 `json:"load"` // 1, 5 and 15 minutes
	Procs      int        `json:"procs"`
	// Samples is how many CPU samples the averages cover. 0 means the daemon
	// has no CPU figure yet.
	Samples int `json:"samples"`
}

// TreeUsage is one process tree: a root process and all its descendants. It
// belongs to one session, except for OpenCode, where one service process hosts
// many sessions. Then Shared is true, Sessions lists them all, and the figures
// are for the whole service. They are never divided between the sessions.
type TreeUsage struct {
	PID      int      `json:"pid"`
	Harness  string   `json:"harness"`
	Sessions []string `json:"sessions"`
	Shared   bool     `json:"shared,omitempty"`
	// CPU is a percentage of one core: 100 is one full core, and a tree that
	// uses three cores shows 300.
	CPU     float64 `json:"cpu"`
	CPUAvg  float64 `json:"cpu_avg"`
	RSS     uint64  `json:"rss"` // bytes, summed over the tree
	RSSAvg  uint64  `json:"rss_avg"`
	Procs   int     `json:"procs"`
	Samples int     `json:"samples"`
}

// UsageReport is the "node.usage" request: a daemon's report to the gateway,
// about every UsageReportSeconds. Small by design; the top lists are not in it.
type UsageReport struct {
	Machine MachineUsage `json:"machine"`
	Trees   []TreeUsage  `json:"trees"`
}

// UsageNode is one machine in the answer to "query.usage".
type UsageNode struct {
	NodeID   string `json:"node_id"`
	Hostname string `json:"hostname"`
	// AgeSeconds is the time since the machine's last report. A machine that
	// sent none (an older agent-net) is not listed.
	AgeSeconds int64       `json:"age_s"`
	Usage      UsageReport `json:"usage"`
}

// UsageAnswer is the answer to "query.usage".
type UsageAnswer struct {
	Nodes []UsageNode `json:"nodes"`
}

// ProcUsage is one process in a top list.
type ProcUsage struct {
	PID int    `json:"pid"`
	Cmd string `json:"cmd"`
	// Args are the first 80 characters of the command line with secrets masked.
	Args string  `json:"args"`
	CPU  float64 `json:"cpu"` // percent of one core over the last sample interval
	RSS  uint64  `json:"rss"`
}

// UsageTop is the heaviest processes of one session's tree. It is only sent on
// request.
type UsageTop struct {
	SessionID string      `json:"session_id"`
	Tree      TreeUsage   `json:"tree"`
	ByCPU     []ProcUsage `json:"by_cpu"`
	ByRSS     []ProcUsage `json:"by_rss"`
}

// QueryUsageTop asks for the top lists of one session.
type QueryUsageTop struct {
	SessionID string `json:"session_id"`
}

// ---- what happens inside a session ----

// Text limits. They are the same for every harness. The daemon enforces them
// on what a harness returns, so a plugin cannot widen them.
const (
	TextDefaultLimit = 50
	TextMaxLimit     = 200
	// TextMaxEntry is the longest text of one entry, in characters.
	TextMaxEntry = 2000
)

// Roles of an entry.
const (
	RoleUser       = "user"
	RoleAssistant  = "assistant"
	RoleToolCall   = "tool_call"
	RoleToolResult = "tool_result"
)

// ValidRole tells whether s is a role of an entry.
func ValidRole(s string) bool {
	return s == RoleUser || s == RoleAssistant || s == RoleToolCall || s == RoleToolResult
}

// TextQuery asks for the recent conversation of one session. At most one of
// Before and After is set.
type TextQuery struct {
	SessionID string `json:"session_id"`
	// Limit is how many entries to return: TextDefaultLimit when 0, at most
	// TextMaxLimit.
	Limit int `json:"limit,omitempty"`
	// Before is the Older cursor of an earlier answer: return the entries
	// before it (paging back).
	Before string `json:"before,omitempty"`
	// After is the Newer cursor of an earlier answer: return the entries after
	// it (following).
	After string `json:"after,omitempty"`
	// Sidechain also returns the entries of sub-agents. Claude Code only.
	Sidechain bool `json:"sidechain,omitempty"`
}

// EnterRequest is OpSessionEnter: how can the owner take over the terminal for
// one session? (decision 0002, W16)
type EnterRequest struct {
	SessionID string `json:"session_id"`
	// TmuxSocket is the socket path of the tmux server the asking terminal
	// runs in (the part of $TMUX before the first comma), or "" when it is
	// not in tmux. With it, the plan can switch the owner's tmux client to the
	// session's pane instead of attaching a second client.
	TmuxSocket string `json:"tmux_socket,omitempty"`
}

// EnterPlan answers OpSessionEnter. A front end runs Argv in Dir with Env added
// and comes back when it ends. It never builds a command itself. When Argv is
// empty the session cannot be entered from here and Note says what to do.
type EnterPlan struct {
	Argv []string `json:"argv,omitempty"`
	Dir  string   `json:"dir,omitempty"`
	// Env is added to the environment of the command. It holds secrets of a
	// private worker server, so it only comes for a session on the asking
	// machine and never crosses the gateway.
	Env map[string]string `json:"env,omitempty"`
	// Host is the machine that holds the session.
	Host string `json:"host,omitempty"`
	// Note is one line for the owner: what the plan does, or why there is none.
	Note string `json:"note,omitempty"`
}

// TextEntry is one step of a session: a prompt, a reply, a tool call or a tool
// result.
type TextEntry struct {
	// Cursor identifies the entry. It is opaque.
	Cursor string `json:"cursor"`
	// Time is when it happened, in Unix milliseconds. 0 when unknown.
	Time int64  `json:"time"`
	Role string `json:"role"`
	// Tool is the tool name for a tool call and its result.
	Tool string `json:"tool,omitempty"`
	// Text has secrets masked and is cut to TextMaxEntry characters.
	Text string `json:"text"`
	// Cut is true when Text was cut.
	Cut bool `json:"cut,omitempty"`
	// Sidechain marks an entry of a sub-agent.
	Sidechain bool `json:"sidechain,omitempty"`
}

// TextPage is the answer: entries oldest first, newest last.
type TextPage struct {
	SessionID string      `json:"session_id,omitempty"`
	Harness   string      `json:"harness,omitempty"`
	Entries   []TextEntry `json:"entries"`
	// Older is the cursor to pass as Before to page back. Empty when the
	// start of the conversation is reached.
	Older string `json:"older,omitempty"`
	// Newer is the cursor to pass as After to get what comes next.
	Newer string `json:"newer,omitempty"`
}

// ---- how many sessions ----

// SessionCounts answers "query.counts": what a header needs, without listing
// every session. Offline sessions are kept for 30 days, so the total of
// sessions is mostly old ones; Offline24h is the number that matters.
type SessionCounts struct {
	Online int `json:"online"`
	// Offline24h is the sessions that are offline and were seen in the last
	// 24 hours.
	Offline24h int `json:"offline_24h"`
	// OfflineTotal is every offline session the gateway still keeps.
	OfflineTotal int `json:"offline_total"`
}
