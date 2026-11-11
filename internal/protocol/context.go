package protocol

import (
	"math"
	"strings"
	"time"
)

// ClaudeContextLimit is the context window of a Claude model, by family. The
// values are the ones in Claude Code 2.1.314's model list: the 5 series and
// Opus 4.7 and later have 1M, the rest have 200k. An unknown model has no
// limit (0), so the views show no percentage.
func ClaudeContextLimit(model string) int64 {
	m := strings.ToLower(model)
	switch {
	case strings.HasPrefix(m, "claude-haiku-5"),
		strings.HasPrefix(m, "claude-sonnet-5"),
		strings.HasPrefix(m, "claude-opus-5"),
		strings.HasPrefix(m, "claude-opus-4-7"),
		strings.HasPrefix(m, "claude-opus-4-8"),
		strings.HasPrefix(m, "claude-fable-5"),
		strings.HasPrefix(m, "claude-mythos-5"):
		return 1_000_000
	case strings.HasPrefix(m, "claude-"):
		return 200_000
	}
	return 0
}

// ContextFill is the rounded percentage of a context window that tokens fill.
// The boolean is false when either number is unknown.
func ContextFill(tokens, limit int64) (int, bool) {
	if tokens <= 0 || limit <= 0 {
		return 0, false
	}
	return int(math.Round(float64(tokens) / float64(limit) * 100)), true
}

// CompactionSupported says whether agent-net can trigger automatic compaction
// for a harness. A harness with no mechanism is reported, never faked.
func CompactionSupported(harness string) bool {
	switch harness {
	case "claude", "opencode", "cto":
		return true
	}
	return false
}

// CacheWindow is the provider's prompt cache lifetime. The "default" key
// covers providers without their own entry.
func CacheWindow(windows map[string]int64, provider string) time.Duration {
	if seconds := windows[provider]; seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if seconds := windows["default"]; seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return 5 * time.Minute
}
