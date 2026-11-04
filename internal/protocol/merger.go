package protocol

// MergerSetRequest is owner-only. An empty SessionID removes the assignment.
type MergerSetRequest struct {
	Project   string `json:"project"`
	NodeID    string `json:"node_id"`
	SessionID string `json:"session_id"`
}

const OpMergerSet = "merger_set"
