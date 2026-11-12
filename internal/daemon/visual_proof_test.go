package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVisualProofRetention(t *testing.T) {
	state := t.TempDir()
	now := time.Now()
	for _, tc := range []struct {
		name        string
		days        int
		proof, keep bool
	}{
		{"proof-kept", 29, true, true}, {"proof-expired", 31, true, false}, {"ordinary-expired", 8, false, false},
	} {
		dir := filepath.Join(state, "inbox", "s_test", tc.name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if tc.proof {
			if err := os.WriteFile(filepath.Join(dir, ".visual-proof"), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
		then := now.Add(-time.Duration(tc.days) * 24 * time.Hour)
		if err := os.Chtimes(dir, then, then); err != nil {
			t.Fatal(err)
		}
	}
	cleanFileFolders(state, now, nil, 30*24*time.Hour)
	for _, name := range []string{"proof-expired", "ordinary-expired"} {
		if _, err := os.Stat(filepath.Join(state, "inbox", "s_test", name)); !os.IsNotExist(err) {
			t.Fatalf("kept %s", name)
		}
	}
	if _, err := os.Stat(filepath.Join(state, "inbox", "s_test", "proof-kept")); err != nil {
		t.Fatal(err)
	}
}
