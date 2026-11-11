package setup

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/niparis/agent-net/internal/claudesettings"
)

// settingsMatch compares two settings documents, ignoring the Claude mouse key
// that every setup run writes and an env object that holds nothing else. Every
// other key and value must match.
func settingsMatch(t *testing.T, got, want string) bool {
	t.Helper()
	strip := func(text string) map[string]any {
		var doc map[string]any
		if err := json.Unmarshal([]byte(text), &doc); err != nil {
			t.Fatalf("settings are not JSON: %v\n%s", err, text)
		}
		if env, ok := doc["env"].(map[string]any); ok {
			delete(env, claudesettings.MouseKey)
			if len(env) == 0 {
				delete(doc, "env")
			}
		}
		return doc
	}
	return reflect.DeepEqual(strip(got), strip(want))
}
