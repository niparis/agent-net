package protocol

import "encoding/json"

// SettingsChange describes one explicitly stored value. A nil Expect means no row.
type SettingsChange struct {
	Key    string          `json:"key"`
	Level  string          `json:"level"`
	Scope  string          `json:"scope"`
	Item   string          `json:"item,omitempty"`
	Value  json.RawMessage `json:"value,omitempty"`
	Delete bool            `json:"delete,omitempty"`
	Expect json.RawMessage `json:"expect,omitempty"`
}
type SettingsWrite struct {
	Changes []SettingsChange `json:"changes"`
	Reason  string           `json:"reason"`
	By      string           `json:"by,omitempty"`
}
type SettingsUndo struct {
	Revision int64  `json:"revision"`
	By       string `json:"by,omitempty"`
}
type SettingsMove struct {
	Key       string          `json:"key"`
	Scope     string          `json:"scope"`
	Old       json.RawMessage `json:"old"`
	New       json.RawMessage `json:"new"`
	Rewritten bool            `json:"rewritten"`
}
type SettingsResult struct {
	Revision int64          `json:"revision"`
	Moved    []SettingsMove `json:"moved"`
}
type SettingsHistory struct {
	Revision int64           `json:"revision"`
	Seq      int             `json:"seq"`
	Key      string          `json:"key"`
	Level    string          `json:"level"`
	Scope    string          `json:"scope"`
	Item     string          `json:"item,omitempty"`
	Old      json.RawMessage `json:"old,omitempty"`
	New      json.RawMessage `json:"new,omitempty"`
	Actor    string          `json:"actor"`
	Reason   string          `json:"reason"`
	Cause    string          `json:"cause"`
	UndoOf   *int64          `json:"undo_of,omitempty"`
	At       int64           `json:"at"`
}

type SettingsReadRequest struct {
	Key    string          `json:"key,omitempty"`
	Repo   string          `json:"repo,omitempty"`
	Target *SettingsTarget `json:"target,omitempty"`
}

// SettingsTarget names one settings level and, for repo and agent, its display
// name. The level disambiguates kinds: repo shop and agent shop are different
// targets. Network is a singleton and takes no name.
type SettingsTarget struct {
	Level string `json:"level"`
	Name  string `json:"name,omitempty"`
}

// SettingsRef is a resolved target or a source of an effective value. Kind is
// network, repo, agent, or recipe for the standard recipe. Names are
// presentation; ID is storage identity and never changes on rename.
type SettingsRef struct {
	Kind  string `json:"kind"`
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Token string `json:"token,omitempty"`
}

// SettingsTargetChoice is one typed candidate for an ambiguous name.
type SettingsTargetChoice struct {
	Ref  SettingsRef `json:"ref"`
	Name string      `json:"name"`
}

// SettingsTargetResolution is the one-time name lookup. Exactly one of Target
// and Choices is set. An unknown name is an error and never a guess.
type SettingsTargetResolution struct {
	Level   string                 `json:"level"`
	Name    string                 `json:"name,omitempty"`
	Target  *SettingsRef           `json:"target,omitempty"`
	Choices []SettingsTargetChoice `json:"choices,omitempty"`
}

type SettingView struct {
	Key             string            `json:"key"`
	Description     string            `json:"description"`
	Kind            string            `json:"kind"`
	Type            string            `json:"type"`
	Levels          []string          `json:"levels"`
	Values          []string          `json:"values,omitempty"`
	Live            bool              `json:"live"`
	Value           json.RawMessage   `json:"value"`
	Source          string            `json:"source"`
	SourceLevel     string            `json:"source_level,omitempty"`
	SourceRef       *SettingsRef      `json:"source_ref,omitempty"`
	SourceLabel     string            `json:"source_label,omitempty"`
	Revision        int64             `json:"revision"`
	Explicit        json.RawMessage   `json:"explicit,omitempty"`
	ExplicitNote    string            `json:"explicit_note,omitempty"`
	Raised          bool              `json:"raised"`
	Bound           json.RawMessage   `json:"bound,omitempty"`
	RecipeDigest    string            `json:"recipe_digest,omitempty"`
	ContextRevision int64             `json:"context_revision"`
	Target          *SettingsRef      `json:"target,omitempty"`
	Actor           string            `json:"actor,omitempty"`
	History         []SettingsHistory `json:"history"`
	DeliveryStep    *StepRef          `json:"delivery_step,omitempty"`
}
