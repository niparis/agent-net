package setup

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSyncDistInvalidatesSignedManifest(t *testing.T) {
	for _, tc := range []struct {
		name      string
		old       string
		new       string
		wantSums  bool
		wantError bool
		blockSums bool
	}{
		{name: "unchanged", old: "same", new: "same", wantSums: true},
		{name: "changed", old: "old", new: "new"},
		{name: "new target", new: "new"},
		{name: "cannot invalidate", old: "old", new: "new", wantError: true, blockSums: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			dist := filepath.Join(dir, "dist")
			if err := os.Mkdir(dist, 0o755); err != nil {
				t.Fatal(err)
			}
			exe := filepath.Join(dir, "agent-net")
			if err := os.WriteFile(exe, []byte(tc.new), 0o755); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(dist, "agent-net-"+runtime.GOOS+"-"+runtime.GOARCH)
			if tc.old != "" {
				if err := os.WriteFile(target, []byte(tc.old), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			sums := filepath.Join(dist, "checksums.txt")
			if tc.blockSums {
				if err := os.Mkdir(sums, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(sums, "child"), []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(sums, []byte("manifest"), 0o644); err != nil {
				t.Fatal(err)
			}
			sig := filepath.Join(dist, "checksums.txt.sig")
			if err := os.WriteFile(sig, []byte("signature"), 0o644); err != nil {
				t.Fatal(err)
			}
			r := &runner{Options: Options{Exe: exe}}
			_, err := r.syncDist(dist)
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "checksum") {
					t.Fatalf("want invalidation error, got %v", err)
				}
				if b, _ := os.ReadFile(target); string(b) != tc.old {
					t.Fatal("binary changed after invalidation failed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if b, _ := os.ReadFile(target); string(b) != tc.new {
				t.Fatalf("binary = %q", b)
			}
			for _, path := range []string{sums, sig} {
				_, err := os.Stat(path)
				if tc.wantSums && err != nil {
					t.Errorf("unchanged binary lost %s: %v", filepath.Base(path), err)
				}
				if !tc.wantSums && !os.IsNotExist(err) {
					t.Errorf("stale %s remains: %v", filepath.Base(path), err)
				}
			}
		})
	}
}
