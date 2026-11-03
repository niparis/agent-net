package protocol

import (
	"fmt"
	"sort"
	"strings"
)

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
	if level == "" && harness == "agy" && effort != "" && strings.HasSuffix(base, "-"+effort) {
		base = strings.TrimSuffix(base, "-"+effort)
		level = effort
	}
	if level == "" && harness == "agy" {
		if i := strings.LastIndexByte(base, '-'); i > 0 && dashScoreLevels[strings.ToLower(base[i+1:])] {
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

// ModelID is the inventory identity, including a provider for harnesses whose
// inventories require one. Effort belongs to the agy inventory identity only.
func (s ModelSelection) ModelID() string {
	model := s.Model
	if model == "" {
		return ""
	}
	if s.Provider != "" && (s.Harness == "opencode" || s.Harness == "pi" || s.Harness == "") {
		model = s.Provider + "/" + model
	}
	if s.Harness == "agy" && s.Effort != "" {
		model += "-" + s.Effort
	}
	return model
}

// LegacyModel is the persisted identity used by older daemons and score tables.
func (s ModelSelection) LegacyModel() string {
	model := s.ModelID()
	if model == "" {
		return ""
	}
	if s.Harness != "agy" && s.Effort != "" {
		model += ":" + s.Effort
	}
	return model
}

// Display shows the same four parts for every harness.
func (s ModelSelection) Display() string {
	provider := s.Provider
	if provider == "" {
		provider = ModelVendor(s.Harness, s.Model)
	}
	effort := s.Effort
	if effort == "" {
		effort = "default"
	}
	model := s.Model
	if model == "" {
		model = "default"
	}
	return strings.Join([]string{s.Harness, provider, model, effort}, " · ")
}

// Arguments builds the harness representation. OpenCode's two values are its
// model id and API variant; Codex's value is split by the app-server adapter.
func (s ModelSelection) Arguments(models []string) ([]string, error) {
	switch s.Harness {
	case "codex":
		return codexModelArguments(s)
	case "claude":
		return claudeModelArguments(s)
	case "agy":
		return agyModelArguments(s, models)
	case "opencode":
		return openCodeModelArguments(s)
	case "pi":
		return piModelArguments(s)
	default:
		return nil, fmt.Errorf("unknown harness %q", s.Harness)
	}
}
func codexModelArguments(s ModelSelection) ([]string, error) { return []string{s.LegacyModel()}, nil }
func claudeModelArguments(s ModelSelection) ([]string, error) {
	var args []string
	if s.Model != "" {
		args = append(args, "--model", s.Model)
	}
	if s.Effort != "" {
		args = append(args, "--effort", s.Effort)
	}
	return args, nil
}
func agyModelArguments(s ModelSelection, models []string) ([]string, error) {
	if s.Effort != "" {
		var valid []string
		for _, id := range models {
			if id == s.ModelID() {
				return []string{"--model", id}, nil
			}
			if suffix, ok := strings.CutPrefix(id, s.Model+"-"); ok && suffix != "" && !strings.Contains(suffix, "-") {
				valid = append(valid, suffix)
			}
		}

		sort.Strings(valid)
		list := strings.Join(valid, ", ")
		if list == "" {
			list = "none reported by agy models"
		}
		return nil, fmt.Errorf("agy model %q does not support effort %q; valid efforts: %s", s.Model, s.Effort, list)
	}
	if s.Model == "" {
		return nil, nil
	}
	return []string{"--model", s.Model}, nil
}
func openCodeModelArguments(s ModelSelection) ([]string, error) {
	return []string{s.ModelID(), s.Effort}, nil
}
func piModelArguments(s ModelSelection) ([]string, error) {
	if s.Model == "" {
		return nil, nil
	}
	return []string{"--model", s.LegacyModel()}, nil
}
