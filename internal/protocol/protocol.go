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
	"strings"
	"time"
)

// Message states.
const (
	StateStored     = "stored"
	StateDispatched = "dispatched"
	StateDelivered  = "delivered"
	StateReplied    = "replied"
	StateFailed     = "failed"
	StateExpired    = "expired"
)

// Message types.
const (
	TypeRequest      = "request"
	TypeReply        = "reply"
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
	ID             string    `json:"message_id"`
	ConversationID string    `json:"conversation_id"`
	ParentID       string    `json:"parent_message_id,omitempty"`
	From           string    `json:"sender_session_id"`
	To             string    `json:"recipient_session_id"`
	Type           string    `json:"message_type"`
	Body           string    `json:"body"`
	Hop            int       `json:"hop"`
	IdempotencyKey string    `json:"idempotency_key"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	State          string    `json:"state"`
	FromName       string    `json:"sender_name"`
	// FromHarness is filled by the gateway so a receiving adapter can say
	// what kind of agent wrote the message.
	FromHarness string `json:"sender_harness,omitempty"`
	// FromWorker is true when the sender is a worker (a session that agent-net
	// started). A receiving adapter frames its text as model output.
	FromWorker bool `json:"sender_worker,omitempty"`
}

// SessionInfo is what a daemon reports about one local session.
type SessionInfo struct {
	SessionID string `json:"session_id,omitempty"`
	Harness   string `json:"harness"`
	NativeID  string `json:"native_id"`
	PID       int    `json:"pid"`
	Cwd       string `json:"cwd"`
	Name      string `json:"name"`
	Status    string `json:"status"` // idle | busy | shell | dormant | unknown
	Delivery  string `json:"delivery"`
	// Repo is worked out by the daemon from Cwd. It is empty from older daemons.
	Repo RepoInfo `json:"repo"`
	// Mode says whether a person has the session open: ModeForeground,
	// ModeBackground or ModeUnknown. Empty from older daemons.
	Mode string `json:"mode,omitempty"`
	// Model is the model the session runs now, as the harness names it
	// (for example "claude-opus-5-5" or "anthropic/claude-sonnet-5"). Empty
	// when the harness does not say.
	Model string `json:"model,omitempty"`
	// Worker is set when agent-net started this session as a worker.
	Worker *WorkerInfo `json:"worker,omitempty"`
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
}

// AgentView keeps what the daemon verified apart from what the agent claims.
type AgentView struct {
	SessionID string  `json:"session_id"`
	NodeID    string  `json:"node_id"`
	Hostname  string  `json:"hostname"`
	Harness   string  `json:"harness"`
	Cwd       string  `json:"cwd"`
	Status    string  `json:"status"`
	Online    bool    `json:"online"`
	LastSeen  int64   `json:"last_seen"`
	Profile   Profile `json:"profile"`
	// Repo comes from the daemon's checks of Cwd, not from the agent.
	Repo RepoInfo `json:"repo"`
	// Mode and Model are what the session's daemon reported. Empty from an
	// older daemon.
	Mode  string `json:"mode,omitempty"`
	Model string `json:"model,omitempty"`
	// Worker is set for a session that agent-net started as a worker.
	Worker *WorkerInfo `json:"worker,omitempty"`
	// PID is the process ID of the session on its machine (0 when unknown).
	PID int `json:"pid,omitempty"`
}

// Header is shared by every gateway <-> daemon frame.
type Header struct {
	Type  string          `json:"type"`
	ID    string          `json:"id,omitempty"`
	OK    bool            `json:"ok,omitempty"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// EncodeRequest builds a flat frame: the params' fields sit next to type and id.
func EncodeRequest(typ, id string, params any) ([]byte, error) {
	m := map[string]any{}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
	}
	m["type"] = typ
	m["id"] = id
	return json.Marshal(m)
}

func EncodeResult(id string, data any, err error) ([]byte, error) {
	h := map[string]any{"type": "result", "id": id, "ok": err == nil}
	if err != nil {
		h["error"] = err.Error()
	} else if data != nil {
		h["data"] = data
	}
	return json.Marshal(h)
}

// Request payloads.
type (
	Hello struct {
		Version  string `json:"version"`
		Hostname string `json:"hostname"`
		// Harnesses is the inventory of installed harnesses. Nil (absent or
		// null) means "not reported"; an empty list means "none installed".
		Harnesses []HarnessInfo `json:"harnesses"`
		// Launch says whether this machine accepts workers. Nil means "not
		// reported" (an older daemon): the gateway then refuses workers there.
		Launch *bool `json:"launch,omitempty"`
	}
	// NodeHarnesses is the "node.harnesses" request: a new inventory.
	NodeHarnesses struct {
		Harnesses []HarnessInfo `json:"harnesses"`
		Launch    *bool         `json:"launch,omitempty"`
	}
	SessionSync struct {
		Sessions []SessionInfo `json:"sessions"`
	}
	SessionUpsert struct {
		Session SessionInfo `json:"session"`
	}
	SessionRemove struct {
		SessionID string `json:"session_id"`
	}
	ProfileAnnounce struct {
		SessionID    string   `json:"session_id"`
		Name         string   `json:"name"`
		Function     string   `json:"function"`
		Project      string   `json:"project"`
		Capabilities []string `json:"capabilities"`
		Task         string   `json:"task"`
	}
	MsgSend struct {
		Sender         string `json:"sender_session_id"`
		Recipient      string `json:"recipient_session_id"`
		Type           string `json:"message_type"`
		Body           string `json:"body"`
		ParentID       string `json:"parent_message_id,omitempty"`
		IdempotencyKey string `json:"idempotency_key"`
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
	}
	QueryAgent struct {
		SessionID string `json:"session_id"`
	}
	QueryMessages struct {
		SessionID      string `json:"session_id,omitempty"`
		ConversationID string `json:"conversation_id,omitempty"`
		Limit          int    `json:"limit,omitempty"`
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
		AfterID int64 `json:"after_id,omitempty"`
		Limit   int   `json:"limit,omitempty"`
	}
	MsgDeliver struct {
		Message Message `json:"message"`
	}
	// NodeUpgrade is the gateway's "node.upgrade" request: install this version
	// and restart. The daemon answers with NodeUpgradeResult before it restarts.
	NodeUpgrade struct {
		Version string `json:"version"`
	}
	NodeUpgradeResult struct {
		From       string `json:"from"`
		To         string `json:"to"`
		Restarting bool   `json:"restarting"`
	}
)

// NodeView is one node as the gateway lists it.
type NodeView struct {
	NodeID   string `json:"node_id"`
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Version  string `json:"version"`
	Online   bool   `json:"online"`
	LastSeen int64  `json:"last_seen"`
	// Harnesses is what the node's daemon last reported. Nil means the node
	// never reported (an older daemon).
	Harnesses []HarnessInfo `json:"harnesses"`
	// Launch says whether the machine accepts workers: nil when it never
	// said (an older daemon), false after `setup --no-launch`.
	Launch *bool `json:"launch,omitempty"`
}

// HarnessInfo is one agent harness found on a machine.
type HarnessInfo struct {
	Name    string `json:"name"` // claude | opencode | pi | codex
	Path    string `json:"path"`
	Version string `json:"version"`
	// Models lists the models the harness can use, as "provider/model". It is
	// only filled for harnesses that have a command to list them (opencode).
	Models []string `json:"models,omitempty"`
	// ModelProviders is the owner's list of preferred model providers on this
	// machine (model_providers in node.json), first is best. It breaks ties
	// when a loose model name matches several models (ResolveModel).
	ModelProviders []string `json:"model_providers,omitempty"`
	// Primary is true when Path is the copy that comes first in the PATH of the
	// user's login shell. When it is false, the daemon could not tell which copy
	// the user runs. Absent in v0.3.5 and older.
	Primary bool `json:"primary,omitempty"`
	// Others are further copies of the same harness (a different real file),
	// each with its own path and version. Absent in v0.3.5 and older.
	Others []HarnessInstall `json:"others,omitempty"`
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
	Parent string `json:"parent_message_id,omitempty"`
}

// ProjectView is one project folder on one machine, built from stored sessions.
type ProjectView struct {
	Name     string `json:"name"`
	NodeID   string `json:"node_id"`
	Hostname string `json:"hostname"`
	Path     string `json:"path"` // the main working tree of the repository, or the folder when there is none
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
