package protocol

import (
	"fmt"
	"strings"
	"unicode"
)

// CharterValueMaxBytes is the maximum UTF-8 byte length of a charter value.
const CharterValueMaxBytes = 2048

// CharterSource identifies one effective field contribution.
type CharterSource struct {
	Level string `json:"level"`
	Scope string `json:"scope,omitempty"`
	Value string `json:"value"`
}
type CharterSources map[string][]CharterSource

// PlaceHeldBack is one thing the owner holds back, and the level that set it
// ("team" or "repo"). The always held back list is not stored here.
type PlaceHeldBack struct {
	Text string `json:"text"`
	From string `json:"from"`
}

// NodePlace is where one record sits in the authority tree (decision 0089):
// its team and live lead, the team's mode, the repo lead it reports to and
// what the owner holds back. The gateway builds it from the same Team and Repo
// data as the turn text, so the card and the turn text agree.
type NodePlace struct {
	Team      string          `json:"team,omitempty"`
	TeamLead  string          `json:"team_lead,omitempty"`
	Mode      string          `json:"mode,omitempty"`
	Repo      string          `json:"repo,omitempty"`
	RepoLead  string          `json:"repo_lead,omitempty"`
	ReportsTo string          `json:"reports_to,omitempty"`
	HeldBack  []PlaceHeldBack `json:"held_back,omitempty"`
}

// Charter contains the fixed, independently versioned attributes of a node.
// Deprecated Consumers, Needs and Notes stay empty for older readers.
type Charter struct {
	MaturityNote    string          `json:"maturity_note,omitempty"`
	Rules           string          `json:"rules,omitempty"`
	Sources         *CharterSources `json:"sources,omitempty"`
	Purpose         string          `json:"purpose,omitempty"`
	Maturity        string          `json:"maturity,omitempty"`
	Accepts         string          `json:"accepts,omitempty"`
	Refuses         string          `json:"refuses,omitempty"`
	FeatureRequests string          `json:"feature_requests,omitempty"`
	Consumers       string          `json:"consumers,omitempty"`
	Escalate        string          `json:"escalate,omitempty"`
	Needs           string          `json:"needs,omitempty"`
	Notes           string          `json:"notes,omitempty"`
}

type GraphLocation struct {
	Machine string `json:"machine"`
	Path    string `json:"path"`
}

type GraphSession struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Machine string `json:"machine"`
	Status  string `json:"status"`
}

// NodeDelegation is the delegation policy the owner set for a node's project
// on one machine (decision 0014). A policy is a rule agents follow, so it is
// public read-only, like the node settings.
type NodeDelegation struct {
	Machine string `json:"machine"`
	DelegationPolicy
}

// NodeBot is what the Graph shows of a connected bot app on its agent node.
// ConnectedAt and LastUsedAt are Unix nanoseconds; LastUsedAt is 0 when the
// bot never called. CanCall lists the shared MCP servers the owner granted.
type NodeBot struct {
	Client      string   `json:"client,omitempty"`
	ConnectedAt int64    `json:"connected_at,omitempty"`
	LastUsedAt  int64    `json:"last_used_at,omitempty"`
	CanCall     []string `json:"can_call,omitempty"`
}

// Node is a component or an architect. Components can be implicit projects.
type Node struct {
	Active     *bool     `json:"active,omitempty"`
	Ref        *GraphRef `json:"ref,omitempty"`
	Owner      *GraphRef `json:"owner,omitempty"`
	Lead       *GraphRef `json:"lead,omitempty"`
	MaxAgents  int       `json:"max_agents,omitempty"`
	AgentCount int       `json:"agent_count,omitempty"`
	OverMax    bool      `json:"over_max,omitempty"`
	Role       string    `json:"role,omitempty"`
	// Bot is set on the agent node of a connected bot app (role "bot").
	Bot *NodeBot `json:"bot,omitempty"`
	// AuthorityMode (a team) and HeldBack (a team or a repo) are the owner's
	// authority settings (decision 0089).
	AuthorityMode string `json:"authority_mode,omitempty"`
	HeldBack      string `json:"held_back,omitempty"`
	// Place is the record's position in the authority tree, built by the
	// gateway from the same data as the turn text (decision 0089).
	Place     *NodePlace `json:"place,omitempty"`
	Name      string     `json:"name"`
	Kind      string     `json:"kind"`
	CreatedAt int64      `json:"created_at,omitempty"`
	Charter   Charter    `json:"charter"`
	// Settings are the node's overrides of its maturity defaults (decision
	// 0022). They sit beside the charter and only the owner changes them.
	Settings map[string]string `json:"settings,omitempty"`
	// Loop is the node's standing loop, nil when it has none (decision 0105).
	Loop *LoopView `json:"loop,omitempty"`
	// Delegation holds the delegation policies the owner set for this node's
	// project on each machine that has one (decision 0014). Only the owner
	// changes them; they are public read-only.
	Delegation   []NodeDelegation `json:"delegation,omitempty"`
	Locations    []GraphLocation  `json:"locations,omitempty"`
	Sessions     []GraphSession   `json:"sessions,omitempty"`
	SessionCount int              `json:"session_count,omitempty"`
}

// CharterChange is an immutable charter history entry.
type CharterChange struct {
	Ref      *GraphRef `json:"ref,omitempty"`
	ID       int64     `json:"id"`
	Node     string    `json:"node"`
	Attr     string    `json:"attr"`
	OldValue string    `json:"old_value"`
	NewValue string    `json:"new_value"`
	At       int64     `json:"at"`
	By       string    `json:"by"`
}

// EdgeCalls is the kind of the derived edge from a bot to the project that
// hosts a shared MCP server the bot is granted. It is never stored or edited.
const EdgeCalls = "calls"

// Edge is a directed dependency, deploy or delivery owner relationship.
type Edge struct {
	FromRef *GraphRef `json:"-"`
	ToRef   *GraphRef `json:"-"`
	From    string    `json:"from"`
	To      string    `json:"to"`
	Kind    string    `json:"kind"`
	Mode    string    `json:"mode,omitempty"`
	Rule    string    `json:"rule"`
}

// Graph is a snapshot of all known nodes and edges.
type Graph struct {
	Warnings []string `json:"warnings,omitempty"`
	Nodes    []Node   `json:"nodes"`
	Edges    []Edge   `json:"edges"`
}

// ParseMaturity parses a maturity level with an optional explanation.
func ParseMaturity(value string) (level, text string, err error) {
	level, text, hasText := strings.Cut(value, " - ")
	switch level {
	case "experimental", "early", "growing", "mature", "critical":
	default:
		return "", "", fmt.Errorf("invalid maturity level")
	}
	if hasText && strings.TrimSpace(text) == "" {
		return "", "", fmt.Errorf("empty maturity explanation")
	}
	return level, text, nil
}

// SplitCharterList splits a list-type charter value (accepts, refuses or
// escalate) into items on semicolons and newlines. It trims each item and drops
// empty ones, so every reader sees the same items.
func SplitCharterList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	items := strings.FieldsFunc(value, func(r rune) bool { return r == ';' || r == '\n' })
	var out []string
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// CleanCharterValue removes control characters other than newlines, then validates the value.
func CleanCharterValue(attr, value string) (string, error) {
	if _, ok := CharterGuideFor(attr); !ok {
		return "", fmt.Errorf("unknown charter attribute")
	}
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' {
			return -1
		}
		return r
	}, value)
	if len(value) > CharterValueMaxBytes {
		return "", fmt.Errorf("charter value exceeds %d bytes", CharterValueMaxBytes)
	}
	if attr == "maturity" && value != "" {
		switch value {
		case "experimental", "early", "growing", "mature":
		default:
			return "", fmt.Errorf("maturity must be experimental, early, growing or mature")
		}
	}
	return value, nil
}

// CharterSetRequest sets an attribute; an empty value clears it.
type CharterSetRequest struct {
	Level string    `json:"level,omitempty"`
	Ref   *GraphRef `json:"ref,omitempty"`
	Node  string    `json:"node"`
	Attr  string    `json:"attr"`
	Value string    `json:"value"`
}

// CharterSetResult reports advisory warnings without blocking the save.
type CharterSetResult struct {
	OK       bool     `json:"ok"`
	Warnings []string `json:"warnings,omitempty"`
}

// GraphHistoryRequest selects history by node, newest first.
type GraphHistoryRequest struct {
	Ref   *GraphRef `json:"ref,omitempty"`
	Node  string    `json:"node"`
	Limit int       `json:"limit,omitempty"`
}

// GraphLifecycleRequest creates a named identity or deactivates a typed ref.
type GraphLifecycleRequest struct {
	Name  string    `json:"name,omitempty"`
	Role  string    `json:"role,omitempty"`
	Ref   *GraphRef `json:"ref,omitempty"`
	Owner string    `json:"owner,omitempty"`
}

// GraphNodeRequest identifies an architect.
type GraphNodeRequest struct {
	Name string `json:"name"`
}

// GraphUndoRequest identifies a history row; zero means the latest row.
type GraphUndoRequest struct {
	CTOChat bool  `json:"cto_chat,omitempty"`
	ID      int64 `json:"id,omitempty"`
}

// GraphUndoFrame avoids the frame's own id field.
type GraphUndoFrame struct {
	HistoryID int64 `json:"history_id,omitempty"`
}

// EdgeChange records a change to an edge; nil means it did not exist.
type EdgeChange struct {
	ID  int64  `json:"id"`
	Old *Edge  `json:"old,omitempty"`
	New *Edge  `json:"new,omitempty"`
	At  int64  `json:"at"`
	By  string `json:"by"`
}

// CharterAttributeAtLevel describes new writes. Legacy unscoped calls retain
// repo-local work fields so older clients can edit an existing charter.
func CharterAttributeAtLevel(level, attr string) bool {
	if attr == "rules" || attr == "refuses" {
		return level == "global" || level == "team" || level == "repo" || level == "agent"
	}
	switch level {
	case "repo":
		return attr == "purpose" || attr == "maturity" || attr == "maturity_note"
	case "team":
		return attr == "purpose" || attr == "escalate"
	case "agent":
		return attr == "accepts" || attr == "escalate" || attr == "feature_requests"
	}
	return false
}
