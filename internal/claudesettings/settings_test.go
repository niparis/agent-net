package claudesettings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func settingsFile(t *testing.T) string {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	return File()
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s is not JSON: %v\n%s", path, err, raw)
	}
	return doc
}

func TestEnsureMouseOnKeepsOtherKeys(t *testing.T) {
	path := settingsFile(t)
	original := `{
  "theme": "dark",
  "env": {
    "OTHER": "kept",
    "CLAUDE_CODE_DISABLE_MOUSE": "1"
  },
  "hooks": {"Stop": []}
}
`
	if err := os.WriteFile(path, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureMouseOn(path)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	doc := readJSON(t, path)
	if doc["theme"] != "dark" || doc["hooks"] == nil {
		t.Fatalf("other keys changed: %v", doc)
	}
	env, _ := doc["env"].(map[string]any)
	if env["OTHER"] != "kept" || env[MouseKey] != MouseOn {
		t.Fatalf("env = %v", env)
	}
	if st, err := os.Stat(path); err != nil {
		t.Fatalf("stat: %v", err)
	} else if st.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", st.Mode().Perm())
	}
	changed, err = EnsureMouseOn(path)
	if err != nil || changed {
		t.Fatalf("second run changed=%v err=%v", changed, err)
	}
}

func TestEnsureMouseOnCreatesMissingFile(t *testing.T) {
	path := settingsFile(t)
	changed, err := EnsureMouseOn(path)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	doc := readJSON(t, path)
	env, _ := doc["env"].(map[string]any)
	if len(doc) != 1 || env[MouseKey] != MouseOn {
		t.Fatalf("file = %v", doc)
	}
}

func TestEnsureMouseOnLeavesInvalidJSON(t *testing.T) {
	path := settingsFile(t)
	bad := []byte("{ this is not json ")
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureMouseOn(path)
	if changed || !IsNotJSON(err) {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(bad) {
		t.Fatalf("file changed: %q %v", got, err)
	}
}

func TestEnsureMouseOnReappliesAfterDrift(t *testing.T) {
	path := settingsFile(t)
	if _, err := EnsureMouseOn(path); err != nil {
		t.Fatal(err)
	}
	if changed, err := ensureValue(path, "1"); err != nil || !changed {
		t.Fatalf("drift setup: %v %v", changed, err)
	}
	changed, err := EnsureMouseOn(path)
	if err != nil || !changed {
		t.Fatalf("reapply changed=%v err=%v", changed, err)
	}
	value, present, err := MouseValue(path)
	if err != nil || !present || value != MouseOn {
		t.Fatalf("value=%q present=%v err=%v", value, present, err)
	}
}

// ensureValue writes the raw value for the mouse key, as another tool would.
func ensureValue(path, value string) (bool, error) {
	members, _, err := readFile(path)
	if err != nil {
		return false, err
	}
	env, index, err := envOf(members)
	if err != nil || index < 0 {
		return false, err
	}
	for i := range env {
		if env[i].key == MouseKey {
			env[i].val = json.RawMessage(`"` + value + `"`)
		}
	}
	members[index].val = encodeObject(env)
	return true, writeFile(path, encodeDocument(members))
}

func TestRemoveMouseOnRemovesOnlyTheKey(t *testing.T) {
	path := settingsFile(t)
	if err := os.WriteFile(path, []byte(`{"theme":"dark","env":{"CLAUDE_CODE_DISABLE_MOUSE":"0","OTHER":"kept"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := RemoveMouseOn(path)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	doc := readJSON(t, path)
	env, _ := doc["env"].(map[string]any)
	if doc["theme"] != "dark" || env["OTHER"] != "kept" {
		t.Fatalf("other keys changed: %v", doc)
	}
	if _, set := env[MouseKey]; set {
		t.Fatalf("mouse key stayed: %v", env)
	}
	changed, err = RemoveMouseOn(path)
	if err != nil || changed {
		t.Fatalf("second run changed=%v err=%v", changed, err)
	}
}

func TestRemoveMouseOnKeepsAValueThatIsNotOurs(t *testing.T) {
	path := settingsFile(t)
	if err := os.WriteFile(path, []byte(`{"env":{"CLAUDE_CODE_DISABLE_MOUSE":"1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := RemoveMouseOn(path)
	if err != nil || changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	value, present, err := MouseValue(path)
	if err != nil || !present || value != "1" {
		t.Fatalf("value=%q present=%v err=%v", value, present, err)
	}
}

func TestMouseValueReportsInvalidJSON(t *testing.T) {
	path := settingsFile(t)
	if err := os.WriteFile(path, []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := MouseValue(path); !errors.Is(err, ErrNotJSON) {
		t.Fatalf("err = %v", err)
	}
}

func TestDirFollowsClaudeConfigDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	if got := Dir(); got != dir {
		t.Fatalf("Dir() = %q, want %q", got, dir)
	}
	if got, want := File(), filepath.Join(dir, "settings.json"); got != want {
		t.Fatalf("File() = %q, want %q", got, want)
	}
}
