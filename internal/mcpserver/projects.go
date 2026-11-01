package mcpserver

// The project tool inputs the old agent_project_list name keeps. The new
// tools take the catalog's schemas.

type projectAddIn struct {
	Paths  []string `json:"paths,omitempty" jsonschema:"folders on THIS machine to register: absolute paths, or starting with ~. A folder inside a repository registers the repository's main tree; a git worktree registers its main tree"`
	Dir    string   `json:"dir,omitempty" jsonschema:"with scan=true: a folder on THIS machine to scan for repositories, for example ~/code"`
	Scan   bool     `json:"scan,omitempty" jsonschema:"scan dir for repositories and register them all"`
	Depth  int      `json:"depth,omitempty" jsonschema:"scan: how many folder levels to go down (default 3, at most 8)"`
	DryRun bool     `json:"dry_run,omitempty" jsonschema:"only list what would be registered, change nothing"`
}

type projectListIn struct {
	Name string `json:"name,omitempty" jsonschema:"only projects with this name"`
}
