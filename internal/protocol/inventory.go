package protocol

import "time"

const (
	InventorySkill      = "skill"
	InventoryMCP        = "mcp"
	InventoryUser       = "user"
	InventoryProject    = "project"
	InventoryNative     = "native"
	InventoryPlugin     = "plugin"
	InventoryPackage    = "package"
	InventorySystem     = "system"
	InventoryFederation = "agent-net federation"
	InventoryOK         = "ok"
	InventoryStale      = "stale"
	InventoryError      = "error"
)

// InventoryReport is a machine's latest read-only observation. It must not
// contain skill contents or secret values from configuration.
type InventoryReport struct {
	Machine   string          `json:"machine"`
	ScannedAt time.Time       `json:"scanned_at"`
	Items     []InventoryItem `json:"items"`
	State     string          `json:"state"`
	Reason    string          `json:"reason,omitempty"`
}

// InventoryItem describes one observed skill, MCP server, or source error.
type InventoryItem struct {
	Harness     string    `json:"harness"`
	Kind        string    `json:"kind"`
	Scope       string    `json:"scope"`
	Project     string    `json:"project,omitempty"`
	ProjectRoot string    `json:"project_root,omitempty"`
	Location    string    `json:"location"`
	Name        string    `json:"name"`
	Source      string    `json:"source"`
	Hash        string    `json:"hash,omitempty"`
	ScannedAt   time.Time `json:"scanned_at"`
	State       string    `json:"state"`
	Reason      string    `json:"reason,omitempty"`
	Unsupported []string  `json:"unsupported,omitempty"`
}

// InventoryPage bounds each owner response. Next is zero at the end.
type InventoryPage struct {
	Reports []InventoryReport `json:"reports"`
	Next    int               `json:"next,omitempty"`
}
