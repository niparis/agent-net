package gateway

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Backup writes a consistent SQLite snapshot and the private skill repository.
// The destination must not exist and must be outside the live data directory.
func (g *Gateway) Backup(dest string) (err error) {
	if !filepath.IsAbs(dest) {
		return errors.New("backup path must be absolute")
	}
	src, err := filepath.Abs(g.opts.DataDir)
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
	repo, err := g.skillRepository()
	if err != nil {
		return err
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if err = os.Mkdir(dest, 0700); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dest)
		}
	}()
	if _, err = g.store.db.Exec(`VACUUM INTO ?`, filepath.Join(dest, "gateway.db")); err != nil {
		return err
	}
	if err = os.Chmod(filepath.Join(dest, "gateway.db"), 0600); err != nil {
		return err
	}
	if err = copyPrivateTree(repo.path, filepath.Join(dest, "skills.git")); err != nil {
		return err
	}
	for _, name := range []string{"ca.pem", "ca.key", "server-key.pem"} {
		if err = copyPrivateFile(filepath.Join(src, name), filepath.Join(dest, name)); err != nil {
			return err
		}
	}
	snapshot, err := OpenStore(filepath.Join(dest, "gateway.db"))
	if err != nil {
		return err
	}
	err = validateSkillBackup(dest, snapshot)
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

// RestoreBackup validates every stored skill before creating the destination.
// It never replaces an existing destination.
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
	if err = copyPrivateTree(filepath.Join(backup, "skills.git"), filepath.Join(stage, "skills.git")); err != nil {
		return err
	}
	// Never accept alternates, hooks, or Git configuration from an external backup.
	if _, err = os.Lstat(filepath.Join(stage, "skills.git", "objects", "info", "alternates")); err == nil {
		return errors.New("skill store has external objects")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err = os.RemoveAll(filepath.Join(stage, "skills.git", "hooks")); err != nil {
		return err
	}
	if err = os.Mkdir(filepath.Join(stage, "skills.git", "hooks"), 0700); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(stage, "skills.git", "config"), []byte("[core]\n\trepositoryformatversion = 0\n\tfilemode = true\n\tbare = true\n"), 0600); err != nil {
		return err
	}
	db, err := OpenStore(filepath.Join(stage, "gateway.db"))
	if err != nil {
		return err
	}
	err = validateSkillBackup(stage, db)
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
func validateSkillBackup(dir string, db *Store) error {
	var result string
	if err := db.db.QueryRow(`PRAGMA integrity_check`).Scan(&result); err != nil || result != "ok" {
		return errors.New("backup database integrity check failed")
	}
	repo, err := openSkillStore(dir, db)
	if err != nil {
		return errors.New("invalid skill repository")
	}
	if _, err = repo.git(nil, "fsck", "--full"); err != nil {
		return errors.New("skill repository integrity check failed")
	}
	rows, err := db.db.Query(`SELECT item,hash FROM skill_versions`)
	if err != nil {
		return err
	}
	var versions [][2]string
	for rows.Next() {
		var v [2]string
		if err = rows.Scan(&v[0], &v[1]); err != nil {
			rows.Close()
			return err
		}
		versions = append(versions, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, v := range versions {
		if _, err = repo.read(v[0], v[1]); err != nil {
			return fmt.Errorf("skill version validation failed: %w", err)
		}
	}
	return nil
}
