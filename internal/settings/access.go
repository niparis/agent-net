package settings

import (
	"encoding/json"
	"time"
)

// String, Bool and Duration read typed recipe values from a snapshot. The
// recipe is validated at startup and invalid stored values are ignored by Resolve.
func (s Snapshot) String(key string, w Where) string {
	e, err := s.Resolve(key, w)
	if err != nil {
		return ""
	}
	var v string
	_ = json.Unmarshal(e.Value, &v)
	return v
}
func (s Snapshot) Bool(key string, w Where) bool {
	e, err := s.Resolve(key, w)
	if err != nil {
		return false
	}
	var v bool
	_ = json.Unmarshal(e.Value, &v)
	return v
}
func (s Snapshot) Duration(key string, w Where) time.Duration {
	v, _ := time.ParseDuration(s.String(key, w))
	return v
}
func (s Snapshot) Strings(key string, w Where) []string {
	e, err := s.Resolve(key, w)
	if err != nil {
		return nil
	}
	var v []string
	_ = json.Unmarshal(e.Value, &v)
	return v
}
