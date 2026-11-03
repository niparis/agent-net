package protocol

import "strings"

const (
	OpProtectedProvidersGet = "protected_providers_get"
	OpProtectedProvidersSet = "protected_providers_set"
)

// ProtectedProviders are subscriptions reserved for the owner's orchestrator.
type ProtectedProviders struct {
	Providers  []string `json:"protected_providers"`
	Reason     string   `json:"reason"`
	Configured bool     `json:"configured,omitempty"`
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
	if harness == "agy" {
		// The vendor comes from the model id. A Claude model through agy draws
		// on the Antigravity plan, not the owner's Anthropic subscription, so
		// it gets a key the protected-provider rule never matches (decision
		// 0049 D4).
		if vendor := ModelVendor(harness, model); vendor != "anthropic" {
			return vendor
		}
		return "agy-anthropic"
	}
	if strings.HasPrefix(model, "claude-") {
		return "anthropic"
	}
	if harness == "codex" && strings.HasPrefix(model, "gpt-") {
		return "openai"
	}
	return harness
}
