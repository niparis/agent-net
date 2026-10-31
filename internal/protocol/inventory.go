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
	NodeID    string          `json:"node_id,omitempty"`
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
	Files       []string  `json:"files,omitempty"`
}

// InventoryPage bounds each owner response. Next is zero at the end.
type InventoryPage struct {
	Reports []InventoryReport `json:"reports"`
	Next    int               `json:"next,omitempty"`
}

// ToolsFilter selects observed locations. Offset pages the result by item.
type ToolsFilter struct {
	Kind    string `json:"kind,omitempty"`
	Harness string `json:"harness,omitempty"`
	Project string `json:"project,omitempty"`
	State   string `json:"state,omitempty"`
	Offset  int    `json:"offset,omitempty"`
}
type ToolLocation struct {
	NodeID    string `json:"node_id"`
	Machine   string `json:"machine"`
	GraphNode string `json:"graph_node,omitempty"`
	InventoryItem
}
type ToolItem struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	Name       string         `json:"name"`
	Hash       string         `json:"hash"`
	Locations  []ToolLocation `json:"locations"`
	Global     bool           `json:"global"`
	Projects   []string       `json:"projects"`
	Unassigned bool           `json:"unassigned"`
	Harnesses  []string       `json:"harnesses"`
	Machines   []string       `json:"machines"`
	Variants   int            `json:"variants"`
	State      string         `json:"state"`
}
type ToolsPage struct {
	Items []ToolItem `json:"items"`
	Next  int        `json:"next,omitempty"`
}
type ToolVariantDiff struct {
	ID      string   `json:"id"`
	Hash    string   `json:"hash"`
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}
type ToolDetail struct {
	Item         ToolItem          `json:"item"`
	VariantDiffs []ToolVariantDiff `json:"variant_diffs"`
}

// ToolImportLocation selects one observed copy by machine and path.
type ToolImportLocation struct {
	NodeID   string `json:"node_id"`
	Location string `json:"location"`
}
type ToolImport struct {
	Item     string             `json:"item"`
	Location ToolImportLocation `json:"location"`
}
type SkillBundleRequest struct {
	Name     string `json:"name"`
	Hash     string `json:"hash"`
	Location string `json:"location"`
}
type SkillBundle struct {
	Data []byte `json:"data"`
}

// ToolVersionRequest selects a stored skill and optional content-hash version.
type ToolVersionRequest struct {
	Item    string `json:"item"`
	Version string `json:"version,omitempty"`
}

// ToolAssignment is the owner's desired scope, separate from observations.
type ToolAssignment struct {
	Scope    string   `json:"scope"`
	Projects []string `json:"projects,omitempty"`
}
type ToolRevision struct {
	Item       string         `json:"item"`
	Revision   int64          `json:"revision"`
	Assignment ToolAssignment `json:"assignment"`
	Version    string         `json:"version"`
	Actor      string         `json:"actor"`
	Source     string         `json:"source"`
	At         int64          `json:"at"`
	State      string         `json:"state"`
}
