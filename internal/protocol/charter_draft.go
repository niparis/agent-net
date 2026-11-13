package protocol

// Charter drafts (decision 0088). A live session of a repo reads its
// AGENTS.md and proposes charter values with agent_charter_draft. A draft is a
// suggestion: nothing changes until the owner or the CTO accepts it.

// CharterDraftSource names where a draft came from.
const CharterDraftSource = "AGENTS.md"

// CharterDraftTeamSource names where a team draft came from.
const CharterDraftTeamSource = "member charters"

// CharterDraftAttrs lists the fields a session may draft at each level.
var CharterDraftAttrs = map[string][]string{
	"repo":  {"purpose", "maturity", "refuses"},
	"agent": {"accepts", "refuses", "escalate", "feature_requests", "rules"},
	"team":  {"purpose", "refuses"},
}

// CharterDraftAllowed tells whether a session may draft attr at level.
func CharterDraftAllowed(level, attr string) bool {
	for _, a := range CharterDraftAttrs[level] {
		if a == attr {
			return true
		}
	}
	return false
}

// CharterDraftRequest is agent_charter_draft: one proposed value. The scope is
// never a parameter: it is the caller's own repo, agent or team. Only a team
// lead with several teams names one with Team.
type CharterDraftRequest struct {
	Level string `json:"level"`
	Attr  string `json:"attr"`
	Value string `json:"value"`
	Team  string `json:"team,omitempty"`
}

// CharterDraftFrame is the daemon frame "charter.draft". The daemon adds the
// caller and the hash of the AGENTS.md it read on its machine.
type CharterDraftFrame struct {
	CharterDraftRequest
	SessionID  string `json:"session_id"`
	SourceHash string `json:"source_hash,omitempty"`
}

// CharterDraftResult answers agent_charter_draft.
type CharterDraftResult struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
}

// CharterDraft is one drafted value. Status is pending, accepted, rejected or
// superseded (the field got a value some other way).
type CharterDraft struct {
	ID        int64  `json:"id"`
	Level     string `json:"level"`
	Scope     string `json:"scope"`
	Name      string `json:"name,omitempty"`
	Attr      string `json:"attr"`
	Value     string `json:"value"`
	Source    string `json:"source"`
	Status    string `json:"status"`
	DraftedBy string `json:"drafted_by,omitempty"`
	CreatedAt int64  `json:"created_at"`
	DecidedAt int64  `json:"decided_at,omitempty"`
	DecidedBy string `json:"decided_by,omitempty"`
	// HistoryID is the graph history entry an accept wrote, for graph_undo. It
	// is set on the answer of a decision only.
	HistoryID int64 `json:"history_id,omitempty"`
}
