package tui

import "testing"

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
}
