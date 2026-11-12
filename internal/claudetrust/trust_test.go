package claudetrust

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
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
func TestTrustedReadsTheOwnerChoice(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	project := filepath.Join(t.TempDir(), "project")
	if trusted, err := Trusted(project); err != nil || trusted {
		t.Fatalf("new config: trusted=%v err=%v", trusted, err)
	}
	if err := Set(project); err != nil {
		t.Fatal(err)
	}
	if trusted, err := Trusted(project); err != nil || !trusted {
		t.Fatalf("after Set: trusted=%v err=%v", trusted, err)
	}
	if trusted, err := Trusted(project + "-other"); err != nil || trusted {
		t.Fatalf("another folder: trusted=%v err=%v", trusted, err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(`{"projects":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Trusted(project); err == nil {
		t.Fatal("a malformed config was read as trusted")
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

// Entering the CTO trusts its folder every time. When the folder is already
// trusted there is nothing to write, so a lock a running Claude holds does not
// block the entry.
func TestSetSkipsTheLockWhenAlreadyTrusted(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	project := filepath.Join(t.TempDir(), "cto")
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(`{"projects":{"`+project+`":{"hasTrustDialogAccepted":true}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".claude.json.lock"), 0700); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := Set(project); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("Set waited %s for a lock it did not need", time.Since(start))
	}
}

// Two callers that start the CTO at once both find the folder untrusted and
// wait for Claude's config lock. The first one writes the answer. The second
// must not rewrite the file once it gets the lock: every write is a sync and a
// rename, and a queue of them on a busy disk is what makes the wait expire.
// The other writer's bytes must survive untouched.
func TestSetKeepsTheAnswerWrittenWhileItWaited(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(dir, ".claude.json")
	project := filepath.Join(t.TempDir(), "cto")
	lock := path + ".lock"
	if err := os.Mkdir(lock, 0700); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- Set(project) }()
	// Set has passed its "already trusted" answer (the file is absent) and is
	// waiting for the lock. A second writer takes the lock, trusts the folder
	// and releases it.
	select {
	case err := <-done:
		t.Fatalf("Set returned while the lock was held: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	written := []byte(`{"projects":{"` + project + `":{"hasTrustDialogAccepted":true}}}`)
	if err := os.WriteFile(path, written, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(written) {
		t.Fatalf("Set rewrote the answer another writer left:\n%s", raw)
	}
}

// Claude Code refreshes its lock while it holds it. A lock untouched for long
// is left by a Claude that crashed: it is removed and the edit goes on.
func TestSetRemovesAStaleLock(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	lock := filepath.Join(dir, ".claude.json.lock")
	if err := os.Mkdir(lock, 0700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), "cto")
	if err := Set(project); err != nil {
		t.Fatal(err)
	}
	if ok, err := Trusted(project); err != nil || !ok {
		t.Fatalf("trusted %v, %v", ok, err)
	}
}
