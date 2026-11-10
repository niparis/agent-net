// Package protocol holds the types shared by the gateway, the daemon and the
// local clients.
package protocol

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Message states.
const (
	StateHeld       = "held"
	StateStored     = "stored"
	StateDispatched = "dispatched"
	StateDelivered  = "delivered"
	StateReplied    = "replied"
	StateFailed     = "failed"
	StateExpired    = "expired"
	// StateAcknowledged is a request closed by an ack: answered, but with
	// nothing delivered to the sender.
	StateAcknowledged = "acknowledged"
	// StateWithdrawn is a message its sender took back from the gateway
	// before it reached the recipient's harness (decision 0129).
	StateWithdrawn = "withdrawn"
)

// Message types.
const (
	TypeRequest      = "request"
	TypeReply        = "reply"
	TypeAck          = "ack"
	TypeNotification = "notification"
)

// LocalProtocol is the version of the local socket protocol (daemon <-> CLI,
// MCP and plugin clients). Clients send it in hello. It only grows when a
// change is not backward compatible; MinLocalProtocol is the oldest client
// the daemon still serves.
const (
	LocalProtocol    = 1
	MinLocalProtocol = 0 // a client that sends no protocol counts as 0
)

// Limits enforced by the gateway.
const (
	MaxHop          = 8
	MaxConvMessages = 50
	MaxBodyBytes    = 64 * 1024
	RatePerMinute   = 20
	DefaultTTL      = 24 * time.Hour
)

type Message struct {
	Overruled      bool   `json:"overruled,omitempty"`
	ID             string `json:"message_id"`
	ConversationID string `json:"conversation_id"`
	ParentID       string `json:"parent_message_id,omitempty"`
	From           string `json:"sender_session_id"`
	To             string `json:"recipient_session_id"`
	Type           string `json:"message_type"`
	Body           string `json:"body"`
	// WorkerTask is set by the gateway for a worker's initial brief. Nil
	// means the message predates this field.
	WorkerTask     *bool     `json:"worker_task,omitempty"`
	Hop            int       `json:"hop"`
	IdempotencyKey string    `json:"idempotency_key"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	State          string    `json:"state"`
	DeliveryDetail string    `json:"delivery_detail,omitempty"`
	FromName       string    `json:"sender_name"`
	// FromHarness is filled by the gateway so a receiving adapter can say
	// what kind of agent wrote the message.
	FromHarness string `json:"sender_harness,omitempty"`
	// FromWorker is true when the sender is a worker (a session that agent-net
	// started). A receiving adapter frames its text as model output.
	FromWorker    bool   `json:"sender_worker,omitempty"`
	FromCTO       bool   `json:"sender_cto,omitempty"`
	OwnerApproved bool   `json:"owner_approved,omitempty"`
	ApprovalNote  string `json:"approval_note,omitempty"`
	// RecipientTrust is filled by the gateway when it hands the message to
	// the recipient's daemon: true when the owner turned peer trust on for the
	// recipient session (decision 0012). A sender cannot set it: it is not a
	// field of MsgSend, and the gateway overwrites it on every delivery.
	RecipientTrust bool `json:"recipient_peer_trust,omitempty"`
	// RecipientMergeAuthority is the receiver's effective node setting, computed
	// by the gateway on delivery. A sender cannot supply it in MsgSend.
	RecipientMergeAuthority string `json:"recipient_merge_authority,omitempty"`
	RecipientMergeText      string `json:"recipient_merge_text,omitempty"`
	RecipientMergeTextSet   bool   `json:"recipient_merge_text_set,omitempty"`
	// RecipientRelation is filled by the gateway when it hands a request to
	// the recipient's daemon: the graph relation from the sender's node to the
	// recipient's node (decision 0022). It is agent-net's own text, after the
	// body, and a sender cannot set it.
	RecipientRelation string `json:"recipient_relation,omitempty"`
	OwnerInstruction  string `json:"owner_instruction,omitempty"`
	// IsOrder is true when the sender flagged the message as an order and the
	// gateway checked that the sender is above the recipient in the tree.
	IsOrder bool `json:"is_order,omitempty"`
	// Urgent is the sender's request to deliver this message ahead of older
	// held messages. It still counts toward the recipient's inbox limit
	// (decision 0129).
	Urgent bool `json:"urgent,omitempty"`
	// HeldForBusy is true while the gateway keeps this message because the
	// recipient is running a turn. It stays state stored until the gateway
	// releases it, alone or in a batch (decision 0129).
	HeldForBusy bool `json:"held_for_busy,omitempty"`
	// Batch carries the original messages when one held batch goes to a
	// recipient as one message, newest first. Empty on every other message.
	Batch []Message `json:"batch,omitempty"`
	// QueuePosition is the place of a waiting message in its recipient's
	// gateway queue, 1 being next, and QueueWaiting is how many messages wait
	// for that recipient. Both are filled on read, never stored.
	QueuePosition int `json:"queue_position,omitempty"`
	QueueWaiting  int `json:"queue_waiting,omitempty"`
	// BusyForSeconds is how long the recipient has been busy, filled on read
	// when the recipient is busy. Zero otherwise.
	BusyForSeconds int64 `json:"busy_for_seconds,omitempty"`
	// AuthorityNote is agent-net's label for an order, or for a reply from
	// above (advice). The gateway fills it on every delivery from the live
	// tree, and a sender cannot set it (decision 0089).
	AuthorityNote string `json:"authority_note,omitempty"`
	// CitedNotes are agent-net's notes on the messages this body cites by ID
	// (decision 0121). The gateway fills them, only for messages the sender
	// was part of, and overwrites whatever a sender supplied.
	CitedNotes []string `json:"cited_notes,omitempty"`
	// Files is the manifest of the files that come with the message
	// (decision 0054). The sending daemon builds it; the gateway stores it
	// and hands it to the receiving daemon, which collects the bytes before
	// it delivers the message. Empty for a message without files.
	Files []FileEntry `json:"files,omitempty"`
}

// IsWorkerTask identifies a stored initial brief. Older gateways and stored
// messages have no marker, so their original prefix remains the fallback.
func IsWorkerTask(m Message) bool {
	if m.WorkerTask != nil {
		return *m.WorkerTask
	}
	return (m.Type == "" || m.Type == TypeRequest) && m.ParentID == "" && strings.HasPrefix(m.Body, "[agent-net worker brief]")
}

// SessionInfo is what a daemon reports about one local session.
type SessionInfo struct {
	// OpenCodeActivity is the service activity timestamp in milliseconds. Discovery preserves it.
	OpenCodeActivity int64  `json:"opencode_activity,omitempty"`
	Type             string `json:"type"`
	SessionID        string `json:"session_id,omitempty"`
	Harness          string `json:"harness"`
	NativeID         string `json:"native_id"`
	PID              int    `json:"pid"`
	Cwd              string `json:"cwd"`
	Name             string `json:"name"`
	Status           string `json:"status"` // idle | busy | shell | dormant | unknown
	Delivery         string `json:"delivery"`
	// Repo is worked out by the daemon from Cwd. It is empty from older daemons.
	Repo RepoInfo `json:"repo"`
	// NoProject says the daemon found Cwd under a system temporary folder, so
	// the session belongs to no project. The gateway keeps the session but
	// derives no project from it. Empty from older daemons.
	NoProject     bool `json:"no_project,omitempty"`
	GraphExcluded bool `json:"graph_excluded,omitempty"`
	GraphChecked  bool `json:"graph_checked,omitempty"`
	// Mode says whether a person has the session open: ModeForeground,
	// ModeBackground or ModeUnknown. Empty from older daemons.
	Mode string `json:"mode,omitempty"`
	// Model is the model the session runs now, as the harness names it
	// (for example "claude-opus-5-5" or "anthropic/claude-sonnet-5"). Empty
	// when the harness does not say.
	Model string `json:"model,omitempty"`
	// Worker is set when agent-net started this session as a worker.
	Worker *WorkerInfo `json:"worker,omitempty"`
	// ToolsVersion is the agent-net version of the session's MCP tools (the
	// `agent-net mcp` process it started), ToolsUnknown when that process sent
	// no version, ToolsDisconnected for a Claude Code session whose process has
	// been gone for more than a minute, and empty for a session with no such
	// process yet. ToolsOld is
	// true when those tools are older than the machine's agent-net, so the
	// owner should reconnect agent-net in the session.
	ToolsVersion string `json:"tools_version,omitempty"`
	ToolsOld     bool   `json:"tools_old,omitempty"`
	// Waiting is set while the session waits for its user (decision 0016).
	Waiting *Waiting `json:"waiting,omitempty"`
	// HarnessWaits is set by a discovery adapter when the harness itself says
	// the session waits for its user: Claude Code's session file or a Codex
	// thread flag (decision 0057). The daemon uses it to see when that wait
	// ends. It stays on this machine.
	HarnessWaits bool `json:"-"`
	// LastActive is when the session last did something: a turn started or
	// ended, a message was sent or received, a task was announced, or a wait
	// started. It is not the time the daemon last saw the session alive.
	// Empty from an older daemon.
	LastActive int64 `json:"last_active,omitempty"`
	// IdleCompactedAt is the last automatic idle compaction in Unix seconds.
	// Discovery clears it on the next busy stretch.
	IdleCompactedAt int64 `json:"idle_compacted_at,omitempty"`
	OwnerUsedAt     int64 `json:"owner_used_at,omitempty"`
	OwnerUses       int   `json:"owner_uses,omitempty"`
	PinnedAt        int64 `json:"pinned_at,omitempty"`
	// HarnessActivity is the last observed output, state change or delivery acknowledgement.
	HarnessActivity int64 `json:"harness_activity,omitempty"`
	// LimitedUntil is when the session's provider usage limit ends, in Unix
	// seconds, and LimitedReason says which limit ("ChatGPT usage limit").
	// Both are empty when the session is not limited. The daemon clears them
	// when the session completes a turn. Empty from an older daemon.
	LimitedUntil  int64  `json:"limited_until,omitempty"`
	LimitedReason string `json:"limited_reason,omitempty"`
}

// StatusDormant is the status of a session that is reachable but has had no
// activity for a long time (OpenCode keeps every session in one service, so
// "online" alone does not mean "in use"). A message to it still delivers.
// Gateways store the status as given, so an older gateway accepts it.
const StatusDormant = "dormant"

// ValidStatus tells whether s is a status a plugin may report.
func ValidStatus(s string) bool {
	switch s {
	case "idle", "busy", "shell", StatusDormant, "unknown":
		return true
	}
	return false
}

// Session modes. Foreground means a person has the session open in a terminal
// or an app. Background means nobody is attached. Unknown means the harness
// gives no reliable signal.
const (
	ModeForeground = "foreground"
	ModeBackground = "background"
	ModeUnknown    = "unknown"
)

// ValidMode tells whether s is a mode a client may report.
func ValidMode(s string) bool {
	return s == ModeForeground || s == ModeBackground || s == ModeUnknown
}

// MaxModelLen is the longest model name the gateway keeps.
const MaxModelLen = 120

// CleanModel trims a model name, drops control characters and cuts it to
// MaxModelLen bytes (at a character boundary).
func CleanModel(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if len(s) > MaxModelLen {
		s = strings.ToValidUTF8(s[:MaxModelLen], "")
	}
	return s
}

// RepoInfo says which project a session works in. The daemon fills it from the
// session's folder with source control commands.
type RepoInfo struct {
	// Root is the main working tree of the repository, or "" when the folder
	// is not in a repository. All git worktrees of a repository share it.
	Root string `json:"root"`
	// Worktree is the working tree the folder is in. It equals Root, except
	// in a linked git worktree.
	Worktree string `json:"worktree"`
	// Project is the base name of Root, or of the folder when there is no repository.
	Project string `json:"project"`
	// Remote is the origin URL with credentials removed.
	Remote string `json:"remote"`
	Branch string `json:"branch"`
	// Description is the one-line project description the daemon read from
	// AGENTS.md (or CLAUDE.md) of the project. The gateway shows it as the
	// session's function. Empty when there is none, and from older daemons.
	Description string `json:"description,omitempty"`
}

// MaxDescriptionLen is the longest project description, in characters.
const MaxDescriptionLen = 120

// CleanDescription trims a description, drops control characters and cuts it
// to MaxDescriptionLen characters.
func CleanDescription(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if r := []rune(s); len(r) > MaxDescriptionLen {
		s = string(r[:MaxDescriptionLen])
	}
	return s
}

type Profile struct {
	Name         string   `json:"name"`
	Function     string   `json:"function"`
	Project      string   `json:"project"`
	Capabilities []string `json:"capabilities"`
	Task         string   `json:"task"`
	// TaskNote is set by the gateway when it reads the session, never by the
	// agent: a task line that names a worker which has ended gets
	// "(worker <id> ended <time>)". The stored task is not changed.
	TaskNote string `json:"task_note,omitempty"`
	// Expertise is what the session can answer about, in a short line the
	// agent wrote ("cogforge API and its deployment; live logs of homelab").
	Expertise string `json:"expertise,omitempty"`
}

// TaskLine is the announced task with the gateway's note after it, when there
// is one. Views show this line. Checks and searches read Task.
func (p Profile) TaskLine() string {
	if p.TaskNote == "" {
		return p.Task
	}
	if p.Task == "" {
		return p.TaskNote
	}
	return p.Task + " " + p.TaskNote
}

// AgentView keeps what the daemon verified apart from what the agent claims.
// AgentCharter is the public part of a project's charter.
type AgentCharter struct {
	Effective       *Charter `json:"effective,omitempty"`
	Purpose         string   `json:"purpose,omitempty"`
	PurposeFallback bool     `json:"purpose_fallback,omitempty"`
	Maturity        string   `json:"maturity,omitempty"`
	Accepts         string   `json:"accepts,omitempty"`
	Refuses         string   `json:"refuses,omitempty"`
	FeatureRequests string   `json:"feature_requests,omitempty"`
	// Rules are the node's effective rules, the same read-only name/value
	// pairs agent_graph_read returns. They are set on a single agent (agent_session_get),
	// not in a list.
	Rules []PublicRule `json:"rules,omitempty"`
}

type PendingRequest struct {
	MessageID string `json:"message_id"`
	PeerID    string `json:"peer_id"`
	PeerName  string `json:"peer_name"`
	Since     int64  `json:"since"`
	Outgoing  bool   `json:"outgoing"`
}

type AgentView struct {
	// AgentID and AgentName identify the lasting agent attached by the gateway.
	// Old gateways omit both. Workers and owner sessions have no agent.
	AgentID          string `json:"agent_id,omitempty"`
	AgentName        string `json:"agent_name,omitempty"`
	RepoLead         bool   `json:"repo_lead,omitempty"`
	RepoLeadExplicit bool   `json:"repo_lead_explicit,omitempty"`
	TeamLead         bool   `json:"team_lead,omitempty"`
	RepoMaxAgents    int    `json:"repo_max_agents,omitempty"`
	RepoAgentCount   int    `json:"repo_agent_count,omitempty"`
	RepoOverMax      bool   `json:"repo_over_max,omitempty"`
	// RepoContext is the gateway resolved repo key; it is not a wire field.
	RepoContext        string           `json:"-"`
	Type               string           `json:"type"`
	NativeID           string           `json:"native_id,omitempty"`
	Pending            []PendingRequest `json:"pending,omitempty"`
	ProjectDisplayName string           `json:"project_display_name,omitempty"`
	Charter            AgentCharter     `json:"charter"`
	SessionID          string           `json:"session_id"`
	NodeID             string           `json:"node_id"`
	Hostname           string           `json:"hostname"`
	Harness            string           `json:"harness"`
	Cwd                string           `json:"cwd"`
	Status             string           `json:"status"`
	// GatewayHeld and InSession count what waits for this session: messages
	// held at the gateway, and messages dispatched but not delivered to its
	// harness yet (decision 0129). Zero from an older gateway.
	GatewayHeld int   `json:"gateway_held,omitempty"`
	InSession   int   `json:"in_session,omitempty"`
	Online      bool  `json:"online"`
	LastSeen    int64 `json:"last_seen"`
	// LastActive is when the session last did something, in Unix seconds.
	// It is not when its daemon last reported it. 0 from an older gateway.
	LastActive int64 `json:"last_active,omitempty"`
	// IdleCompactedAt is the last automatic idle compaction in Unix seconds.
	// Discovery clears it on the next busy stretch.
	IdleCompactedAt int64 `json:"idle_compacted_at,omitempty"`
	OwnerUsedAt     int64 `json:"owner_used_at,omitempty"`
	OwnerUses       int   `json:"owner_uses,omitempty"`
	PinnedAt        int64 `json:"pinned_at,omitempty"`
	// LimitedUntil and LimitedReason: see SessionInfo. A past time means the
	// limit is over. Empty from an older gateway.
	LimitedUntil  int64  `json:"limited_until,omitempty"`
	LimitedReason string `json:"limited_reason,omitempty"`
	// StalledSince is the time of the last observed harness activity during a stall.
	StalledSince int64 `json:"stalled_since,omitempty"`
	// TaskStarted is when the announced task last changed, in Unix seconds. It
	// is for the read tools; the old list tools keep their output unchanged.
	TaskStarted int64   `json:"-"`
	Profile     Profile `json:"profile"`
	// Repo comes from the daemon's checks of Cwd, not from the agent.
	Repo RepoInfo `json:"repo"`
	// Mode and Model are what the session's daemon reported. Empty from an
	// older daemon.
	Mode  string `json:"mode,omitempty"`
	Model string `json:"model,omitempty"`
	// Worker is set for a session that agent-net started as a worker.
	Worker *WorkerInfo `json:"worker,omitempty"`
	// ToolsVersion and ToolsOld: see SessionInfo. Empty from an older daemon.
	ToolsVersion string `json:"tools_version,omitempty"`
	ToolsOld     bool   `json:"tools_old,omitempty"`
	// PID is the process ID of the session on its machine (0 when unknown).
	PID int `json:"pid,omitempty"`
	// FirstSeen is when the gateway first stored the session, in Unix
	// seconds. It does not change while the session lives, so a status view
	// can keep a stable order. 0 from an older gateway.
	FirstSeen int64 `json:"first_seen,omitempty"`
	// Title is the name the harness gives the session. Empty when it has
	// none.
	Title string `json:"title,omitempty"`
	// PeerTrust is true when the owner allowed this session to act on
	// requests from other agents without asking (decision 0012).
	PeerTrust bool `json:"peer_trust,omitempty"`
	// Delegation is the effective delegation policy of the session (decision
	// 0014), nil when it has none. Only the owner sets it.
	Delegation *DelegationView `json:"delegation,omitempty"`
	// Waiting is what the session is waiting to hear from its user (decision
	// 0016), nil when it is not waiting.
	Waiting *Waiting `json:"waiting,omitempty"`
	// Loop is the standing loop of the session's node, nil when it has none.
	Loop *LoopView `json:"loop,omitempty"`
}

// Header is shared by every gateway <-> daemon frame.
type Header struct {
	Type  string `json:"type"`
	ID    string `json:"id,omitempty"`
	OK    bool   `json:"ok,omitempty"`
	Error string `json:"error,omitempty"`
	// Retry is set only by the gateway, on a result that failed for a fault of
	// its own (a storage error): the same call may work later. A refusal never
	// carries it, and a result from a node is never passed on with it.
	Retry bool `json:"retry,omitempty"`
	// Via is set by a daemon on a request: the kind of caller behind it (see
	// the Via constants). It only labels the gateway's request counters. An
	// older daemon sends none.
	Via  string          `json:"via,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

// RetryableError is a gateway fault that the caller may try again. Only
// gateway code makes it.
type RetryableError struct{ Err error }

func (e RetryableError) Error() string { return e.Err.Error() }
func (e RetryableError) Unwrap() error { return e.Err }

// EncodeRequest builds a flat frame: the params' fields sit next to type and id.
func EncodeRequest(typ, id string, params any) ([]byte, error) {
	return EncodeRequestVia(typ, id, "", params)
}

// EncodeRequestVia is EncodeRequest with the origin tag of the caller (the
// Via constants). An empty via adds no field.
func EncodeRequestVia(typ, id, via string, params any) ([]byte, error) {
	m := map[string]json.RawMessage{}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
	}
	// The frame's own id and type would silently replace a parameter of the
	// same name (an Inbox answer once lost its item id this way).
	for _, k := range []string{"id", "type"} {
		if _, ok := m[k]; ok {
			return nil, fmt.Errorf("request %s: parameter %q clashes with the frame field", typ, k)
		}
	}
	if via != "" {
		if _, ok := m["via"]; ok {
			return nil, fmt.Errorf("request %s: parameter %q clashes with the frame field", typ, "via")
		}
		m["via"], _ = json.Marshal(via)
	}
	m["type"], _ = json.Marshal(typ)
	m["id"], _ = json.Marshal(id)
	return json.Marshal(m)
}

func EncodeResult(id string, data any, err error) ([]byte, error) {
	h := map[string]any{"type": "result", "id": id, "ok": err == nil}
	if err != nil {
		h["error"] = err.Error()
		var re RetryableError
		if errors.As(err, &re) {
			h["retry"] = true
		}
	} else if data != nil {
		h["data"] = data
	}
	return json.Marshal(h)
}

// Request payloads.
type (
	Hello struct {
		SessionAgents bool `json:"session_agents,omitempty"`
		// GraphRefs advertises typed Graph support independently of release numbering.
		GraphRefs bool   `json:"graph_refs,omitempty"`
		Version   string `json:"version"`
		Hostname  string `json:"hostname"`
		// TimeZone is the machine's IANA time zone, for example Europe/Paris.
		// Empty from an older daemon.
		TimeZone string `json:"time_zone,omitempty"`
		// Harnesses is the inventory of installed harnesses. Nil (absent or
		// null) means "not reported"; an empty list means "none installed".
		Harnesses []HarnessInfo `json:"harnesses"`
		// Launch says whether this machine accepts workers. Nil means "not
		// reported" (an older daemon): the gateway then refuses workers there.
		Launch *bool `json:"launch,omitempty"`
		// TailscaleIP is the machine's Tailscale address, or empty when it has
		// none. A sending daemon uses it to push files over Taildrop.
		TailscaleIP string         `json:"tailscale_ip,omitempty"`
		Health      *MachineHealth `json:"health,omitempty"`
	}
	// NodeHarnesses is the "node.harnesses" request: a new inventory.
	NodeHarnesses struct {
		Harnesses []HarnessInfo  `json:"harnesses"`
		Health    *MachineHealth `json:"health,omitempty"`
		Launch    *bool          `json:"launch,omitempty"`
	}
	SessionSync struct {
		Sessions []SessionInfo `json:"sessions"`
	}
	SessionUpsert struct {
		Session SessionInfo `json:"session"`
	}
	SessionRemove struct {
		Closed    bool   `json:"closed,omitempty"`
		Reopen    bool   `json:"reopen,omitempty"`
		SessionID string `json:"session_id"`
		Lost      bool   `json:"lost,omitempty"`
	}
	ProfileAnnounce struct {
		SessionID string `json:"session_id"`
		// A name is not an announce field: only the owner renames a session
		// (decision 0033). An older client's "name" is ignored.
		Function     string   `json:"function"`
		Project      string   `json:"project"`
		Capabilities []string `json:"capabilities"`
		Task         string   `json:"task"`
		Expertise    string   `json:"expertise,omitempty"`
		// Waiting, when set, says the session now waits for its user. An
		// announce without it clears an earlier wait (decision 0016).
		Waiting *Waiting `json:"waiting,omitempty"`
	}
	// SessionTrust is the "session.trust" request: the owner turns peer trust
	// on or off for one session.
	SessionTrust struct {
		SessionID string `json:"session_id"`
		On        bool   `json:"on"`
	}
	// MsgHandoff passes a message the sender received to another session. The
	// new message keeps the original sender, so the new recipient answers the
	// asker directly.
	MsgHandoff struct {
		Sender         string `json:"sender_session_id"`
		MessageID      string `json:"message_id"`
		Recipient      string `json:"recipient_session_id"`
		Note           string `json:"note,omitempty"`
		IdempotencyKey string `json:"idempotency_key,omitempty"`
	}
	MsgSend struct {
		ReplyOutcome
		// WorkerTask is set only by the daemon that builds a worker brief.
		WorkerTask bool `json:"worker_task,omitempty"`
		// WorkerIdleNotice marks the worker's idle-without-hand-back notice.
		// The gateway uses it to reject notices for a task superseded by a
		// newer owner request.
		WorkerIdleNotice bool `json:"worker_idle_notice,omitempty"`
		// WorkerNotice is set by the worker host daemon for its own notices.
		WorkerNotice bool `json:"worker_notice,omitempty"`
		// WorkerTaskRef identifies the gateway-tracked start for a remote relay.
		WorkerTaskRef string `json:"worker_task_ref,omitempty"`
		FromCTO       bool   `json:"sender_cto,omitempty"`
		// OwnerApproved carries CTO owner voice. Only the CTO path sets it,
		// together with FromCTO. The gateway refuses it from any other sender.
		OwnerApproved bool   `json:"owner_approved,omitempty"`
		Sender        string `json:"sender_session_id"`
		Recipient     string `json:"recipient_session_id"`
		// LeadAddress records an explicit repo: or team: address. The gateway
		// validates the recipient is still that lead before bypassing routing.
		LeadAddress string `json:"lead_address,omitempty"`
		// Order asks the gateway to label the message an order. The gateway
		// checks that the sender is above the recipient in the tree. If not,
		// the message goes as a request.
		Order bool `json:"order,omitempty"`
		// Urgent asks the gateway to deliver this message ahead of older
		// messages held for the recipient. It still counts toward the inbox
		// limit (decision 0129).
		Urgent bool `json:"urgent,omitempty"`
		// Ack closes the parent request without sending anything to its
		// sender. The body may be empty and an outcome is refused. The request
		// then counts as answered for reminders, the TUI's wait/owes view and
		// idle checks.
		Ack      bool   `json:"ack,omitempty"`
		Type     string `json:"message_type"`
		Body     string `json:"body"`
		ParentID string `json:"parent_message_id,omitempty"`
		// ConversationID, without ParentID, threads the message into an
		// existing conversation without naming a message it answers. The
		// recipient must have written to the sender in that conversation. A
		// Claude reply uses it when its daemon cannot tell which open request
		// it answers (W4.4). An older gateway ignores it and starts a new
		// conversation.
		ConversationID string `json:"conversation_id,omitempty"`
		IdempotencyKey string `json:"idempotency_key"`
		// Files is the manifest of the files that come with the message. Only
		// the daemon sets it, after its path checks and secret scan.
		Files []FileEntry `json:"files,omitempty"`
	}
	DeliveryUpdate struct {
		MessageID string `json:"message_id"`
		State     string `json:"state"`
		Detail    string `json:"detail"`
	}
	QueryAgents struct {
		Harness    string `json:"harness,omitempty"`
		Capability string `json:"capability,omitempty"`
		Project    string `json:"project,omitempty"`
		OnlineOnly bool   `json:"online_only,omitempty"`
		Text       string `json:"text,omitempty"`
		// IncludeOwners also returns the owner pseudo-sessions ("you (on
		// host)"). They are left out by default: they are people, not agents.
		IncludeOwners bool `json:"include_owners,omitempty"`
		// WorkersOwnedBy keeps only the workers started by this session, and
		// that session itself (so a reader can name the owner). A gateway
		// from before this field ignores it and sends every agent.
		WorkersOwnedBy string `json:"workers_owned_by,omitempty"`
		// IfVersion is the agents topic version the caller already holds. The
		// gateway answers NotModified and no rows when the topic has not moved,
		// so a safety refresh costs almost nothing.
		IfVersion uint64 `json:"if_version,omitempty"`
		// Lite leaves out fields no list screen shows. The TUI sets it.
		Lite bool `json:"lite,omitempty"`
	}
	// QueryNodes asks for NodesInfo. IfVersion is the nodes topic version the
	// caller already holds; an unchanged topic answers NotModified.
	QueryNodes struct {
		IfVersion uint64 `json:"if_version,omitempty"`
	}
	QueryAgent struct {
		SessionID string `json:"session_id"`
	}
	QueryMessages struct {
		SessionID      string `json:"session_id,omitempty"`
		ConversationID string `json:"conversation_id,omitempty"`
		Limit          int    `json:"limit,omitempty"`
		Offset         int    `json:"offset,omitempty"`
	}
	QueryMessage struct {
		MessageID string `json:"message_id"`
	}
	QueryProjects struct {
		// Name keeps only projects with this name (case-insensitive).
		Name string `json:"name,omitempty"`
	}
	// QueryEvents asks for the event feed: metadata of what happens on the
	// network, never message bodies. AfterID 0 returns the newest Limit events;
	// a larger AfterID returns the events after it, oldest first.
	QueryEvents struct {
		AfterID  int64 `json:"after_id,omitempty"`
		BeforeID int64 `json:"before_id,omitempty"`
		Limit    int   `json:"limit,omitempty"`
	}
	MsgDeliver struct {
		Message Message `json:"message"`
	}
	// MsgWithdraw takes back an undelivered message. A sender withdraws its
	// own message; the owner and the CTO may withdraw any message to one
	// session, or every message older than a named one (decision 0129).
	MsgWithdraw struct {
		MessageID string `json:"message_id"`
		// OlderThan, when set, withdraws every undelivered message to that
		// message's recipient that is older than it. Owner and CTO only.
		OlderThan bool   `json:"older_than,omitempty"`
		Sender    string `json:"sender_session_id"`
		AsOwner   bool   `json:"as_owner,omitempty"`
	}
	// NodeRename is the "node.rename" request: the owner gives a machine a
	// display name, or clears it with an empty name.
	NodeRename struct {
		NodeID string `json:"node_id"`
		Name   string `json:"name"`
	}
	// NodeName is the gateway's "node.name" push: the name the gateway now
	// lists for this machine, so the daemon uses the same one.
	NodeName struct {
		Hostname string `json:"hostname"`
	}
	// NodeUpgrade is the gateway's "node.upgrade" request: install this version
	// and restart. The daemon answers with NodeUpgradeResult before it restarts.
	NodeUpgrade struct {
		Version string `json:"version"`
		Force   bool   `json:"force,omitempty"`
	}
	NodeUpgradeResult struct {
		From       string `json:"from"`
		To         string `json:"to"`
		Restarting bool   `json:"restarting"`
	}
)

// NodeView is one node as the gateway lists it.
type NodeView struct {
	QualityPicks       map[string]string `json:"quality_picks,omitempty"`
	QualityPickReasons map[string]string `json:"quality_pick_reasons,omitempty"`
	// QualityPickModels is the picker's choice per level as a Models row
	// name: family and effort, for example "terra high".
	QualityPickModels map[string]string `json:"quality_pick_models,omitempty"`
	NodeID            string            `json:"node_id"`
	Hostname          string            `json:"hostname"`
	OS                string            `json:"os"`
	Version           string            `json:"version"`
	// TimeZone is the IANA time zone this machine reported, for example
	// Europe/Paris. Empty from an older daemon.
	TimeZone string `json:"time_zone,omitempty"`
	// TailscaleIP is the Tailscale address this machine last reported, empty
	// when it has none or runs an older daemon.
	TailscaleIP string `json:"tailscale_ip,omitempty"`
	Online      bool   `json:"online"`
	LastSeen    int64  `json:"last_seen"`
	// Harnesses is what the node's daemon last reported. Nil means the node
	// never reported (an older daemon).
	Harnesses []HarnessInfo `json:"harnesses"`
	// Quotas is the newest quota-axi reading of the machine, from its usage
	// report (decision 0022). Nil when it has none or the report is stale.
	Quotas *Quotas `json:"quotas,omitempty"`
	// Launch says whether the machine accepts workers: nil when it never
	// said (an older daemon), false after `setup --no-launch`.
	Launch *bool `json:"launch,omitempty"`
	// Hostname is the name to show: the owner's display name when one is set
	// (decision 0015), else the host name the daemon reported.
	// DisplayName is the owner's name alone ("" when none); Reported is the
	// host name the daemon reported in its hello.
	DisplayName string `json:"display_name,omitempty"`
	Reported    string `json:"reported_hostname,omitempty"`
	// Tracks is the worker track record of this machine, one entry per
	// harness and model (decision 0022).
	Tracks []TrackRecord `json:"tracks,omitempty"`
	// RecentModelFailures reports real failed runs per harness in the failure window.
	RecentModelFailures map[string]map[string]bool `json:"recent_model_failures,omitempty"`
	// RecentModelSuccesses reports recently completed workers by harness and model.
	RecentModelSuccesses map[string]map[string]bool `json:"recent_model_successes,omitempty"`
}

// HarnessInfo is one agent harness found on a machine.
type HarnessInfo struct {
	// MissingOperations lists capabilities absent from the running harness service.
	MissingOperations []string `json:"missing_operations,omitempty"`
	// DeliveryWarning is a failed harness transport check, empty when healthy or unknown.
	DeliveryWarning string `json:"delivery_warning,omitempty"`
	SignedIn        string `json:"signed_in,omitempty"` // "yes", "no", or empty (unknown).
	SignIn          string `json:"sign_in,omitempty"`
	Name            string `json:"name"` // claude | opencode | pi | codex | agy
	Path            string `json:"path"`
	Version         string `json:"version"`
	// Models lists the models the harness can use: "provider/model" where the
	// harness has a provider, plain ids for Codex and Claude's aliases. It is
	// empty when the harness did not report a list.
	Models []string `json:"models,omitempty"`
	// ModelDefaultEfforts contains known harness defaults by model ID.
	ModelDefaultEfforts map[string]string `json:"model_default_efforts,omitempty"`
	// ModelProviders is the owner's list of preferred model providers on this
	// machine (model_providers in node.json), first is best. It breaks ties
	// when a loose model name matches several models (ResolveModel).
	ModelProviders []string `json:"model_providers,omitempty"`
	// Primary is true when Path is the copy that comes first in the PATH of the
	// user's login shell. When it is false, the daemon could not tell which copy
	// the user runs. Absent in v0.3.5 and older.
	Primary bool `json:"primary,omitempty"`
	// PolicyReady is true for claude when the hook that applies the delegation
	// policy is installed on the machine (decision 0014).
	PolicyReady bool `json:"policy_ready,omitempty"`
	// Others are further copies of the same harness (a different real file),
	// each with its own path and version. Absent in v0.3.5 and older.
	Others []HarnessInstall `json:"others,omitempty"`
	// Update is the last update check on this machine, when one has run.
	Update *HarnessUpdate `json:"update,omitempty"`
}

type HarnessUpdate struct {
	CheckedAt time.Time `json:"checked_at"`
	Before    string    `json:"before,omitempty"`
	After     string    `json:"after,omitempty"`
	Result    string    `json:"result"`
	Detail    string    `json:"detail,omitempty"`
}

// HarnessInstall is one extra copy of a harness found on a machine.
type HarnessInstall struct {
	Path    string `json:"path"`
	Version string `json:"version"`
}

// Event is one row of the gateway's audit record.
type Event struct {
	ID        int64     `json:"id"`
	Time      time.Time `json:"time"`
	Kind      string    `json:"kind"`
	NodeID    string    `json:"node_id,omitempty"`
	Hostname  string    `json:"hostname,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	MessageID string    `json:"message_id,omitempty"`
	Detail    string    `json:"detail,omitempty"`
}

// FeedEvent is an Event as any node may read it: metadata only. For events
// about a message it also names both ends and the type, so a reader need not
// ask for the message (whose body only its two nodes may read).
type FeedEvent struct {
	Event
	From string `json:"from,omitempty"` // sender session of the message
	To   string `json:"to,omitempty"`   // recipient session of the message
	Type string `json:"message_type,omitempty"`
	// Parent is the message this one answers. A stored reply marks its parent
	// as replied, which has no event of its own.
	Parent  string `json:"parent_message_id,omitempty"`
	FromCTO bool   `json:"sender_cto,omitempty"`
	// OwnerApproved is true for a message the owner approved as their own
	// words, including an approved CTO send as the owner. The Graph draws it
	// as a gold line.
	OwnerApproved  bool   `json:"owner_approved,omitempty"`
	Voice          bool   `json:"voice,omitempty"`
	SenderOwner    bool   `json:"sender_owner,omitempty"`
	RecipientOwner bool   `json:"recipient_owner,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
}

// ProjectView is one project folder on one machine, built from stored sessions.
type ProjectView struct {
	DisplayName string `json:"display_name,omitempty"`
	Permissions string `json:"permissions"`
	Name        string `json:"name"`
	NodeID      string `json:"node_id"`
	Hostname    string `json:"hostname"`
	Path        string `json:"path"` // the main working tree of the repository, or the folder when there is none
	// Worktrees lists the linked git worktrees of the project where sessions ran.
	Worktrees []string `json:"worktrees"`
	Remote    string   `json:"remote"`
	Harnesses []string `json:"harnesses"` // which harnesses have run there
	Sessions  int      `json:"sessions"`  // sessions known there, online or not
	Online    int      `json:"online"`
	LastSeen  int64    `json:"last_seen"`
	// Source says where the row comes from: registered, seen or both. An older
	// gateway sends none; read that as seen.
	Source        string `json:"source,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
	// AddedBy says who registered it ("cli", or the harness and session).
	AddedBy   string `json:"added_by,omitempty"`
	AddedAt   int64  `json:"added_at,omitempty"`
	CheckedAt int64  `json:"checked_at,omitempty"`
}

// NodesInfo answers "query.nodes" and the admin nodes call.
type NodesInfo struct {
	GatewayVersion string     `json:"gateway_version"`
	Nodes          []NodeView `json:"nodes"`
	// NotModified means the caller's IfVersion already matches the nodes
	// topic, so Nodes holds no rows and the caller keeps its copy.
	NotModified bool `json:"not_modified,omitempty"`
	// Playbook is the fresh notes of the network, for agent_machines_find and the
	// owner's "Learned" list (decision 0022).
	Playbook []PlaybookNote `json:"playbook,omitempty"`
	// Calibration is the read-only difficulty table: per caller and
	// difficulty, the recent rework median and what the loop would do.
	Calibration Calibration `json:"calibration,omitempty"`
	// GatewayWarnings lists health, exposure and operational warnings from the gateway.
	GatewayWarnings []string `json:"gateway_warnings,omitempty"`
}

// Upgrade outcomes per node.
const (
	UpgradeOK      = "ok"
	UpgradeFailed  = "failed"
	UpgradeOffline = "offline"
)

// UpgradeResult is the outcome of an upgrade for one node.
type UpgradeResult struct {
	NodeID   string `json:"node_id"`
	Hostname string `json:"hostname"`
	From     string `json:"from"`
	To       string `json:"to"`
	Status   string `json:"status"`
	Detail   string `json:"detail,omitempty"`
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// NewID returns prefix + "_" + 16 hex characters, e.g. "m_0123456789abcdef".
func NewID(prefix string) string { return prefix + "_" + randHex(8) }

// NewToken returns 32 random bytes as hex.
func NewToken() string { return randHex(32) }

// CertFingerprint is the SHA-256 of the first PEM certificate's DER bytes, in hex.
func CertFingerprint(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "", errors.New("no PEM certificate found")
	}
	sum := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(sum[:]), nil
}

// SessionType uses the existing harness and worker roles for older records.
func SessionType(harness string, worker *WorkerInfo, explicit string) string {
	switch harness {
	case "cto", "owner", "bot":
		return harness
	}
	if worker != nil {
		return "worker"
	}
	if explicit == "merger" {
		return explicit
	}
	return "agent"
}

// Limited tells whether the session's provider usage limit is still on at now.
func (a AgentView) Limited(now time.Time) bool {
	return a.LimitedUntil > now.Unix()
}

// ToolsDisconnected tells whether the session is online but has lost its
// agent-net tools (see the ToolsDisconnected constant).
func (a AgentView) ToolsDisconnected() bool {
	return a.Online && a.Worker == nil && a.ToolsVersion == ToolsDisconnected
}

// LimitKind is the kind of limit in a limited_reason ("ChatGPT usage limit"):
// "usage limit", "rate limit", "session limit" or "quota".
func LimitKind(reason string) string {
	for _, k := range []string{"rate limit", "session limit", "quota"} {
		if strings.HasSuffix(reason, k) {
			return k
		}
	}
	return "usage limit"
}
