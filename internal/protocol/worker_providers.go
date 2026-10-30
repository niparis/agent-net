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
	if harness == "claude" || harness == "codex" {
		return true
	}
	if p == "codex" || p == "opencode-go" {
		return true
	}
	reading, ok := quotas.Provider(p)
	return ok && !reading.Stale && reading.Error == "" && len(reading.Windows) > 0
}
