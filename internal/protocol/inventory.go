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

type SkillFile struct {
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	Fingerprint string `json:"fingerprint"`
}

type SkillManifest struct {
	Files    []SkillFile `json:"files,omitempty"`
	Newest   time.Time   `json:"newest"`
	TooLarge bool        `json:"too_large,omitempty"`
}

// InventoryItem describes one observed skill, MCP server, or source error.
type InventoryItem struct {
	Harness            string         `json:"harness"`
	Kind               string         `json:"kind"`
	Scope              string         `json:"scope"`
	Project            string         `json:"project,omitempty"`
	ProjectRoot        string         `json:"project_root,omitempty"`
	Location           string         `json:"location"`
	Name               string         `json:"name"`
	Source             string         `json:"source"`
	Hash               string         `json:"hash,omitempty"`
	ContentFingerprint string         `json:"content_fingerprint,omitempty"`
	Manifest           *SkillManifest `json:"manifest,omitempty"`
	ScannedAt          time.Time      `json:"scanned_at"`
	State              string         `json:"state"`
	Reason             string         `json:"reason,omitempty"`
	Unsupported        []string       `json:"unsupported,omitempty"`
	Files              []string       `json:"files,omitempty"`
	Aliases            []string       `json:"aliases,omitempty"`
	// Tracked is set for a project skill when the repository lists files under
	// it: it is part of the repo. An untracked project skill is a local copy.
	Tracked bool `json:"tracked,omitempty"`
	// Import says whether a native MCP entry can move into the federation. It
	// is set only for native MCP entries and holds names, never values.
	Import *MCPImportInfo `json:"import,omitempty"`
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
	// VersionNo is the agent-net store number (1, 2, ...) of this copy's
	// content, or 0 when no stored version has this content (it was edited
	// outside agent-net, or never imported).
	VersionNo int `json:"version_no,omitempty"`
	InventoryItem
}
type ToolItem struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
	Hash string `json:"hash"`
	// VersionNo is the number of this content in the agent-net store: v1, v2
	// and so on, counted per skill name in import order. Zero means the
	// content is not in the store.
	VersionNo  int            `json:"version_no,omitempty"`
	Locations  []ToolLocation `json:"locations"`
	Global     bool           `json:"global"`
	Projects   []string       `json:"projects"`
	Unassigned bool           `json:"unassigned"`
	Harnesses  []string       `json:"harnesses"`
	Machines   []string       `json:"machines"`
	Variants   int            `json:"variants"`
	State      string         `json:"state"`
	Desired    *ToolRevision  `json:"desired,omitempty"`
	Applied    string         `json:"applied,omitempty"`
	Results    []ToolResult   `json:"results,omitempty"`
	// Federated is true for a server registered with the MCP registry. It has
	// no copies on machines: Scope and ScopeProjects say which sessions can see
	// and call it (a server with no assignment is global), and Host says which
	// machine runs it.
	Federated     bool     `json:"federated,omitempty"`
	Scope         string   `json:"scope,omitempty"`
	ScopeProjects []string `json:"scope_projects,omitempty"`
	Host          string   `json:"host,omitempty"`
	HostOnline    bool     `json:"host_online,omitempty"`
	// Imported says this server came from a native entry. NativeStillPresent
	// is true while that entry is still in the harness config. Conflicts lists
	// a native entry and a federated server that share a name, so the same
	// server is not registered twice without the owner seeing it.
	Imported           *MCPImportRecord `json:"imported,omitempty"`
	NativeStillPresent bool             `json:"native_still_present,omitempty"`
	Conflicts          []string         `json:"conflicts,omitempty"`
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
	// Machines limits the assignment to these machines, by node ID. Empty
	// means every machine, which is what every assignment meant before
	// machine scope existed. It combines with global, projects and
	// uninstalled: "global on research only". Machines the assignment does
	// not name keep whatever they have.
	Machines []string `json:"machines,omitempty"`
}

// OnMachine says whether the assignment applies to a machine.
func (a ToolAssignment) OnMachine(node string) bool {
	if len(a.Machines) == 0 {
		return true
	}
	for _, m := range a.Machines {
		if m == node {
			return true
		}
	}
	return false
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

type ToolPlanTarget struct {
	NodeID      string `json:"node_id"`
	Machine     string `json:"machine"`
	Harness     string `json:"harness"`
	Project     string `json:"project,omitempty"`
	Checkout    string `json:"checkout,omitempty"`
	Source      string `json:"source,omitempty"`
	Destination string `json:"destination,omitempty"`
	Action      string `json:"action"`
	State       string `json:"state"`
	Conflict    string `json:"conflict,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	// Repo removal plans name the local branch, its base commit, and the exact
	// tracked files the daemon will remove in an isolated worktree.
	Branch        string   `json:"branch,omitempty"`
	DefaultBranch string   `json:"default_branch,omitempty"`
	BaseCommit    string   `json:"base_commit,omitempty"`
	Files         []string `json:"files,omitempty"`
	Dirty         bool     `json:"dirty,omitempty"`
}
type ToolPlan struct {
	ID           string           `json:"id"`
	Item         string           `json:"item"`
	Version      string           `json:"version"`
	BaseRevision int64            `json:"base_revision"`
	Assignment   ToolAssignment   `json:"assignment"`
	Targets      []ToolPlanTarget `json:"targets"`
	Operation    string           `json:"operation,omitempty"`
	RepoTargets  []ToolRepoTarget `json:"repo_targets,omitempty"`
	Conflicts    []string         `json:"conflicts"`
	State        string           `json:"state"`
	Actor        string           `json:"actor"`
	At           int64            `json:"at"`
	// UndoOf is the revision that this plan reverses. It is zero for a plan
	// that the owner proposed.
	UndoOf int64 `json:"undo_of,omitempty"`
}
type ToolPlanRequest struct {
	Item        string           `json:"item"`
	Target      ToolAssignment   `json:"target"`
	Operation   string           `json:"operation,omitempty"`
	Project     string           `json:"project,omitempty"`
	RepoTargets []ToolRepoTarget `json:"repo_targets,omitempty"`
}

// ToolRepoTarget selects one registered checkout for a repo-level operation.
type ToolRepoTarget struct {
	NodeID   string `json:"node_id"`
	Checkout string `json:"checkout"`
}

// ToolTargetOutcome is one target's result in an apply or undo answer. State is
// ok, pending, conflict or failed. Reason says why when it is not ok.
type ToolTargetOutcome struct {
	NodeID      string `json:"node_id"`
	Machine     string `json:"machine"`
	Harness     string `json:"harness,omitempty"`
	Project     string `json:"project,omitempty"`
	Checkout    string `json:"checkout,omitempty"`
	Source      string `json:"source,omitempty"`
	Destination string `json:"destination,omitempty"`
	Action      string `json:"action"`
	State       string `json:"state"`
	Reason      string `json:"reason,omitempty"`
	Branch      string `json:"branch,omitempty"`
	Commit      string `json:"commit,omitempty"`
}

// ToolApplyResult answers tools_apply and tools_undo: the new revision and
// what each target reported when the answer was built.
type ToolApplyResult struct {
	ToolRevision
	Targets []ToolTargetOutcome `json:"targets"`
}

// ToolUndoRequest asks to reverse an item's newest applied revision.
type ToolUndoRequest struct {
	Item string `json:"item"`
}
type ToolPlanID struct {
	ID string `json:"plan_id"`
}

type ToolPlanProbe struct {
	Name          string `json:"name"`
	Harness       string `json:"harness"`
	Checkout      string `json:"checkout,omitempty"`
	Source        string `json:"source,omitempty"`
	Hash          string `json:"hash,omitempty"`
	Operation     string `json:"operation,omitempty"`
	Project       string `json:"project,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
	Branch        string `json:"branch,omitempty"`
}
type ToolPlanProbeResult struct {
	Destination string   `json:"destination,omitempty"`
	Conflict    string   `json:"conflict,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"`
	BaseCommit  string   `json:"base_commit,omitempty"`
	Files       []string `json:"files,omitempty"`
	Dirty       bool     `json:"dirty,omitempty"`
}

type ToolAuditEvent struct {
	ID       int64            `json:"id"`
	Item     string           `json:"item"`
	PlanID   string           `json:"plan_id"`
	Action   string           `json:"action"`
	Actor    string           `json:"actor"`
	Revision int64            `json:"revision,omitempty"`
	Targets  []ToolPlanTarget `json:"targets"`
	Result   string           `json:"result"`
	At       int64            `json:"at"`
	// Assignment is the scope the plan or revision asked for, including its
	// machines. It is empty for events from before machine scope existed.
	Assignment *ToolAssignment `json:"assignment,omitempty"`
}

// ToolBackup describes one local backup of an item. It never carries content.
type ToolBackup struct {
	Time   int64  `json:"time"`
	Size   int64  `json:"size"`
	Source string `json:"source"`
}
type ToolHistoryRequest struct {
	Item string `json:"item"`
}

// ToolNeed is advisory charter text whose satisfaction is not inferred.
type ToolNeed struct {
	Node  string `json:"node"`
	Text  string `json:"text"`
	State string `json:"state"`
}

// ToolTarget is an owner-applied plan target for one daemon.
type ToolTarget struct {
	Revision    ToolRevision   `json:"revision"`
	Name        string         `json:"name"`
	Index       int            `json:"index"`
	Target      ToolPlanTarget `json:"target"`
	CopiesReady bool           `json:"copies_ready"`
}

// ToolResult reports the outcome of one target on its owning daemon.
type ToolResult struct {
	Item     string `json:"item"`
	Revision int64  `json:"revision"`
	Index    int    `json:"index"`
	NodeID   string `json:"node_id,omitempty"`
	Harness  string `json:"harness,omitempty"`
	Project  string `json:"project,omitempty"`
	Checkout string `json:"checkout,omitempty"`
	State    string `json:"state"`
	Reason   string `json:"reason,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Commit   string `json:"commit,omitempty"`
}

// ToolAccept marks one difference as accepted as it is, or takes the mark off.
// Key is the stable key of the difference (see internal/toolsdiff).
type ToolAccept struct {
	Key      string `json:"key"`
	Accepted bool   `json:"accepted"`
}
