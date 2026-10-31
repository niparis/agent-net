package protocol

import (
	"encoding/json"
	"time"
)

// CTOSettings is the owner's CTO harness and per-harness model preferences,
// plus the rules that keep the one long CTO conversation within its window.
type CTOSettings struct {
	Model         string                `json:"model"`
	Harness       string                `json:"harness"`
	OpenCodeModel string                `json:"opencode_model"`
	ClaudeModel   string                `json:"claude_model"`
	Compaction    CTOCompactionSettings `json:"compaction,omitempty"`
}

// CTOCompactionSettings are the auto-compaction rules. Zero values are
// defaults: 50 percent of context, cache-expiry compaction enabled, and a
// five minute prompt cache window per provider.
type CTOCompactionSettings struct {
	ThresholdPercent   int            `json:"threshold_percent,omitempty"`
	CacheExpiryEnabled *bool          `json:"cache_expiry_enabled,omitempty"`
	CacheWindowSeconds map[string]int `json:"cache_window_seconds,omitempty"`
}

const (
	DefaultCTOCompactionThreshold = 50
	DefaultCTOCacheWindowSeconds  = 5 * 60
)

// Normalized returns the settings with the defaults applied. It never returns
// a nil cache window map.
func (s CTOCompactionSettings) Normalized() CTOCompactionSettings {
	if s.ThresholdPercent <= 0 {
		s.ThresholdPercent = DefaultCTOCompactionThreshold
	}
	if s.CacheExpiryEnabled == nil {
		enabled := true
		s.CacheExpiryEnabled = &enabled
	}
	windows := make(map[string]int, len(s.CacheWindowSeconds))
	for provider, seconds := range s.CacheWindowSeconds {
		if seconds > 0 {
			windows[provider] = seconds
		}
	}
	s.CacheWindowSeconds = windows
	return s
}

// CacheWindow returns the provider's prompt cache window, or the default.
func (s CTOCompactionSettings) CacheWindow(provider string) time.Duration {
	if seconds := s.Normalized().CacheWindowSeconds[provider]; seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return DefaultCTOCacheWindowSeconds * time.Second
}

// CTOSettingsRequest reads the settings when all pointers are nil and saves
// the given fields otherwise. Model is saved for Harness, or for the active
// harness when Harness is omitted. Pointers permit partial updates, including
// a reset of a model to the harness default.
type CTOSettingsRequest struct {
	Model      *string                `json:"model,omitempty"`
	Harness    *string                `json:"harness,omitempty"`
	Compaction *CTOCompactionSettings `json:"compaction,omitempty"`
}

const OpCTOSettings = "cto_settings"

const (
	OpCTOEnter        = "cto_enter"
	OpCTOSend         = "cto_send"
	OpCTONew          = "cto_new"
	OpCTOEvents       = "cto_events"
	OpCTOGraphHistory = "cto_graph_history"
)

type CTOSendRequest struct {
	Text string `json:"text"`
}
type CTOEventsRequest struct {
	// Index is the forward offset; a negative value requests the page before
	// that offset. Zero returns the newest page.
	Index int `json:"index"`
}
type CTOAction struct {
	ID   int64  `json:"id"`
	Text string `json:"text"`
	At   int64  `json:"at"`
}

type CTOEvents struct {
	Model      string            `json:"model,omitempty"`
	GraphAfter int64             `json:"graph_after"`
	Actions    []CTOAction       `json:"actions"`
	Messages   []json.RawMessage `json:"messages"`
	Next       int               `json:"next"`
	Older      int               `json:"older,omitempty"`
}

// CTOGraphChange is an attributed graph history entry, oldest first.
type CTOGraphChange struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	Node     string `json:"node"`
	Attr     string `json:"attr"`
	OldValue string `json:"old_value"`
	NewValue string `json:"new_value"`
	At       int64  `json:"at"`
}
