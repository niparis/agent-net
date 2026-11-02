package tui

import (
	"strings"
	"testing"
)

func TestKeyBindingsUnique(t *testing.T) {
	seen := map[keyContext]map[string]bool{}
	for _, b := range keyBindings {
		if seen[b.context] == nil {
			seen[b.context] = map[string]bool{}
		}
		for _, key := range b.keys {
			if seen[b.context][key] {
				t.Errorf("duplicate key %q in context %q", key, b.context)
			}
			seen[b.context][key] = true
			if got := keyAction(b.context, key); got != b.action {
				t.Errorf("%s %q: got %q, want %q", b.context, key, got, b.action)
			}
		}
	}
	for _, context := range allKeyContexts {
		if len(seen[context]) == 0 {
			t.Errorf("context %q has no bindings", context)
		}
	}
}

func TestHelpListsEveryBinding(t *testing.T) {
	help := strings.Join(newModel(120, 34).helpContent(120), "\n")
	for _, binding := range keyBindings {
		if !strings.Contains(help, strings.ReplaceAll(string(binding.context), "CTO", "CTO")) {
			t.Errorf("help omits context %q", binding.context)
		}
		if !strings.Contains(help, binding.label) {
			t.Errorf("help omits %q: %q", binding.context, binding.label)
		}
	}
}
