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
	Key  string `json:"key,omitempty"`
	Repo string `json:"repo,omitempty"`
}
type SettingView struct {
	Key         string            `json:"key"`
	Description string            `json:"description"`
	Kind        string            `json:"kind"`
	Type        string            `json:"type"`
	Levels      []string          `json:"levels"`
	Values      []string          `json:"values,omitempty"`
	Live        bool              `json:"live"`
	Value       json.RawMessage   `json:"value"`
	Source      string            `json:"source"`
	Revision    int64             `json:"revision"`
	Explicit    json.RawMessage   `json:"explicit,omitempty"`
	Actor       string            `json:"actor,omitempty"`
	History     []SettingsHistory `json:"history"`
}
