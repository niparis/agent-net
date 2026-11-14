package setup

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/claudesettings"
)

// settingsMatch compares two settings documents, ignoring the Claude mouse key
// and the tool-decide hook that every setup run writes, and an env or hooks
// object that holds nothing else. Every other key and value must match.
func settingsMatch(t *testing.T, got, want string) bool {
	t.Helper()
	strip := func(text string) map[string]any {
		var doc map[string]any
		if err := json.Unmarshal([]byte(text), &doc); err != nil {
			t.Fatalf("settings are not JSON: %v\n%s", err, text)
		}
		if env, ok := doc["env"].(map[string]any); ok {
			delete(env, claudesettings.MouseKey)
			delete(env, claudesettings.AutoUpdateKey)
			if len(env) == 0 {
				delete(doc, "env")
			}
		}
		if hooks, ok := doc["hooks"].(map[string]any); ok {
			dropToolDecideHook(hooks)
			if len(hooks) == 0 {
				delete(doc, "hooks")
			}
		}
		return doc
	}
	return reflect.DeepEqual(strip(got), strip(want))
}

// dropToolDecideHook removes the groups of the always-on tool-decide hook from
// a decoded hooks object.
func dropToolDecideHook(hooks map[string]any) {
	for event, raw := range hooks {
		groups, _ := raw.([]any)
		var keep []any
		for _, g := range groups {
			if !strings.Contains(fmt.Sprint(g), "tool-decide") {
				keep = append(keep, g)
			}
		}
		if len(keep) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = keep
		}
	}
}
