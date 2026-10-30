package protocol

// The project registry. A project is a git main tree on one machine. The
// machine's own daemon checks the folder and sends the gateway the checked
// entry; the gateway never looks at folders.

// Project registry limits.
const (
	// ProjectMaxPerCall caps the projects one add call may register. A scan
	// that finds more reports the overflow.
	ProjectMaxPerCall = 200
	// DefaultScanDepth is how many folder levels a scan goes down.
	DefaultScanDepth = 3
	// MaxScanDepth is the deepest scan a caller may ask for.
	MaxScanDepth = 8
	// ProjectMaxFieldLen caps each text field of an entry.
	ProjectMaxFieldLen = 4096
)

// Where a listed project comes from.
const (
	SourceRegistered = "registered"
	SourceSeen       = "seen"
	SourceBoth       = "both"
)

// ProjectEntry is one registered project, as the daemon checked it.
type ProjectEntry struct {
	GraphExcluded bool `json:"graph_excluded,omitempty"`
	// Name is the base name of Path.
	Name string `json:"name"`
	// Path is the main working tree of the repository.
	Path string `json:"path"`
	// Remote is the origin URL with credentials removed.
	Remote string `json:"remote,omitempty"`
	// DefaultBranch is the branch origin/HEAD points to, when the clone knows it.
	DefaultBranch string `json:"default_branch,omitempty"`
}

// ProjectAdd is the daemon's "project.add" request. The gateway registers the
// entries on the sending node, whatever the caller says.
type ProjectAdd struct {
	Projects       []ProjectEntry `json:"projects"`
	DryRun         bool           `json:"dry_run,omitempty"`
	AddedBySession string         `json:"added_by_session,omitempty"`
	AddedByHarness string         `json:"added_by_harness,omitempty"`
}

// ProjectAddResult is the gateway's answer to "project.add".
type ProjectAddResult struct {
	Added    []ProjectEntry `json:"added"`
	Existing []ProjectEntry `json:"existing"`
}

// ProjectRemove is the "project.remove" request. Name or Path picks the
// project. On names another machine (host name or node ID); only the command
// line sets it.
type ProjectRemove struct {
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
	On   string `json:"on,omitempty"`
}

// ProjectAddRequest is the local socket method "project_add".
type ProjectAddRequest struct {
	// Paths are folders to register: absolute, or starting with ~.
	Paths []string `json:"paths,omitempty"`
	// Dir with Scan registers the repositories under a folder.
	Dir   string `json:"dir,omitempty"`
	Scan  bool   `json:"scan,omitempty"`
	Depth int    `json:"depth,omitempty"`
	// DryRun lists what would be added and changes nothing.
	DryRun bool `json:"dry_run,omitempty"`
}

// ProjectSkip is a folder that was not registered, with the reason.
type ProjectSkip struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// ProjectAddOutcome is the answer to "project_add".
type ProjectAddOutcome struct {
	// Machine is the host name of the machine that was changed.
	Machine  string         `json:"machine"`
	DryRun   bool           `json:"dry_run"`
	Added    []ProjectEntry `json:"added"`
	Existing []ProjectEntry `json:"existing"`
	Skipped  []ProjectSkip  `json:"skipped"`
	// Overflow counts repositories a scan found beyond the cap. None of them
	// were looked at.
	Overflow int `json:"overflow"`
}
