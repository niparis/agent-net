package protocol

import "strings"

const (
	OpProtectedProvidersGet = "protected_providers_get"
	OpProtectedProvidersSet = "protected_providers_set"
)

// ProtectedProviders are subscriptions reserved for the owner's orchestrator.
type ProtectedProviders struct {
	Providers []string `json:"protected_providers"`
	Reason    string   `json:"reason"`
}

// ProfileProvider identifies the provider of a worker profile independently of quota aliases.
func ProfileProvider(harness, model string) string {
	harness, model = strings.ToLower(strings.TrimSpace(harness)), strings.ToLower(strings.TrimSpace(model))
	if harness == "claude" {
		return "anthropic"
	}
	if i := strings.IndexByte(model, '/'); i > 0 {
		return model[:i]
	}
	if strings.HasPrefix(model, "claude-") {
		return "anthropic"
	}
	if harness == "codex" && strings.HasPrefix(model, "gpt-") {
		return "openai"
	}
	return harness
}
