package logrotate

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// A log file is renamed when the next write would pass the size limit.
func TestRotatesAtTheSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-net-daemon.log")
	w, err := Open(path, 100, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if _, err := w.Write(bytes.Repeat([]byte("a"), 100)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("rotated before the file reached its limit: %v", err)
	}
	if _, err := w.Write([]byte("b")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("did not rotate at the size limit: %v", err)
	}
	rotated, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rotated) != 100 {
		t.Fatalf("rotated file has %d bytes, want 100", len(rotated))
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != "b" {
		t.Fatalf("new log file has %q, want the write after the rotation", current)
	}
}

// Rotation keeps five old files and drops the sixth.
func TestRotationKeepsFiveOldFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-net-gateway.log")
	w, err := Open(path, 10, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Each pair fills the file and then pushes it over the limit, so every
	// pair is one rotation.
	for i := 0; i < 7; i++ {
		if _, err := w.Write(bytes.Repeat([]byte{'a' + byte(i)}, 10)); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 5; i++ {
		if _, err := os.Stat(path + "." + string(rune('0'+i))); err != nil {
			t.Fatalf("missing old file %d: %v", i, err)
		}
	}
	if _, err := os.Stat(path + ".6"); !os.IsNotExist(err) {
		t.Fatalf("kept a sixth old file: %v", err)
	}
}

func TestIsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	same, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer same.Close()
	other, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	if !IsFile(same, path) {
		t.Fatal("the open file was not recognised as the log path")
	}
	if IsFile(other, path) {
		t.Fatal("a different file was recognised as the log path")
	}
	if IsFile(same, filepath.Join(t.TempDir(), "missing")) {
		t.Fatal("a missing path was recognised")
	}
}
