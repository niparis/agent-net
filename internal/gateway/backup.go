package gateway

import (
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Backup writes a consistent SQLite snapshot and the gateway keys. The
// destination must not exist and must be outside the live data directory.
func (g *Gateway) Backup(dest string) (err error) {
	if !filepath.IsAbs(dest) {
		return errors.New("backup path must be absolute")
	}
	g.backupMu.Lock()
	defer g.backupMu.Unlock()
	return backupStore(dest, g.opts.DataDir, g.store.path)
}

// backupStore writes the snapshot of dbPath and the gateway keys under dataDir. The gateway backup command, the typed Graph cutover and
// the automatic backup before a migration all call this one function.
func backupStore(dest, dataDir, dbPath string) (err error) {
	if !filepath.IsAbs(dest) {
		return errors.New("backup path must be absolute")
	}
	src, err := filepath.Abs(dataDir)
	if err != nil {
		return err
	}
	src, err = filepath.EvalSymlinks(src)
	if err != nil {
		return err
	}
	dest, err = filepath.Abs(dest)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(dest))
	if err != nil {
		return err
	}
	dest = filepath.Join(parent, filepath.Base(dest))
	rel, err := filepath.Rel(src, dest)
	if err != nil {
		return err
	}
	if rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return errors.New("backup must be outside gateway data directory")
	}
	if err = os.Mkdir(dest, 0700); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dest)
		}
	}()
	// Use a separate WAL reader so a slow snapshot does not occupy the
	// store's sole connection and stall node hellos.
	snapshotDB, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer snapshotDB.Close()
	if _, err = snapshotDB.Exec(`VACUUM INTO ?`, filepath.Join(dest, "gateway.db")); err != nil {
		return err
	}
	if err = os.Chmod(filepath.Join(dest, "gateway.db"), 0600); err != nil {
		return err
	}
	for _, name := range []string{"ca.pem", "ca.key", "server-key.pem"} {
		if err = copyPrivateFile(filepath.Join(src, name), filepath.Join(dest, name)); err != nil {
			return err
		}
	}
	// Validate without migrating. A backup taken before an upgrade keeps the
	// old schema, so restoring it and the old binary still works.
	snapshot, err := openSnapshotStore(filepath.Join(dest, "gateway.db"))
	if err != nil {
		return err
	}
	err = validateBackup(snapshot)
	closeErr := snapshot.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func copyPrivateFile(src, dest string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("backup contains a non-regular file")
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	opened, err := in.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return errors.New("backup file changed")
	}
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if e := out.Close(); err == nil {
		err = e
	}
	return err
}
func copyPrivateTree(src, dest string) error {
	return filepath.WalkDir(src, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		out := filepath.Join(dest, rel)
		if e.IsDir() {
			return os.Mkdir(out, 0700)
		}
		if !e.Type().IsRegular() {
			return errors.New("backup contains a non-regular file")
		}
		return copyPrivateFile(p, out)
	})
}

// RestoreBackup checks the database before creating the destination. It never
// replaces an existing destination. A skills.git folder in an older backup is
// not restored: the skill archive holds its skills.
func RestoreBackup(backup, dest string) error {
	backup, err := filepath.Abs(backup)
	if err != nil {
		return err
	}
	backup, err = filepath.EvalSymlinks(backup)
	if err != nil {
		return err
	}
	dest, err = filepath.Abs(dest)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(dest))
	if err != nil {
		return err
	}
	dest = filepath.Join(parent, filepath.Base(dest))
	rel, err := filepath.Rel(backup, dest)
	if err != nil {
		return err
	}
	if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
		return errors.New("restore destination is inside backup")
	}
	if _, err = os.Lstat(dest); err == nil {
		return errors.New("restore destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".agent-net-restore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	for _, name := range []string{"gateway.db", "ca.pem", "ca.key", "server-key.pem"} {
		if err = copyPrivateFile(filepath.Join(backup, name), filepath.Join(stage, name)); err != nil {
			return err
		}
	}
	db, err := OpenStore(filepath.Join(stage, "gateway.db"))
	if err != nil {
		return err
	}
	err = validateBackup(db)
	closeErr := db.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Mkdir(dest, 0700); err != nil {
		return err
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(dest)
		}
	}()
	entries, err := os.ReadDir(stage)
	if err != nil {
		return err
	}
	for _, e := range entries {
		src := filepath.Join(stage, e.Name())
		dst := filepath.Join(dest, e.Name())
		if e.IsDir() {
			err = copyPrivateTree(src, dst)
		} else {
			err = copyPrivateFile(src, dst)
		}
		if err != nil {
			return err
		}
	}
	success = true
	return nil
}
func validateBackup(db *Store) error {
	var result string
	if err := db.db.QueryRow(`PRAGMA integrity_check`).Scan(&result); err != nil || result != "ok" {
		return errors.New("backup database integrity check failed")
	}
	return nil
}
