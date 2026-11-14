package authority

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// Held is what a session's role node holds: its scopes, and for each team or
// agent scope the repos it covers (the gateway resolves those).
type Held struct {
	Scopes []protocol.Scope
	// Repos maps a scope's over value (team:x, agent:y) to repo IDs.
	Repos map[string][]string
}

// Call is one tool call to decide. Repo is the repo of the folder the call
// runs in, empty when the folder is in none.
type Call struct {
	Tool  string
	Input json.RawMessage
	Repo  string
}

// covers says whether a scope reaches repo.
func (h Held) covers(s protocol.Scope, repo string) bool {
	if s.Over == protocol.ScopeOverAll {
		return true
	}
	if repo == "" {
		return false
	}
	return s.Over == "repo:"+repo || slices.Contains(h.Repos[s.Over], repo)
}

// Holds says whether the held scopes include one of kind that reaches repo. It
// is how agent-net's own holds ask the question (a worker start that names a
// model, a permission proposal, an ask): nothing is allowed by the harness, the
// hold is only lifted.
func (h Held) Holds(kind, repo string) bool {
	for _, s := range h.Scopes {
		if s.Kind == kind && h.covers(s, repo) {
			return true
		}
	}
	return false
}

// Allow answers one question: do the held scopes allow this call. False means
// no decision, never "block": the harness asks as it does today.
func Allow(h Held, c Call) bool {
	if len(h.Scopes) == 0 {
		return false
	}
	switch {
	case strings.EqualFold(c.Tool, "bash"):
		return allowBash(h, c)
	case strings.HasPrefix(c.Tool, "mcp__agent-net-cto__"):
		return allowTool(h, strings.TrimPrefix(c.Tool, "mcp__agent-net-cto__"), c.Input)
	case strings.HasPrefix(c.Tool, "mcp__agent-net__"):
		return allowTool(h, strings.TrimPrefix(c.Tool, "mcp__agent-net__"), c.Input)
	case strings.HasPrefix(c.Tool, "mcp__"):
		return false
	}
	return allowTool(h, c.Tool, c.Input)
}

func allowBash(h Held, c Call) bool {
	var in struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(c.Input, &in) != nil {
		return false
	}
	command := strings.TrimSpace(in.Command)
	if command == "" || Compound(command) {
		return false
	}
	if _, never := CommandNever(command); never {
		return false
	}
	for _, s := range h.Scopes {
		if !h.covers(s, c.Repo) {
			continue
		}
		for _, re := range bashRes[s.Kind] {
			if re.MatchString(command) {
				return true
			}
		}
	}
	return false
}

func allowTool(h Held, name string, input json.RawMessage) bool {
	var method string
	if name == "cto_op" {
		var in struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(input, &in) != nil || in.Method == "" || OwnerOnly(in.Method) {
			return false
		}
		method = in.Method
	}
	for _, s := range h.Scopes {
		for _, row := range Table {
			if row.Kind != s.Kind {
				continue
			}
			if method != "" && row.hasOp(method) || method == "" && row.hasTool(name) {
				return true
			}
		}
	}
	return false
}
