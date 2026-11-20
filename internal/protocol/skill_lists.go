package protocol

// Skill lists on graph nodes (decision 0169, task 3). A node carries add and
// remove lists; the effective list is the merge along its tree path.
var SkillLevels = []string{"global", "team", "repo", "agent"}

// The node skill list frames. The daemon checks the caller and the gateway
// records the actor, like the skill archive frames.
const (
	FrameSkillsSet       = "skills.set"
	FrameSkillsEffective = "skills.effective"
	FrameSkillsUndo      = "skills.undo"
	// FrameSkillsFolder is the launch read of task 4: one target resolved to
	// its effective skills at their versions.
	FrameSkillsFolder = "skills.folder"
	// FrameSkillsVersion reads one stored version's bundle. The daemon asks
	// for each skill it does not have yet: one bundle is bounded, a whole set
	// is not.
	FrameSkillsVersion = "skills.version"
)

// SkillFolderRequest names the target of a launch. Session resolves to that
// session's Agent, else its Repo; Agent and Repo name one directly; an empty
// request is the global node.
type SkillFolderRequest struct {
	Session string `json:"session,omitempty"`
	Agent   string `json:"agent,omitempty"`
	Repo    string `json:"repo,omitempty"`
	By      string `json:"by"`
}

// SkillFolder is one launch target's set, without the bundles.
type SkillFolder struct {
	Level  string           `json:"level"`
	Scope  string           `json:"scope"`
	Skills []EffectiveSkill `json:"skills"`
}

// SkillVersionRequest asks for one stored version's bundle. Part asks for one
// part of a bundle that is sent in parts (decision 0169); 0 is the first.
type SkillVersionRequest struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
	By      string `json:"by"`
	Part    int    `json:"part,omitempty"`
}

// SkillVersion is one stored version with its bundle and content hash. Parts
// is 0 when Data is the whole bundle. Otherwise Data is part Part of Parts,
// and Size is the size of the whole bundle.
type SkillVersion struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
	Hash    string `json:"hash"`
	Data    []byte `json:"data"`
	Parts   int    `json:"parts,omitempty"`
	Size    int64  `json:"size,omitempty"`
}

// SkillSetRequest replaces one node's own add and remove lists. Pins name a
// version per skill; without one a skill follows its latest live version.
type SkillSetRequest struct {
	Level  string         `json:"level"`
	Scope  string         `json:"scope"`
	Add    []string       `json:"add,omitempty"`
	Remove []string       `json:"remove,omitempty"`
	Pins   map[string]int `json:"pins,omitempty"`
	By     string         `json:"by"`
}

// SkillEffectiveRequest reads one node, or every node that has a list when
// level is empty.
type SkillEffectiveRequest struct {
	Level string `json:"level"`
	Scope string `json:"scope"`
	By    string `json:"by"`
}

// SkillUndoRequest takes back one recorded list change.
type SkillUndoRequest struct {
	Change string `json:"change"`
	By     string `json:"by"`
}

// EffectiveSkill is one skill a node gets, with the node that gave it. Version
// is the pinned or latest live version; 0 means the archive no longer has it.
type EffectiveSkill struct {
	Name        string `json:"name"`
	Version     int    `json:"version"`
	Hash        string `json:"hash,omitempty"`
	Pin         int    `json:"pin,omitempty"`
	PinMissing  bool   `json:"pin_missing,omitempty"`
	Level       string `json:"level"`
	Scope       string `json:"scope"`
	Description string `json:"description,omitempty"`
	// ContextTokens is what a session loads for this skill at start. SizeTokens
	// is the whole bundle, loaded only when the skill is used.
	ContextTokens int `json:"context_tokens"`
	SizeTokens    int `json:"size_tokens"`
}

// EffectiveSkills is one node's resolved list.
type EffectiveSkills struct {
	Level  string           `json:"level"`
	Scope  string           `json:"scope"`
	Skills []EffectiveSkill `json:"skills"`
	// ContextTokens totals the context figure of the skills.
	ContextTokens int `json:"context_tokens"`
}

// SkillRestartSession is a live session that still runs the old set.
type SkillRestartSession struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name,omitempty"`
	Level     string `json:"level,omitempty"`
	Scope     string `json:"scope,omitempty"`
}

// SkillRestartOffer is how the CTO restarts those sessions.
type SkillRestartOffer struct {
	Sessions []SkillRestartSession `json:"sessions"`
	Call     string                `json:"call"`
}

// SkillSetResult is the diff one set made, with undo.
type SkillSetResult struct {
	Level    string   `json:"level"`
	Scope    string   `json:"scope"`
	Added    []string `json:"added,omitempty"`
	Removed  []string `json:"removed,omitempty"`
	Pinned   []string `json:"pinned,omitempty"`
	Unpinned []string `json:"unpinned,omitempty"`
	// BeforeTokens and AfterTokens total the context figure.
	BeforeTokens int               `json:"before_tokens"`
	AfterTokens  int               `json:"after_tokens"`
	Effective    []EffectiveSkill  `json:"effective"`
	Change       string            `json:"change,omitempty"`
	Restart      SkillRestartOffer `json:"restart"`
	// PinnedByMaturity says the adds were pinned because the repo is mature
	// or critical (decision 0169).
	PinnedByMaturity bool `json:"pinned_by_maturity,omitempty"`
}
