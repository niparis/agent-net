package claudetrust

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestTrustPreservesConfigAndRemovesOnlyTrust(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(dir, ".claude.json")
	project := filepath.Join(t.TempDir(), "worktree")
	if err := os.WriteFile(path, []byte(`{"extra":{"keep":true},"projects":{"/other":{"x":1}}}`), 0640); err != nil {
		t.Fatal(err)
	}
	if err := Set(project); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err = json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	p := v["projects"].(map[string]any)
	if p[project].(map[string]any)["hasTrustDialogAccepted"] != true || p["/other"] == nil || v["extra"] == nil {
		t.Fatalf("config: %s", raw)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0640 {
		t.Fatalf("mode: %v", info.Mode())
	}
	if err := Remove(project); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	json.Unmarshal(raw, &v)
	p = v["projects"].(map[string]any)
	if _, ok := p[project]; ok || p["/other"] == nil {
		t.Fatalf("removed: %s", raw)
	}
}
func TestConcurrentTrustWrites(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	base := t.TempDir()
	var wg sync.WaitGroup
	for _, n := range []string{"one", "two", "three"} {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			if err := Set(filepath.Join(base, n)); err != nil {
				t.Error(err)
			}
		}(n)
	}
	wg.Wait()
	raw, err := os.ReadFile(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Projects map[string]any `json:"projects"`
	}
	if err = json.Unmarshal(raw, &v); err != nil || len(v.Projects) != 3 {
		t.Fatalf("config: %s %v", raw, err)
	}
}

func TestMalformedConfigIsNotChanged(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(dir, ".claude.json")
	raw := []byte(`{"projects":[]}`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Set(filepath.Join(dir, "project")); err == nil {
		t.Fatal("accepted invalid projects")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(raw) {
		t.Fatalf("config changed: %s %v", got, err)
	}
}
func TestRemovePreservesProjectFields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	project := filepath.Join(dir, "project")
	path := filepath.Join(dir, ".claude.json")
	data, _ := json.Marshal(map[string]any{"projects": map[string]any{project: map[string]any{"hasTrustDialogAccepted": true, "mcpServers": map[string]any{"keep": true}}}})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Remove(project); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var root struct {
		Projects map[string]map[string]json.RawMessage `json:"projects"`
	}
	if json.Unmarshal(raw, &root) != nil || root.Projects[project]["mcpServers"] == nil || root.Projects[project]["hasTrustDialogAccepted"] != nil {
		t.Fatalf("config: %s", raw)
	}
}
