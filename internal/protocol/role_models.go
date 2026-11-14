package protocol

import (
	"errors"
	"fmt"
	"strings"
)

// RoleModel is one entry of a role's ordered model list: the CTO, the chief
// of staff and the merger each have one (decision 0162). An entry is a full
// candidate, harness and model, so an OpenCode or Pi CTO is just another
// entry. Provider and Effort may be empty.
type RoleModel struct {
	Harness  string `json:"harness"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`
}

// Display is the one-line form a note and the settings text use. An empty
// provider is filled from the model, as the chief.model line always did.
func (r RoleModel) Display() string {
	provider := r.Provider
	if provider == "" {
		provider = ModelVendor(r.Harness, r.Model)
	}
	parts := []string{r.Harness, provider, r.Model, r.Effort}
	for len(parts) > 1 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return strings.Join(parts, " · ")
}

// Key identifies the harness and model of an entry.
func (r RoleModel) Key() string { return r.Harness + "\x00" + r.Model }

// IsZero reports an entry with no harness.
func (r RoleModel) IsZero() bool { return strings.TrimSpace(r.Harness) == "" }

// ParseRoleModelLine reads one entry in the form the chief.model setting uses:
// harness · provider · model · effort, where provider, model and effort may be
// left out.
func ParseRoleModelLine(line string) (RoleModel, error) {
	parts := strings.Split(line, "·")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	if len(parts) == 1 {
		// A single word is a harness; a model needs its harness.
		parts = append(parts, "")
	}
	for len(parts) < 4 {
		parts = append(parts, "")
	}
	if len(parts) > 4 {
		return RoleModel{}, fmt.Errorf("model entry %q has more than harness, provider, model and effort", strings.TrimSpace(line))
	}
	out := RoleModel{Harness: parts[0], Provider: parts[1], Model: parts[2], Effort: parts[3]}
	if out.IsZero() {
		return RoleModel{}, errors.New("model entry has no harness")
	}
	return out, nil
}

// ParseRoleModels reads an ordered list, one entry per line. Blank lines are
// allowed, so a setting may be laid out for reading.
func ParseRoleModels(text string) ([]RoleModel, error) {
	var out []RoleModel
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m, err := ParseRoleModelLine(line)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// FormatRoleModels writes the ordered list in the form ParseRoleModels reads.
func FormatRoleModels(models []RoleModel) string {
	lines := make([]string, 0, len(models))
	for _, m := range models {
		if !m.IsZero() {
			lines = append(lines, m.Display())
		}
	}
	return strings.Join(lines, "\n")
}

// The three roles with an ordered model list (decision 0162).
const (
	RoleCTO    = "cto"
	RoleChief  = "chief"
	RoleMerger = "merger"
)

// RoleModelsKey is the settings key that holds one role's ordered list. The
// CTO list is local daemon state, so its key is only the fresh-install
// default; the daemon sends the list it holds.
func RoleModelsKey(role string) string {
	switch role {
	case RoleCTO:
		return "cto.models"
	case RoleChief:
		return "chief.model"
	case RoleMerger:
		return "merger.models"
	}
	return ""
}

// OpRolePick is the worker frame that asks the one gate for a role's start.
const OpRolePick = "role_pick"

// RolePickRequest asks which model a role session may start on. The CTO sends
// its local ordered list; the chief and merger leave Candidates empty and the
// gateway reads their setting. Resume keeps Current first: a role resumes on
// the model it already runs, and only a locked check moves it to the list.
type RolePickRequest struct {
	Role string `json:"role"`
	// NodeID is the machine that will run the session. Empty means the
	// machine that asks.
	NodeID     string      `json:"node_id,omitempty"`
	Candidates []RoleModel `json:"candidates,omitempty"`
	Current    RoleModel   `json:"current,omitempty"`
	Resume     bool        `json:"resume,omitempty"`
}

// RolePickResult is the gate's answer for a role start. Refused is one line
// when no entry can run now, so the owner sees a reason, never a silent
// failure. NewConversation says the current model failed a locked check, so
// the role falls to its list with a new conversation.
type RolePickResult struct {
	Harness         string `json:"harness,omitempty"`
	Provider        string `json:"provider,omitempty"`
	Model           string `json:"model,omitempty"`
	Effort          string `json:"effort,omitempty"`
	Machine         string `json:"machine,omitempty"`
	Note            string `json:"note,omitempty"`
	Refused         string `json:"refused,omitempty"`
	NewConversation bool   `json:"new_conversation,omitempty"`
}

// RoleModelOf returns the entry a result names.
func (r RolePickResult) RoleModelOf() RoleModel {
	return RoleModel{Harness: r.Harness, Provider: r.Provider, Model: r.Model, Effort: r.Effort}
}
