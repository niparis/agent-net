package protocol

// ProjectsLayoutRequest is the query for projects_layout.
type ProjectsLayoutRequest struct {
	Machine string `json:"machine,omitempty"`
	Caller  string `json:"caller,omitempty"`
}

// LayoutProject is one git repository within a root.
type LayoutProject struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Remote     string `json:"remote,omitempty"`
	LastCommit int64  `json:"last_commit"`
}

// LayoutGroup is a folder holding repositories in a grouped root.
type LayoutGroup struct {
	Name       string   `json:"name"`
	Path       string   `json:"path"`
	Projects   int      `json:"projects"`
	Owners     []string `json:"owners"`
	LastCommit int64    `json:"last_commit"`
}

// LayoutRoot is a folder holding 2 or more git repositories at depth 1 or 2.
type LayoutRoot struct {
	Path     string          `json:"path"`
	Kind     string          `json:"kind"` // "flat" or "grouped"
	Groups   []LayoutGroup   `json:"groups"`
	Projects []LayoutProject `json:"projects"`
}

// MachineLayout is the project layout of one online machine.
type MachineLayout struct {
	Machine   string       `json:"machine"`
	Home      string       `json:"home"`
	Roots     []LayoutRoot `json:"roots"`
	ScannedAt int64        `json:"scanned_at"`
	Truncated bool         `json:"truncated"`
}

// ProjectsLayoutResult is the result returned by projects_layout.
type ProjectsLayoutResult struct {
	Machines []MachineLayout `json:"machines"`
}
