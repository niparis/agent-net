package protocol

import (
	"reflect"
	"testing"
)

func TestResolveWorkerProfileModel(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		models    []string
		resolved  string
		ambiguous []string
	}{
		{name: "exact wins", model: "openai-codex/gpt-6.1-terra", models: []string{"openai/gpt-6.1-terra", "openai-codex/gpt-6.1-terra"}, resolved: "openai-codex/gpt-6.1-terra"},
		{name: "different provider", model: "openai-codex/gpt-6.1-terra", models: []string{"openai/gpt-6.1-terra"}, resolved: "openai/gpt-6.1-terra"},
		{name: "ambiguous provider", model: "other/x", models: []string{"azure/x", "openai/x"}, ambiguous: []string{"azure/x", "openai/x"}},
		{name: "plain model", model: "gpt-6.1-terra", models: []string{"provider/gpt-6.1-terra"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, ambiguous := ResolveWorkerProfileModel(tt.model, tt.models)
			if resolved != tt.resolved || !reflect.DeepEqual(ambiguous, tt.ambiguous) {
				t.Fatalf("resolved %q, ambiguous %v; want %q, %v", resolved, ambiguous, tt.resolved, tt.ambiguous)
			}
		})
	}
}
