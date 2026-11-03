package gateway

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// VACUUM is expensive, so it runs only when deleted rows left more than a
// fifth of the file free. A fresh database is left alone.
func TestVacuumRunsOnlyAboveTheFreePageThreshold(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Now()
	if _, _, ran, err := s.MaybeVacuum(now); err != nil {
		t.Fatal(err)
	} else if ran {
		t.Fatal("vacuumed a database with no free pages")
	}

	addFreePages(t, s)

	before, after, ran, err := s.MaybeVacuum(now)
	if err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("did not vacuum a database with free pages above the threshold")
	}
	if after >= before {
		t.Fatalf("vacuum did not shrink the file: before %d, after %d", before, after)
	}
	if after <= 0 {
		t.Fatalf("database file is missing after the vacuum: size %d", after)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("gateway.db is gone: %v", err)
	}
}

// A vacuum is tried at most once a week, however many free pages appear.
func TestVacuumRunsAtMostWeekly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Now()
	addFreePages(t, s)
	if _, _, ran, err := s.MaybeVacuum(now); err != nil || !ran {
		t.Fatalf("first vacuum: ran=%v err=%v", ran, err)
	}

	addFreePages(t, s)
	if _, _, ran, err := s.MaybeVacuum(now.Add(6 * 24 * time.Hour)); err != nil || ran {
		t.Fatalf("vacuumed again within a week: ran=%v err=%v", ran, err)
	}
	if _, _, ran, err := s.MaybeVacuum(now.Add(8 * 24 * time.Hour)); err != nil || !ran {
		t.Fatalf("did not vacuum a week later: ran=%v err=%v", ran, err)
	}
}

// addFreePages writes rows large enough to matter and deletes them, which
// leaves free pages in the file.
func addFreePages(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vacuum_filler (v BLOB)`); err != nil {
		t.Fatal(err)
	}
	blob := bytes.Repeat([]byte("x"), 4096)
	for i := 0; i < 400; i++ {
		if _, err := s.db.Exec(`INSERT INTO vacuum_filler (v) VALUES (?)`, blob); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`DELETE FROM vacuum_filler`); err != nil {
		t.Fatal(err)
	}
}
