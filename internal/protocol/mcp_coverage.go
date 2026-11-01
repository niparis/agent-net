package protocol

// Project MCP coverage: one server the project already declares, missing from
// other harnesses on the same machine. A plan is shown before anything is
// written. Apply checks every file again, saves a backup and writes the
// harness's own format. Undo restores the bytes it found.

// MCPCoverageRequest asks for a plan. The daemon re-reads the project and the
// source declaration; it never trusts the listed paths as files to edit.
type MCPCoverageRequest struct {
	NodeID        string   `json:"node_id,omitempty"`
	Machine       string   `json:"machine,omitempty"`
	Project       string   `json:"project"`
	Checkout      string   `json:"checkout,omitempty"`
	Server        string   `json:"server"`
	SourceHarness string   `json:"source_harness,omitempty"`
	SourcePath    string   `json:"source_path,omitempty"`
	Harnesses     []string `json:"harnesses"`
}

// MCPCoverageTarget is one file the plan writes. It carries paths, hashes and
// names only, never an environment value.
type MCPCoverageTarget struct {
	Harness  string   `json:"harness"`
	Path     string   `json:"path"`
	Key      string   `json:"key,omitempty"`
	Format   string   `json:"format,omitempty"` // json, jsonc or toml
	Action   string   `json:"action"`           // create or update
	PreHash  string   `json:"pre_hash,omitempty"`
	PostHash string   `json:"post_hash,omitempty"`
	State    string   `json:"state"`
	Conflict string   `json:"conflict,omitempty"`
	Preview  []string `json:"preview,omitempty"`
}

// MCPCoveragePlan is what the owner sees before applying.
type MCPCoveragePlan struct {
	ID            string              `json:"id"`
	NodeID        string              `json:"node_id"`
	Machine       string              `json:"machine,omitempty"`
	Project       string              `json:"project"`
	Checkout      string              `json:"checkout"`
	Server        string              `json:"server"`
	SourceHarness string              `json:"source_harness,omitempty"`
	SourcePath    string              `json:"source_path,omitempty"`
	Targets       []MCPCoverageTarget `json:"targets"`
	Conflicts     []string            `json:"conflicts,omitempty"`
	State         string              `json:"state"`
	Actor         string              `json:"actor,omitempty"`
	At            int64               `json:"at,omitempty"`
}

// MCPCoverageApplyRequest applies a plan the owner saw.
type MCPCoverageApplyRequest struct {
	PlanID string `json:"plan_id"`
}

// MCPCoverageUndoRequest reverses one applied coverage plan.
type MCPCoverageUndoRequest struct {
	PlanID string `json:"plan_id"`
}

// MCPCoverageResult answers apply and undo.
type MCPCoverageResult struct {
	PlanID  string              `json:"plan_id"`
	State   string              `json:"state"`
	Targets []MCPCoverageTarget `json:"targets"`
}
