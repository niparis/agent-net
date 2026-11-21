package protocol

import "time"

const (
	InventorySkill   = "skill"
	InventoryUser    = "user"
	InventoryProject = "project"
	InventoryOK      = "ok"
	InventoryStale   = "stale"
	InventoryError   = "error"
)

// InventoryReport is the result of listing the skill folders of one root. It
// holds names, paths, hashes and plain reasons, never skill contents.
type InventoryReport struct {
	ScannedAt time.Time       `json:"scanned_at"`
	Items     []InventoryItem `json:"items"`
	State     string          `json:"state"`
	Reason    string          `json:"reason,omitempty"`
}

// InventoryItem describes one skill folder, or the error that stopped the read
// of one.
type InventoryItem struct {
	Harness     string    `json:"harness"`
	Kind        string    `json:"kind"`
	Scope       string    `json:"scope"`
	Project     string    `json:"project,omitempty"`
	ProjectRoot string    `json:"project_root,omitempty"`
	Location    string    `json:"location"`
	Name        string    `json:"name"`
	Hash        string    `json:"hash,omitempty"`
	ScannedAt   time.Time `json:"scanned_at"`
	State       string    `json:"state"`
	Reason      string    `json:"reason,omitempty"`
}

// ToolImportLocation selects one observed copy by machine and path.
type ToolImportLocation struct {
	NodeID   string `json:"node_id"`
	Location string `json:"location"`
}

// SkillBundleRequest asks a machine for one skill folder's bundle, within the
// skill archive's limit (decision 0169). The machine answers in parts when the
// bundle is larger than one part. Snapshot and Part ask for a later part of
// such an answer.
type SkillBundleRequest struct {
	Name     string `json:"name"`
	Location string `json:"location"`
	Snapshot string `json:"snapshot,omitempty"`
	Part     int    `json:"part,omitempty"`
}

// SkillBundle is the answer. Without Parts, Data is the whole bundle. With
// Parts, Data is part 0 of Parts parts; Snapshot names the bundle for the
// other parts, and Size and SHA256 describe the whole bundle. A later part's
// answer carries only Data.
type SkillBundle struct {
	Data     []byte `json:"data"`
	Snapshot string `json:"snapshot,omitempty"`
	Parts    int    `json:"parts,omitempty"`
	Size     int64  `json:"size,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
}
