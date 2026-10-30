package protocol

import (
	"encoding/json"
	"time"
)

// CTOSettings is the owner's network-wide model preference and the rules that
// keep the one long CTO conversation within its model's window.
type CTOSettings struct {
	Model      string                `json:"model"`
	Compaction CTOCompactionSettings `json:"compaction,omitempty"`
}

// CTOCompactionSettings are the auto-compaction rules. Zero values are
// defaults: 70 percent of the model's context, a 40k floor for the idle rule,
// and a five minute prompt cache window per provider.
type CTOCompactionSettings struct {
	ThresholdPercent   int            `json:"threshold_percent,omitempty"`
	FloorTokens        int64          `json:"floor_tokens,omitempty"`
	CacheWindowSeconds map[string]int `json:"cache_window_seconds,omitempty"`
}

const (
	DefaultCTOCompactionThreshold = 70
	DefaultCTOCompactionFloor     = 40_000
	DefaultCTOCacheWindowSeconds  = 5 * 60
)

// Normalized returns the settings with the defaults applied. It never returns
// a nil cache window map.
func (s CTOCompactionSettings) Normalized() CTOCompactionSettings {
	if s.ThresholdPercent <= 0 {
		s.ThresholdPercent = DefaultCTOCompactionThreshold
	}
	if s.FloorTokens <= 0 {
		s.FloorTokens = DefaultCTOCompactionFloor
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

// CTOSettingsRequest reads the settings when both pointers are nil and saves
// the given fields otherwise. Pointers permit partial updates, including a
// reset of the model to the machine's default.
type CTOSettingsRequest struct {
	Model      *string                `json:"model,omitempty"`
	Compaction *CTOCompactionSettings `json:"compaction,omitempty"`
}

const OpCTOSettings = "cto_settings"

const (
	OpCTOSend         = "cto_send"
	OpCTONew          = "cto_new"
	OpCTOEvents       = "cto_events"
	OpCTOGraphHistory = "cto_graph_history"
)

type CTOSendRequest struct {
	Text string `json:"text"`
}
type CTOEventsRequest struct {
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
