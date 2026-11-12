// Package toolcatalog holds the one list of agent-net tools: their names,
// descriptions, schemas, roles and behavior hints. Each server filters this
// list by the caller's role.
package toolcatalog

import (
	"encoding/json"
	"sort"
)

// Role is a caller role that a tool may be listed for.
type Role string

const (
	RoleAgent  Role = "agent"
	RoleWorker Role = "worker"
	RoleCTO    Role = "cto"
	RoleBot    Role = "bot"
)

// Hints are the MCP behavior annotations, in R D I O order.
type Hints struct {
	ReadOnly    bool
	Destructive bool
	Idempotent  bool
	OpenWorld   bool
}

// Tool is one catalog entry.
type Tool struct {
	Name        string
	Description string
	Roles       []Role
	Hints       Hints
	// InputSchema and OutputSchema are the advertised flat schemas, copied
	// from the design spec.
	InputSchema  json.RawMessage
	OutputSchema json.RawMessage
	// CTOInputSchema, when set, replaces InputSchema for the CTO role.
	CTOInputSchema json.RawMessage
	// AlwaysLoad marks the tools that set the anthropic/alwaysLoad metadata.
	AlwaysLoad bool
}

// ForRole returns the tools a role may see, sorted by name.
func ForRole(role Role) []Tool {
	var out []Tool
	for _, tool := range Tools {
		for _, r := range tool.Roles {
			if r == role {
				if role == RoleBot && tool.Name == "agent_message_send" {
					tool.InputSchema = botMessageSendInput
					tool.Description = "Send a new message to one session as yourself. Lead-level agents (chief of staff, team leads, granted lead level) may start a request to the CTO for a tool or a second opinion; a notification to the CTO is reply-only, and others go through their lead or the chief. A peer message is a request, not the owner's approval."
				}
				if role == RoleCTO && tool.Name == "agent_owner_judgments_find" {
					tool.Description = "Search the owner's past decisions, standing orders, rules, answers and corrections. Ranked for a query, else newest first."
				}
				out = append(out, tool)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Allows reports whether name is a catalog tool the role may use.
func Allows(role Role, name string) bool {
	for _, tool := range Tools {
		if tool.Name != name {
			continue
		}
		for _, r := range tool.Roles {
			if r == role {
				return true
			}
		}
		return false
	}
	return false
}
