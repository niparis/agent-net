package protocol

import (
	"fmt"
	"strings"
)

// Scope kinds. A scope is authority the owner delegates to a role node, kept
// apart from reporting lines and depends edges. The list is fixed on purpose:
// a new kind is a small code change, never a free-text value.
const (
	ScopeAsks        = "asks"
	ScopeAdmin       = "admin"
	ScopeMerge       = "merge"
	ScopeRelease     = "release"
	ScopeDeploy      = "deploy"
	ScopeReview      = "review"
	ScopePermissions = "permissions"
)

// ScopeKinds lists every scope kind in display order.
var ScopeKinds = []string{ScopeAsks, ScopeAdmin, ScopeMerge, ScopeRelease, ScopeDeploy, ScopeReview, ScopePermissions}

// ScopeOverAll is the over value that covers the whole network.
const ScopeOverAll = "all"

// ScopeOverKinds are the target kinds an over may name.
var ScopeOverKinds = []string{ScopeOverAll, "team", "repo", "agent"}

// Scope is one stored scope of a node (an Agent ID). Over is all, or
// team:<id>, repo:<id> or agent:<id>. Note is shown only.
type Scope struct {
	Node  string `json:"node"`
	Kind  string `json:"kind"`
	Over  string `json:"over"`
	Note  string `json:"note,omitempty"`
	SetBy string `json:"set_by,omitempty"`
	SetAt int64  `json:"set_at,omitempty"`
}

// ScopeSetRequest adds a scope or changes its note. ScopeRemoveRequest ignores
// Note.
type ScopeSetRequest struct {
	Node  string `json:"node"`
	Kind  string `json:"kind"`
	Over  string `json:"over"`
	Note  string `json:"note,omitempty"`
	Owner string `json:"owner,omitempty"`
}

// ValidScopeKind reports whether kind is in ScopeKinds.
func ValidScopeKind(kind string) bool {
	for _, k := range ScopeKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// ParseScopeOver splits over into its target kind (all, team, repo or agent)
// and ID. The ID is empty for all.
func ParseScopeOver(over string) (string, string, error) {
	if over == ScopeOverAll {
		return ScopeOverAll, "", nil
	}
	kind, id, ok := strings.Cut(over, ":")
	if !ok || strings.TrimSpace(id) == "" || id != strings.TrimSpace(id) {
		return "", "", fmt.Errorf("over must be all, team:<name>, repo:<name> or agent:<name>")
	}
	switch kind {
	case "team", "repo", "agent":
		return kind, id, nil
	}
	return "", "", fmt.Errorf("over must be all, team:<name>, repo:<name> or agent:<name>")
}
