// Package dbfile keeps SQLite database files private to the user.
package dbfile

import (
	"errors"
	"os"
)

// Create makes the database file with mode 0600 when it does not exist, so
// SQLite never creates it with the default umask. SQLite gives the -wal and
// -shm files the mode of the database file.
func Create(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}

// Secure sets mode 0600 on the database file and on its -wal and -shm files,
// where they exist. It fixes files that an older version created with 0644.
func Secure(path string) error {
	var first error
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(p, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) && first == nil {
			first = err
		}
	}
	return first
}
