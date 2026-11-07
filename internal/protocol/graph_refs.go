package protocol

import (
	"encoding/json"
	"fmt"
)

// GraphRef identifies a durable record. Names are presentation, never keys.
// Special endpoints are delivery placeholders or the wildcard, not actors.
type GraphRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

func (r GraphRef) Validate() error {
	if r.ID == "" || (r.Kind != "repo" && r.Kind != "agent" && r.Kind != "special" && r.Kind != "team" && r.Kind != "session") {
		return fmt.Errorf("graph reference needs kind and id")
	}
	return nil
}

// EndpointKinds retains the shipped edge meanings, including deploys.
func (e Edge) EndpointKinds() (string, string) {
	if e.Kind == "depends" {
		return "repo", "repo"
	}
	if e.Kind == "governs" {
		return "agent", "agent"
	}
	if len(e.From) >= 10 && e.From[:10] == "/delivery/" {
		return "special", "repo"
	}
	return "agent", "repo"
}

// MarshalJSON keeps the string adapter for legacy callers and emits objects
// for typed reads. Internal routing continues to use the stable string keys.
func (e Edge) MarshalJSON() ([]byte, error) {
	var from, to any = e.From, e.To
	if e.FromRef != nil {
		from = e.FromRef
	}
	if e.ToRef != nil {
		to = e.ToRef
	}
	return json.Marshal(struct {
		From any    `json:"from"`
		To   any    `json:"to"`
		Kind string `json:"kind"`
		Mode string `json:"mode,omitempty"`
		Rule string `json:"rule"`
	}{from, to, e.Kind, e.Mode, e.Rule})
}

func (e *Edge) UnmarshalJSON(b []byte) error {
	var w struct {
		From json.RawMessage `json:"from"`
		To   json.RawMessage `json:"to"`
		Kind string          `json:"kind"`
		Mode string          `json:"mode"`
		Rule string          `json:"rule"`
	}
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*e = Edge{Kind: w.Kind, Mode: w.Mode, Rule: w.Rule}
	decode := func(b json.RawMessage, key *string, ref **GraphRef) error {
		if len(b) == 0 {
			return nil
		}
		if b[0] == '"' {
			return json.Unmarshal(b, key)
		}
		var r GraphRef
		if err := json.Unmarshal(b, &r); err != nil {
			return err
		}
		if err := r.Validate(); err != nil {
			return err
		}
		*ref, *key = &r, r.ID
		return nil
	}
	if err := decode(w.From, &e.From, &e.FromRef); err != nil {
		return err
	}
	return decode(w.To, &e.To, &e.ToRef)
}

func (e PublicGraphEdge) MarshalJSON() ([]byte, error) {
	return json.Marshal(Edge{From: e.From, To: e.To, Kind: e.Kind, Mode: e.Mode, Rule: e.Rule, FromRef: e.FromRef, ToRef: e.ToRef})
}

func (e *PublicGraphEdge) UnmarshalJSON(b []byte) error {
	var edge Edge
	if err := json.Unmarshal(b, &edge); err != nil {
		return err
	}
	*e = PublicGraphEdge{From: edge.From, To: edge.To, Kind: edge.Kind, Mode: edge.Mode, Rule: edge.Rule, FromRef: edge.FromRef, ToRef: edge.ToRef}
	return nil
}

// RefMatches compares typed identities and cannot conflate same-name records.
func RefMatches(a, b *GraphRef) bool { return a != nil && b != nil && *a == *b }
