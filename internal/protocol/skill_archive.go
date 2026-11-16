package protocol

// The skill archive frames (decision 0169). Each one is a plain gateway call:
// the daemon checks that the caller is the owner or the CTO, the gateway
// records the actor.
const (
	FrameSkillArchiveAdd    = "skills.archive_add"
	FrameSkillArchiveAddGit = "skills.archive_add_git"
	FrameSkillArchiveRemove = "skills.archive_remove"
	FrameSkillArchiveList   = "skills.archive_list"
	FrameSkillArchiveRead   = "skills.archive_read"
	FrameSkillArchiveUndo   = "skills.archive_undo"
	FrameSkillArchiveIntake = "skills.archive_intake"
)

// SkillArchiveAdd asks for one skill folder on an online machine.
type SkillArchiveAdd struct {
	Location ToolImportLocation `json:"location"`
	By       string             `json:"by"`
}

// SkillArchiveAddGit asks for one Git URL. Path limits it to a subfolder.
type SkillArchiveAddGit struct {
	URL  string `json:"url"`
	Path string `json:"path,omitempty"`
	By   string `json:"by"`
}

// SkillArchiveRemove takes a skill name out of the archive for every node.
type SkillArchiveRemove struct {
	Name string `json:"name"`
	By   string `json:"by"`
}

// SkillArchiveVersion names one version. Zero means the latest live version.
type SkillArchiveVersion struct {
	Name    string `json:"name"`
	Version int    `json:"version,omitempty"`
	By      string `json:"by,omitempty"`
}

// SkillArchiveUndo takes back the add or remove with the given change id.
type SkillArchiveUndo struct {
	Change string `json:"change"`
	By     string `json:"by"`
}

// SkillArchiveIntake pulls the skills of the online machines into the archive.
type SkillArchiveIntake struct {
	By string `json:"by"`
}

// SkillArchiveList asks for the archive rows.
type SkillArchiveList struct {
	By string `json:"by"`
}

// ArchivedSkill is the list row: the latest live version of one name.
type ArchivedSkill struct {
	Name          string `json:"name"`
	Version       int    `json:"version"`
	Versions      int    `json:"versions"`
	Hash          string `json:"hash"`
	Description   string `json:"description"`
	Tokens        int    `json:"tokens"`
	AddedBy       string `json:"added_by"`
	AddedAt       int64  `json:"added_at"`
	Source        string `json:"source"`
	SourceKind    string `json:"source_kind"`
	SourceNode    string `json:"source_node,omitempty"`
	SourcePath    string `json:"source_path,omitempty"`
	SourceURL     string `json:"source_url,omitempty"`
	SourceCommit  string `json:"source_commit,omitempty"`
	SourceSession string `json:"source_session,omitempty"`
}

// SkillArchiveAddResult says what one add did to one skill.
type SkillArchiveAddResult struct {
	Name        string `json:"name"`
	Version     int    `json:"version"`
	Versions    int    `json:"versions"`
	Hash        string `json:"hash"`
	Description string `json:"description"`
	Tokens      int    `json:"tokens"`
	// State is added, restored, unchanged or removed.
	State  string `json:"state"`
	Change string `json:"change,omitempty"`
}

// SkillArchiveMultiResult answers a Git add or an intake: every skill it saw,
// and the names that now hold more than one version for the CTO to pick from.
type SkillArchiveMultiResult struct {
	Skills []SkillArchiveAddResult `json:"skills"`
	// Shared names two different contents under one name.
	Shared []string `json:"shared,omitempty"`
}

// SkillArchiveFile is one file of a stored version. Text is empty when the
// file is not bounded UTF-8; Binary says why.
type SkillArchiveFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Text   string `json:"text,omitempty"`
	Binary bool   `json:"binary,omitempty"`
}

// SkillArchiveReadResult is one stored version with its files.
type SkillArchiveReadResult struct {
	Name    string             `json:"name"`
	Version int                `json:"version"`
	Hash    string             `json:"hash"`
	Files   []SkillArchiveFile `json:"files"`
}
