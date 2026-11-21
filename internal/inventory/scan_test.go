package inventory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// userSkills lists the skills of every user folder under home, as the daemon's
// skill sources answer does.
func userSkills(home string) []protocol.InventoryItem {
	var items []protocol.InventoryItem
	for _, root := range UserSkillRoots(home, "") {
		items = append(items, ScanSkillRoot(home, root).Items...)
	}
	return items
}

func writeSkill(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("skill"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestUserAndProjectSkillRootsListEveryHarness(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	for _, path := range []string{
		".claude/skills/u-claude", ".agents/skills/u-agents", ".config/opencode/skills/u-opencode",
		".pi/agent/skills/group/u-nested", ".codex/skills/u-codex", ".gemini/config/skills/u-agy",
	} {
		writeSkill(t, filepath.Join(home, path, "SKILL.md"))
	}
	for _, path := range []string{".claude/skills/p-claude", ".agents/skills/p-agents", ".opencode/skills/p-opencode", ".pi/skills/p-pi", ".agent/skills/p-agy"} {
		writeSkill(t, filepath.Join(project, path, "SKILL.md"))
	}
	found := map[string]bool{}
	for _, item := range userSkills(home) {
		if item.State != protocol.InventoryOK || item.Hash == "" || item.Scope != protocol.InventoryUser {
			t.Errorf("user skill %+v", item)
		}
		found[item.Name] = true
	}
	for _, root := range ProjectSkillRoots("repo", project) {
		for _, item := range ScanSkillRoot(home, root).Items {
			if item.Scope != protocol.InventoryProject || item.Project != "repo" {
				t.Errorf("project skill %+v", item)
			}
			found[item.Name] = true
		}
	}
	for _, name := range []string{"u-claude", "u-agents", "u-opencode", "u-nested", "u-codex", "u-agy", "p-claude", "p-agents", "p-opencode", "p-pi", "p-agy"} {
		if !found[name] {
			t.Errorf("missing %s", name)
		}
	}
}

// A folder without SKILL.md is not a skill, and a missing root lists nothing.
func TestFolderWithoutSkillFileIsNotListed(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude/skills/not-a-skill"), 0700); err != nil {
		t.Fatal(err)
	}
	if items := userSkills(home); len(items) != 0 {
		t.Fatalf("items: %+v", items)
	}
}

func TestSymlinkedRootIsAnError(t *testing.T) {
	home := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(home, ".claude")); err != nil {
		t.Fatal(err)
	}
	report := ScanSkillRoot(home, SkillRoot{Harness: "claude", Base: home, Path: filepath.Join(home, ".claude/skills")})
	if len(report.Items) != 1 || report.Items[0].State != protocol.InventoryError || report.State != protocol.InventoryError {
		t.Fatalf("report: %+v", report)
	}
}

// A link that stays inside the home folder reports the folder it points to.
func TestSkillSymlinkInsideHomeReportsItsTarget(t *testing.T) {
	home := t.TempDir()
	real := filepath.Join(home, ".agents/skills/demo")
	alias := filepath.Join(home, ".claude/skills/demo")
	writeSkill(t, filepath.Join(real, "SKILL.md"))
	if err := os.MkdirAll(filepath.Dir(alias), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../.agents/skills/demo", alias); err != nil {
		t.Fatal(err)
	}
	for _, item := range ScanSkillRoot(home, SkillRoot{Harness: "claude", Base: home, Path: filepath.Dir(alias)}).Items {
		if item.Name == "demo" {
			if item.Location != real || item.Hash == "" || item.State != protocol.InventoryOK {
				t.Fatalf("alias: %+v", item)
			}
			return
		}
	}
	t.Fatal("missing alias")
}

func TestSkillSymlinkOutsideHomeIsNotFollowed(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "SKILL.md"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".claude/skills")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "demo")); err != nil {
		t.Fatal(err)
	}
	for _, item := range userSkills(home) {
		if item.Harness == "claude" && item.Name == "demo" {
			if item.Hash != "" || item.Location != filepath.Join(dir, "demo") || !strings.Contains(item.Reason, outside) {
				t.Fatalf("outside link: %+v", item)
			}
			return
		}
	}
	t.Fatal("missing outside link")
}

// A skill that cannot be hashed is an error row with a reason that names the
// file, and the reason holds no file content.
func TestSkillScanFailureReasons(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, string)
		want  []string
	}{
		{"oversized file", func(t *testing.T, root string) {
			f, e := os.Create(filepath.Join(root, "voice.onnx"))
			if e != nil {
				t.Fatal(e)
			}
			defer f.Close()
			if e = f.Truncate(113895201); e != nil {
				t.Fatal(e)
			}
		}, []string{"64 MiB", "voice.onnx", "114 MB"}},
		{"absolute symlink", func(t *testing.T, root string) {
			if e := os.Symlink(t.TempDir(), filepath.Join(root, "link")); e != nil {
				t.Fatal(e)
			}
		}, []string{"absolute symlink", "link"}},
		{"escaping symlink", func(t *testing.T, root string) {
			if e := os.Symlink("../../outside", filepath.Join(root, "link")); e != nil {
				t.Fatal(e)
			}
		}, []string{"escaping symlink", "link"}},
		{"unreadable file", func(t *testing.T, root string) {
			if os.Geteuid() == 0 {
				t.Skip("root can read mode 000 files")
			}
			path := filepath.Join(root, "private.txt")
			if e := os.WriteFile(path, []byte("not sent"), 0000); e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = os.Chmod(path, 0600) })
		}, []string{"unreadable file", "private.txt", "permission denied"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			root := filepath.Join(home, ".agents/skills/broken")
			writeSkill(t, filepath.Join(root, "SKILL.md"))
			tc.setup(t, root)
			found := false
			for _, item := range userSkills(home) {
				if item.Name != "broken" {
					continue
				}
				found = true
				if item.State != protocol.InventoryError {
					t.Fatalf("expected error: %+v", item)
				}
				for _, want := range tc.want {
					if !strings.Contains(item.Reason, want) {
						t.Errorf("reason %q lacks %q", item.Reason, want)
					}
				}
			}
			if !found {
				t.Fatal("failed skill absent")
			}
		})
	}
}

func TestSkillScanEnvFileIsNotAScanFailure(t *testing.T) {
	root := t.TempDir()
	if e := os.WriteFile(filepath.Join(root, ".env"), []byte("MODE=fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := SkillHash(root); e != nil {
		t.Fatal(e)
	}
}
