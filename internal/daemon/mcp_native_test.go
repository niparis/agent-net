package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

const nativeClaudeFile = "{\n  \"numStartups\": 3,\n  \"mcpServers\": {\n    \"keep\": {\"command\": \"/bin/true\"},\n    \"demo\": {\n      \"command\": \"/bin/echo\",\n      \"args\": [\"hi\"]\n    }\n  },\n  \"theme\": \"dark\"\n}\n"
const nativeClaudeWant = "{\n  \"numStartups\": 3,\n  \"mcpServers\": {\n    \"keep\": {\"command\": \"/bin/true\"}\n  },\n  \"theme\": \"dark\"\n}\n"

type nativeDaemon struct {
	d     *Daemon
	home  string
	state string
	repo  string
}

func newNativeDaemon(t *testing.T, files map[string]string) *nativeDaemon {
	t.Helper()
	n := &nativeDaemon{home: t.TempDir(), state: t.TempDir(), repo: filepath.Join(t.TempDir(), "alpha")}
	if err := os.MkdirAll(n.repo, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		path := strings.ReplaceAll(name, "REPO", n.repo)
		if !filepath.IsAbs(path) {
			path = filepath.Join(n.home, name)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	n.d = &Daemon{cfg: Config{StateDir: n.state, InventoryHome: n.home, CodexHome: filepath.Join(n.home, ".codex")}}
	n.rescan()
	return n
}

func (n *nativeDaemon) rescan() {
	n.d.toolsReport = inventory.Scan(inventory.Options{Home: n.home, CodexHome: n.d.cfg.CodexHome,
		Projects: []protocol.ProjectEntry{{Name: "alpha", Path: n.repo}}})
}

func (n *nativeDaemon) req(op, harness, scope, project, file, name string) protocol.MCPNativeEditRequest {
	return protocol.MCPNativeEditRequest{Op: op, Harness: harness, Scope: scope, Project: project, Location: file, Name: name}
}

func (n *nativeDaemon) claudeUser() protocol.MCPNativeEditRequest {
	return n.req(protocol.MCPNativePlan, "claude", "user", "", filepath.Join(n.home, ".claude.json"), "demo")
}

func readNativeText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestNativeEditRemovesOnlyTheEntryAndBacksUp(t *testing.T) {
	n := newNativeDaemon(t, map[string]string{".claude.json": nativeClaudeFile})
	file := filepath.Join(n.home, ".claude.json")
	plan := n.d.nativeEdit(n.claudeUser())
	if plan.Conflict != "" || plan.FileHash == "" || plan.Committed {
		t.Fatalf("plan: %+v", plan)
	}
	if readNativeText(t, file) != nativeClaudeFile {
		t.Fatal("plan changed the file")
	}
	req := n.claudeUser()
	req.Op, req.FileHash = protocol.MCPNativeApply, plan.FileHash
	res := n.d.nativeEdit(req)
	if res.Conflict != "" || res.PostHash == "" || res.BackupID == "" {
		t.Fatalf("apply: %+v", res)
	}
	if got := readNativeText(t, file); got != nativeClaudeWant {
		t.Fatalf("file:\n%q", got)
	}
	if info, _ := os.Stat(file); info.Mode().Perm() != 0o640 {
		t.Fatalf("file mode changed to %v", info.Mode().Perm())
	}
	// The backup holds the original, is private, and the journal is gone.
	stamp := filepath.Join(n.state, mcpBackupDir, filepath.FromSlash(res.BackupID))
	if readNativeText(t, filepath.Join(stamp, "preimage")) != nativeClaudeFile {
		t.Fatal("backup is not the original")
	}
	for _, p := range []string{filepath.Join(stamp, "preimage"), filepath.Join(stamp, "meta.json")} {
		if info, err := os.Stat(p); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", p, info, err)
		}
	}
	for _, p := range []string{stamp, filepath.Dir(stamp), filepath.Join(n.state, mcpBackupDir)} {
		if info, err := os.Stat(p); err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s: %v %v", p, info, err)
		}
	}
	if left, _ := os.ReadDir(filepath.Join(n.state, "skill-journal")); len(left) != 0 {
		t.Fatalf("journal left: %v", left)
	}
	if _, err := os.Stat(file + ".lock"); !os.IsNotExist(err) {
		t.Fatal("lock left behind")
	}
}

func TestNativeEditRefusesWhenTheFileChangedSincePlan(t *testing.T) {
	n := newNativeDaemon(t, map[string]string{".claude.json": nativeClaudeFile})
	file := filepath.Join(n.home, ".claude.json")
	plan := n.d.nativeEdit(n.claudeUser())
	edited := strings.Replace(nativeClaudeFile, `"dark"`, `"light"`, 1)
	if err := os.WriteFile(file, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	req := n.claudeUser()
	req.Op, req.FileHash = protocol.MCPNativeApply, plan.FileHash
	res := n.d.nativeEdit(req)
	if res.Conflict != "changed since plan" {
		t.Fatalf("apply: %+v", res)
	}
	if readNativeText(t, file) != edited {
		t.Fatal("the file was touched")
	}
	if _, err := os.Stat(filepath.Join(n.state, mcpBackupDir)); !os.IsNotExist(err) {
		t.Fatal("a backup was made for a refused edit")
	}
}

func TestNativeEditUndoRestoresOnlyAnUntouchedPostimage(t *testing.T) {
	n := newNativeDaemon(t, map[string]string{".claude.json": nativeClaudeFile})
	file := filepath.Join(n.home, ".claude.json")
	plan := n.d.nativeEdit(n.claudeUser())
	req := n.claudeUser()
	req.Op, req.FileHash = protocol.MCPNativeApply, plan.FileHash
	applied := n.d.nativeEdit(req)
	undo := protocol.MCPNativeEditRequest{Op: protocol.MCPNativeUndo, Name: "demo", PostHash: applied.PostHash, BackupID: applied.BackupID}

	// A later edit by the user: undo stops and keeps the file.
	later := strings.Replace(nativeClaudeWant, `"dark"`, `"light"`, 1)
	if err := os.WriteFile(file, []byte(later), 0o600); err != nil {
		t.Fatal(err)
	}
	if res := n.d.nativeEdit(undo); res.Conflict != "changed since apply" {
		t.Fatalf("undo after a user edit: %+v", res)
	}
	if readNativeText(t, file) != later {
		t.Fatal("undo overwrote a later edit")
	}

	// With the postimage back, undo restores the original bytes.
	if err := os.WriteFile(file, []byte(nativeClaudeWant), 0o600); err != nil {
		t.Fatal(err)
	}
	if res := n.d.nativeEdit(undo); res.Conflict != "" {
		t.Fatalf("undo: %+v", res)
	}
	if readNativeText(t, file) != nativeClaudeFile {
		t.Fatal("original not restored")
	}
	// A wrong or invented backup ID is refused.
	bad := undo
	bad.BackupID = "../../../etc/passwd"
	if res := n.d.nativeEdit(bad); res.Conflict != "invalid backup" {
		t.Fatalf("bad id: %+v", res)
	}
}

func TestNativeEditRefusesCommittedProjectFile(t *testing.T) {
	n := newNativeDaemon(t, map[string]string{"REPO/.mcp.json": "{\"mcpServers\": {\"demo\": {\"command\": \"/bin/echo\"}}}\n"})
	file := filepath.Join(n.repo, ".mcp.json")
	req := n.req(protocol.MCPNativePlan, "claude", "project", "alpha", file, "demo")
	// Not tracked yet: the file can be edited.
	gitIn(t, n.repo, "init", "-q", "-b", "main")
	n.rescan()
	if res := n.d.nativeEdit(req); res.Conflict != "" || res.Committed {
		t.Fatalf("untracked file: %+v", res)
	}
	gitIn(t, n.repo, "add", ".mcp.json")
	gitIn(t, n.repo, "commit", "-q", "-m", "mcp")
	before := readNativeText(t, file)
	for _, op := range []string{protocol.MCPNativePlan, protocol.MCPNativeApply} {
		req.Op = op
		res := n.d.nativeEdit(req)
		if !res.Committed || res.Conflict != "committed in the repo: remove it with a commit" {
			t.Fatalf("%s: %+v", op, res)
		}
	}
	if readNativeText(t, file) != before {
		t.Fatal("a committed file was edited")
	}
}

func TestNativeEditRefusesAnEntryTheScanDidNotSee(t *testing.T) {
	n := newNativeDaemon(t, map[string]string{".claude.json": nativeClaudeFile})
	req := n.claudeUser()
	req.Name = "ghost"
	if res := n.d.nativeEdit(req); res.Conflict != "the native entry is no longer observed" {
		t.Fatalf("%+v", res)
	}
	// A path outside the known native sources is refused too.
	req = n.claudeUser()
	req.Location = "/etc/hostname"
	if res := n.d.nativeEdit(req); res.Conflict == "" {
		t.Fatalf("%+v", res)
	}
}

func TestNativeEditCodexTomlKeepsComments(t *testing.T) {
	toml := "# keep me\nmodel = \"m\"\n\n[mcp_servers.demo]\n# why\ncommand = \"/bin/echo\"\n\n[mcp_servers.demo.env]\nFOO = \"${FOO}\"\n\n# next\n[mcp_servers.keep]\ncommand = \"/bin/true\"\n"
	n := newNativeDaemon(t, map[string]string{".codex/config.toml": toml})
	file := filepath.Join(n.home, ".codex", "config.toml")
	req := n.req(protocol.MCPNativePlan, "codex", "user", "", file, "demo")
	plan := n.d.nativeEdit(req)
	req.Op, req.FileHash = protocol.MCPNativeApply, plan.FileHash
	if res := n.d.nativeEdit(req); res.Conflict != "" {
		t.Fatalf("%+v", res)
	}
	want := "# keep me\nmodel = \"m\"\n\n# next\n[mcp_servers.keep]\ncommand = \"/bin/true\"\n"
	if got := readNativeText(t, file); got != want {
		t.Fatalf("got %q", got)
	}
}

func TestNativeEditWaitsForClaudeConfigLock(t *testing.T) {
	nativeLockWait = 100 * time.Millisecond
	t.Cleanup(func() { nativeLockWait = 2 * time.Second })
	n := newNativeDaemon(t, map[string]string{".claude.json": nativeClaudeFile})
	file := filepath.Join(n.home, ".claude.json")
	plan := n.d.nativeEdit(n.claudeUser())
	if err := os.Mkdir(file+".lock", 0o700); err != nil {
		t.Fatal(err)
	}
	req := n.claudeUser()
	req.Op, req.FileHash = protocol.MCPNativeApply, plan.FileHash
	if res := n.d.nativeEdit(req); !strings.Contains(res.Conflict, "lock is held") {
		t.Fatalf("%+v", res)
	}
	if readNativeText(t, file) != nativeClaudeFile {
		t.Fatal("edited while the lock was held")
	}
	if _, err := os.Stat(file + ".lock"); err != nil {
		t.Fatal("removed a lock this edit did not take")
	}
}

func TestPruneKeepsMCPBackupsLikeSkillBackups(t *testing.T) {
	d := &Daemon{cfg: Config{StateDir: t.TempDir()}}
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	skillBackupNow = func() time.Time { return now }
	defer func() { skillBackupNow = time.Now }()
	dir := filepath.Join(d.cfg.StateDir, mcpBackupDir, "item")
	mk := func(when time.Time) string {
		p := filepath.Join(dir, when.UTC().Format("20060102T150405.000000000Z")+"-1")
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		return p
	}
	old := mk(now.Add(-31 * 24 * time.Hour))
	fresh := mk(now.Add(-time.Hour))
	d.pruneSkillBackups()
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old MCP backup remains")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("fresh MCP backup removed")
	}
}

// The rename in replaceNative is the last compare-and-swap: it refuses when the
// file no longer has the hash the edit started from.
func TestReplaceNativeRefusesWhenTheFileChanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(path, []byte("one"), 0o640); err != nil {
		t.Fatal(err)
	}
	if why := replaceNative(path, 0o640, []byte("two"), sumHex([]byte("something else"))); why != "changed since plan" {
		t.Fatalf("stale replace: %q", why)
	}
	if readNativeText(t, path) != "one" {
		t.Fatal("a stale replace wrote the file")
	}
	if left, _ := os.ReadDir(dir); len(left) != 1 {
		t.Fatalf("temporary file left: %v", left)
	}
	if why := replaceNative(path, 0o640, []byte("two"), sumHex([]byte("one"))); why != "" {
		t.Fatalf("replace: %q", why)
	}
	if readNativeText(t, path) != "two" {
		t.Fatal("replace did not write")
	}
}

// Undo trusts nothing in the request except the backup ID: the backup must
// match the edit the owner is undoing, and must be unchanged on disk.
func TestNativeEditUndoRefusesAMismatchedOrTamperedBackup(t *testing.T) {
	n := newNativeDaemon(t, map[string]string{".claude.json": nativeClaudeFile})
	file := filepath.Join(n.home, ".claude.json")
	plan := n.d.nativeEdit(n.claudeUser())
	req := n.claudeUser()
	req.Op, req.FileHash = protocol.MCPNativeApply, plan.FileHash
	applied := n.d.nativeEdit(req)
	if applied.Conflict != "" {
		t.Fatalf("apply: %+v", applied)
	}
	good := protocol.MCPNativeEditRequest{Op: protocol.MCPNativeUndo, Name: "demo", PostHash: applied.PostHash, BackupID: applied.BackupID}

	wrongPost := good
	wrongPost.PostHash = sumHex([]byte("not the postimage"))
	if res := n.d.nativeEdit(wrongPost); res.Conflict != "the backup is not the one this edit made" {
		t.Fatalf("wrong post hash: %+v", res)
	}
	wrongName := good
	wrongName.Name = "keep"
	if res := n.d.nativeEdit(wrongName); res.Conflict != "the backup is not the one this edit made" {
		t.Fatalf("wrong name: %+v", res)
	}
	if readNativeText(t, file) != nativeClaudeWant {
		t.Fatal("a refused undo changed the file")
	}

	// A backup whose saved original was altered is not restored.
	pre := filepath.Join(n.state, mcpBackupDir, filepath.FromSlash(applied.BackupID), "preimage")
	if err := os.WriteFile(pre, []byte("{\"mcpServers\":{\"evil\":{\"command\":\"/bin/sh\"}}}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if res := n.d.nativeEdit(good); res.Conflict != "the local backup does not match" {
		t.Fatalf("tampered backup: %+v", res)
	}
	if readNativeText(t, file) != nativeClaudeWant {
		t.Fatal("a tampered backup was restored")
	}
}

// No backup, no edit: if the original cannot be saved, the file stays as it is.
func TestNativeEditDoesNotTouchTheFileWithoutABackup(t *testing.T) {
	n := newNativeDaemon(t, map[string]string{".claude.json": nativeClaudeFile})
	file := filepath.Join(n.home, ".claude.json")
	plan := n.d.nativeEdit(n.claudeUser())
	// A plain file where the backup folder must be makes every backup fail.
	if err := os.WriteFile(filepath.Join(n.state, mcpBackupDir), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	req := n.claudeUser()
	req.Op, req.FileHash = protocol.MCPNativeApply, plan.FileHash
	res := n.d.nativeEdit(req)
	if !strings.Contains(res.Conflict, "cannot save a backup") {
		t.Fatalf("apply: %+v", res)
	}
	if readNativeText(t, file) != nativeClaudeFile {
		t.Fatal("the file was edited without a backup")
	}
}
