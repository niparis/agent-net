package protocol

import "strings"

// ModelSelection keeps the caller's model identity separate from its effort.
type ModelSelection struct {
	Harness  string
	Provider string
	Model    string
	Effort   string
}

// ParseModelSelection accepts legacy provider prefixes and effort suffixes.
// Explicit fields take precedence over fields embedded in the model.
func ParseModelSelection(harness, provider, model, effort string) ModelSelection {
	model = strings.TrimSpace(model)
	base, level := SplitModelLevel(model)
	if level == "" && harness == "agy" {
		if i := strings.LastIndexByte(base, '-'); i > 0 && modelLevels[base[i+1:]] {
			base, level = base[:i], base[i+1:]
		}
	}
	if effort == "" {
		effort = level
	}
	if p, m, ok := strings.Cut(base, "/"); ok {
		if provider == "" {
			provider = p
		}
		base = m
	}
	return ModelSelection{Harness: harness, Provider: provider, Model: base, Effort: effort}
}
