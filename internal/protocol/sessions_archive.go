package protocol

// SessionsArchiveRequest combines selectors with AND. IDs are ORed within the
// ID list. Empty selectors match all offline sessions. Expected pins a preview.
type SessionsArchiveRequest struct {
	IDs           []string `json:"ids,omitempty"`
	Name          string   `json:"name,omitempty"`
	Folder        string   `json:"folder,omitempty"`
	Project       *string  `json:"project,omitempty"` // Empty string selects no project.
	Harness       string   `json:"harness,omitempty"`
	CreatedBefore string   `json:"created_before,omitempty"` // YYYY-MM-DD or RFC3339, UTC.
	EndedWorkers  bool     `json:"ended_workers,omitempty"`
	DryRun        bool     `json:"dry_run,omitempty"`
	Restore       bool     `json:"restore,omitempty"`
	Expected      []string `json:"expected,omitempty"`
}

type SessionsArchiveResult struct {
	Sessions []SessionArchiveEntry `json:"sessions"`
	Count    int                   `json:"count"`
}

type SessionArchiveEntry struct {
	Project   string `json:"project"`
	Machine   string `json:"machine"`
	LastSeen  int64  `json:"last_seen"`
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
	Folder    string `json:"folder"`
	Harness   string `json:"harness"`
}
