package protocol

import "strings"

// Candidate is the only type that describes one model a start may use: a
// harness, a provider, a model, an effort and a display name (decision 0162).
// WorkerModelCandidate and RoleModel are names for this same type, so a
// ranker, the gate and a role's ordered list all carry one value.
type Candidate struct {
	Harness  string `json:"harness"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`
	// Name is the display name of a worker profile or a role entry. It is for
	// a note, never for the wire.
	Name string `json:"-"`
	// The rankers fill the rest of the facts for one pick. The gate ignores
	// them, and a role entry leaves them empty.
	DefaultEffort string      `json:"-"`
	Failed        bool        `json:"-"`
	Succeeded     bool        `json:"-"`
	Track         TrackRecord `json:"-"`
	// EarlyQuitLevels marks recent failed attempts only at their difficulty.
	EarlyQuitLevels map[string]bool `json:"-"`
}

// Key identifies a candidate's harness and model for the facts snapshot and
// the rotation state.
func (c Candidate) Key() string { return c.Harness + "\x00" + c.Model }

// Label is the one-line name a note uses for a candidate.
func (c Candidate) Label() string {
	if strings.TrimSpace(c.Name) != "" {
		return c.Name
	}
	return c.Harness + " " + c.Model
}

// Display is the one-line form a role's settings text uses. An empty provider
// is filled from the model, as the chief.model line always did.
func (c Candidate) Display() string {
	provider := c.Provider
	if provider == "" {
		provider = ModelVendor(c.Harness, c.Model)
	}
	parts := []string{c.Harness, provider, c.Model, c.Effort}
	for len(parts) > 1 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return strings.Join(parts, " · ")
}

// QuotaProvider is the provider whose windows limit this candidate.
func (c Candidate) QuotaProvider() string {
	if c.Provider != "" {
		return c.Provider
	}
	return QuotaProviderForStart(c.Harness, c.Model)
}

// IsZero reports an entry with no harness, as an unset role model.
func (c Candidate) IsZero() bool { return strings.TrimSpace(c.Harness) == "" }
