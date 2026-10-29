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
}

// SessionInfo is what a daemon reports about one local session.
type SessionInfo struct {
	SessionID string `json:"session_id,omitempty"`
	Harness   string `json:"harness"`
	NativeID  string `json:"native_id"`
	PID       int    `json:"pid"`
	Cwd       string `json:"cwd"`
	Name      string `json:"name"`
	Status    string `json:"status"` // idle | busy | shell | unknown
	Delivery  string `json:"delivery"`
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
