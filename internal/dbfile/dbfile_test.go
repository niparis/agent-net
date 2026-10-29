package dbfile

import (
	"os"
	"path/filepath"
	"testing"
)

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

func TestCreateMakesPrivateFileAndKeepsContent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.db")
	if err := Create(p); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, p); m != 0o600 {
		t.Fatalf("mode %o, want 600", m)
	}
	os.WriteFile(p, []byte("data"), 0o600)
	if err := Create(p); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "data" {
		t.Fatalf("Create changed an existing file: %q", b)
	}
}

func TestSecureFixesExistingFilesAndIgnoresMissingOnes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.db")
	for _, f := range []string{p, p + "-wal"} {
		os.WriteFile(f, nil, 0o644)
		os.Chmod(f, 0o644)
	}
	if err := Secure(p); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{p, p + "-wal"} {
		if m := mode(t, f); m != 0o600 {
			t.Fatalf("%s mode %o, want 600", f, m)
		}
	}
	if _, err := os.Stat(p + "-shm"); err == nil {
		t.Fatal("Secure must not create files")
	}
}
