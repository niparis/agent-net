package protocol

import "strings"

const (
	OpWorkerProvidersGet = "worker_providers_get"
	OpWorkerProvidersSet = "worker_providers_set"
)

// WorkerProviders holds an explicit network allowlist. Nil means the defaults.
type WorkerProviders struct {
	Providers []string `json:"providers"`
}

// WorkerProvider returns the quota provider or, for an unmapped provider, its ID.
func WorkerProvider(harness, model string) string {
	if p := QuotaProviderForStart(harness, model); p != "" {
		return p
	}
	if i := strings.IndexByte(model, '/'); i > 0 {
		return strings.ToLower(model[:i])
	}
	return strings.ToLower(harness)
}

// WorkerProviderAllowed applies the owner list or the subscription defaults.
func WorkerProviderAllowed(harness, model string, allowed []string, quotas *Quotas) bool {
	p := WorkerProvider(harness, model)
	if allowed != nil {
		for _, name := range allowed {
			if mapped := QuotaProviderForModelProvider(name); mapped != "" {
				name = mapped
			}
			if strings.EqualFold(name, p) {
				return true
			}
		}
		return false
	}
	return SubscriptionProvider(harness, model, quotas)
}

// SubscriptionProvider tells whether a start's model draws on a subscription
// or a provider with a usable quota reading. It is the rule the worker provider
// defaults use, kept here so model resolution and the defaults never disagree.
// Everything else is pay-per-use, such as OpenRouter and OpenCode Zen
// (the `opencode/` provider).
func SubscriptionProvider(harness, model string, quotas *Quotas) bool {
	p := WorkerProvider(harness, model)
	switch {
	case harness == "claude" || harness == "codex" || harness == "agy":
		return true
	case p == "codex" || p == "opencode-go":
		return true
	}
	reading, ok := quotas.Provider(p)
	return ok && !reading.Stale && reading.Error == "" && len(reading.Windows) > 0
}

// PayPerUseProvider is the opposite of SubscriptionProvider: the model's
// provider bills per use, so a loose name must not resolve to it.
func PayPerUseProvider(harness, model string, quotas *Quotas) bool {
	return !SubscriptionProvider(harness, model, quotas)
}
