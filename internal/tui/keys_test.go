package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestKeyBindingsUnique(t *testing.T) {
	seen := map[keyContext]map[string]bool{}
	for _, b := range keyBindings {
		if len(b.keys) == 0 || b.label == "" {
			t.Errorf("%s %q: blank key or label", b.context, b.action)
		}
		if seen[b.context] == nil {
			seen[b.context] = map[string]bool{}
		}
		for _, key := range b.keys {
			if key == "" {
				t.Errorf("%s %q: blank key", b.context, b.action)
			}
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
	m := newModel(120, 34)
	m.helpAll = true
	help := strings.Join(strings.Fields(strings.Join(m.helpContent(120), " ")), " ")
	for _, binding := range keyBindings {
		helpText := binding.help
		if helpText == "" {
			helpText = binding.label
		}
		helpText = strings.ReplaceAll(helpText, "CTO", m.ctoTitle())
		if binding.action == actNotifications {
			helpText = "desktop notifications"
		}
		if strings.Contains(help, helpText) {
			continue
		}
		if binding.action == actFirst || binding.action == actLast {
			continue // the first and last rows share one line
		}
		global := false
		for _, key := range binding.keys {
			if stringIn(key, globalSameKeys) {
				global = true
				break
			}
		}
		if global && !binding.local {
			continue // the fixed Global row covers this meaning
		}
		t.Errorf("help omits %q: %q", binding.context, helpText)
	}
}

func TestHelpGlobalRowsAreFixed(t *testing.T) {
	m := newModel(120, 40)
	var got []string
	for _, line := range m.globalHelpLines(120) {
		got = append(got, strings.Join(strings.Fields(ansi.Strip(line)), " "))
	}
	var want []string
	for _, row := range globalHelpRows {
		want = append(want, row.keys+" "+strings.ReplaceAll(row.help, "CTO", m.ctoTitle()))
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("Global rows:\n got %q\nwant %q", got, want)
	}
}

func stringIn(s string, list []string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// Every action in the table is handled by the package. Shared actions are the
// act constants; every other action is written as a string in a handler, so
// the table cannot name an action that nothing reads.
func TestKeyBindingsEveryActionHasAHandler(t *testing.T) {
	shared := map[actionID]bool{
		actTips: true, actRestartTips: true, actNotifications: true, actQuit: true,
		actClose: true, actAllScreens: true, actDown: true, actUp: true, actPageDown: true,
		actPageUp: true, actFirst: true, actLast: true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var code strings.Builder
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "keys.go" {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		code.Write(data)
		code.WriteByte('\n')
	}
	source := code.String()
	seen := map[actionID]bool{}
	for _, binding := range keyBindings {
		if seen[binding.action] || shared[binding.action] {
			continue
		}
		seen[binding.action] = true
		if !strings.Contains(source, `"`+string(binding.action)+`"`) {
			t.Errorf("action %q has no handler in the package", binding.action)
		}
	}
}
