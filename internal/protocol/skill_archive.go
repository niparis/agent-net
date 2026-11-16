package protocol

// The skill archive frames (decision 0169). Each one is a plain gateway call:
// the daemon checks that the caller is the owner or the CTO, the gateway
// records the actor.
const (
	FrameSkillArchiveAdd     = "skills.archive_add"
	FrameSkillArchiveAddGit  = "skills.archive_add_git"
	FrameSkillArchiveRemove  = "skills.archive_remove"
	FrameSkillArchiveList    = "skills.archive_list"
	FrameSkillArchiveRead    = "skills.archive_read"
	FrameSkillArchiveUndo    = "skills.archive_undo"
	FrameSkillArchiveIntake  = "skills.archive_intake"
	FrameSkillArchivePromote = "skills.archive_promote"
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
//
// With Version set, only that live version goes. Its content is then kept out of
// later intakes, and the other versions stay.
type SkillArchiveRemove struct {
	Name    string `json:"name"`
	Version int    `json:"version,omitempty"`
	By      string `json:"by"`
}

// SkillArchiveVersion names one version. Zero means the latest live version.
type SkillArchiveVersion struct {
	Name    string `json:"name"`
	Version int    `json:"version,omitempty"`
	By      string `json:"by,omitempty"`
}

// SkillArchivePromote makes one live version the latest of its name. The
// latest is what a node following latest gets (decision 0169).
type SkillArchivePromote struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
	By      string `json:"by"`
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

// ArchivedSkill is the list row: the latest live version of one name. The
// other live versions are its candidates, which a node following latest does
// not get until one is promoted.
type ArchivedSkill struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
	// Versions counts the live versions. Removed counts the versions taken out
	// and kept as tombstones: version numbers are never reused, so the latest
	// version can be above Versions.
	Versions      int    `json:"versions"`
	Removed       int    `json:"removed,omitempty"`
	Candidates    []int  `json:"candidates,omitempty"`
	NoDescription bool   `json:"no_description,omitempty"`
	Hash          string `json:"hash"`
	Description   string `json:"description"`
	// ContextTokens is the name plus the description: what every session loads.
	// SizeTokens is the whole bundle, loaded only when the skill is used.
	ContextTokens int    `json:"context_tokens"`
	SizeTokens    int    `json:"size_tokens"`
	AddedBy       string `json:"added_by"`
	AddedAt       int64  `json:"added_at"`
	Source        string `json:"source"`
	SourceKind    string `json:"source_kind"`
	SourceNode    string `json:"source_node,omitempty"`
	SourceProject string `json:"source_project,omitempty"`
	SourcePath    string `json:"source_path,omitempty"`
	SourceURL     string `json:"source_url,omitempty"`
	SourceCommit  string `json:"source_commit,omitempty"`
	SourceSession string `json:"source_session,omitempty"`
}

// SkillArchiveAddResult says what one add did to one skill.
type SkillArchiveAddResult struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
	// Versions counts the live versions after this change; Removed counts the
	// removed ones that keep their numbers.
	Versions      int    `json:"versions"`
	Removed       int    `json:"removed,omitempty"`
	Hash          string `json:"hash"`
	Description   string `json:"description"`
	ContextTokens int    `json:"context_tokens"`
	SizeTokens    int    `json:"size_tokens"`
	// Source is the one line that says where the stored version came from.
	Source string `json:"source"`
	// Latest is the version a node following latest gets after this change.
	// A candidate that intake stored has Version above Latest.
	Latest int `json:"latest,omitempty"`
	// NoDescription is set when the skill has no description in its front matter.
	NoDescription bool `json:"no_description,omitempty"`
	// State is added, restored, unchanged, removed, promoted or reset.
	State  string `json:"state"`
	Change string `json:"change,omitempty"`
}

// SkillArchiveMultiResult answers a Git add or an intake: every skill it saw,
// and the names that now hold more than one version for the CTO to pick from.
type SkillArchiveMultiResult struct {
	Skills []SkillArchiveAddResult `json:"skills"`
	// Shared names two different contents under one name.
	Shared []string `json:"shared,omitempty"`
	// Machines and Skipped are the intake report: one row for every enrolled
	// machine, and one for every skill that was seen but not stored.
	Machines []SkillArchiveMachine `json:"machines,omitempty"`
	Skipped  []SkillArchiveSkipped `json:"skipped,omitempty"`
}

// SkillArchiveMachine is what the intake did on one enrolled machine. Result
// is scanned, offline or skipped. A scanned machine counts distinct skills (a
// name and its content) and one row per skipped name:
// Found = Added + Existing + Skipped.
type SkillArchiveMachine struct {
	NodeID string `json:"node_id"`
	Name   string `json:"name"`
	Result string `json:"result"`
	Reason string `json:"reason,omitempty"`
	// Read says how many skill folders of each kind the machine answered with,
	// so a missing project folder shows up in the report.
	Read     string `json:"read,omitempty"`
	Found    int    `json:"found"`
	Added    int    `json:"added"`
	Existing int    `json:"existing"`
	Skipped  int    `json:"skipped"`
}

// SkillArchiveSkipped is one skill that the intake saw and did not store, with
// the reason. One row covers every folder that holds the same name on the
// machine. Name is empty when the folder had no readable name.
type SkillArchiveSkipped struct {
	Name      string   `json:"name,omitempty"`
	NodeID    string   `json:"node_id"`
	Machine   string   `json:"machine"`
	Locations []string `json:"locations"`
	Reason    string   `json:"reason"`
}

// FrameNodeSkillSources is the read-only gateway request for the skill folders
// the inventory scan does not list: Claude Code plugin skills and the CTO's
// own skill. The daemon answers with SkillSources and then serves
// node.skill_bundle for each location it listed.
const FrameNodeSkillSources = "node.skill_sources"

// SkillSourcesRequest names the registered projects of the machine. The daemon
// reads the skill folders of each one at the time of the request.
type SkillSourcesRequest struct {
	Projects []ProjectEntry `json:"projects,omitempty"`
}

// SkillSource is one skill folder found by that request. Origin is
// "plugin <name>", "agent-net" or "project <name>"; Project is set for project
// folders. State and Reason follow the inventory items: an unreadable folder is
// listed with its reason.
type SkillSource struct {
	Name     string `json:"name"`
	Hash     string `json:"hash,omitempty"`
	Location string `json:"location"`
	Origin   string `json:"origin"`
	Project  string `json:"project,omitempty"`
	State    string `json:"state"`
	Reason   string `json:"reason,omitempty"`
}

// SkillSourcesResult answers SkillSourcesRequest. ProjectsRead names the
// projects of the request whose folders the daemon read. A daemon that does
// not read projects leaves it empty. UserRead says the answer lists the
// user-level folders as they are on disk now (decision 0169). A daemon without
// it gets its user folders from its last inventory scan.
type SkillSourcesResult struct {
	Sources      []SkillSource `json:"sources"`
	ProjectsRead []string      `json:"projects_read,omitempty"`
	UserRead     bool          `json:"user_read,omitempty"`
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
