package inventory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

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
			if e := os.MkdirAll(root, 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("# Fixture"), 0600); e != nil {
				t.Fatal(e)
			}
			tc.setup(t, root)
			report := Scan(Options{Home: home})
			found := false
			for _, item := range report.Items {
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

func TestSkillInventoryMergeRetainsFailureReason(t *testing.T) {
	old := protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{{Kind: "skill", Name: "bad", Location: "/skills/bad", Hash: "old", State: "ok"}}}
	failed := protocol.InventoryItem{Kind: "skill", Name: "bad", Location: "/skills/bad", State: "error", Reason: "absolute symlink (link)"}
	for _, partial := range []bool{false, true} {
		fresh := protocol.InventoryReport{State: "error", Reason: "one or more sources could not be scanned", Items: []protocol.InventoryItem{failed}}
		if partial {
			fresh.Items = append(fresh.Items, protocol.InventoryItem{Kind: "skill", Name: "good", State: "ok"})
		}
		report := MergeReports(old, fresh)
		report = MergeReports(report, fresh)
		if report.Items[0].Reason != failed.Reason || report.Items[0].Hash != "old" || report.Items[0].State != "stale" {
			t.Fatalf("partial=%v: %+v", partial, report)
		}
	}
}
