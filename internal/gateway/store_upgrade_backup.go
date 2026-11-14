package gateway

import (
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/sqlitecache"
)

// upgradeBackupDir is the folder beside the gateway data directory that holds
// the automatic backups, next to the older gateway-before-typed-graph folders.
func upgradeBackupDir(dataDir string) string {
	dir, err := filepath.Abs(dataDir)
	if err != nil {
		dir = dataDir
	}
	return filepath.Join(filepath.Dir(dir), "gateway-backups")
}

// upgradeBackupPath names the new folder for the backup of version at now.
// A second upgrade in the same second gets a counted suffix.
func upgradeBackupPath(dataDir, version string, now time.Time) string {
	base := filepath.Join(upgradeBackupDir(dataDir), "pre-v"+backupVersionPart(version)+"-"+now.UTC().Format("20060102-150405"))
	path := base
	for i := 2; ; i++ {
		if _, err := os.Stat(path); err != nil {
			return path
		}
		path = base + "-" + strconv.Itoa(i)
	}
}

// backupVersionPart keeps the release version usable in a folder name.
func backupVersionPart(version string) string {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if version == "" {
		return "unknown"
	}
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '+' {
			return r
		}
		return '-'
	}, version)
}

// storedSchemaVersion reports the highest applied migration. ok is false when
// the file has no version table, so it is new and holds nothing to back up.
func (s *Store) storedSchemaVersion() (version int, ok bool, err error) {
	var tables int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_version'`).Scan(&tables); err != nil {
		return 0, false, err
	}
	if tables == 0 {
		return 0, false, nil
	}
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_version`).Scan(&version); err != nil {
		return 0, false, err
	}
	return version, true, nil
}

// backupBeforeUpgrade writes the same backup as the manual command before a
// migration changes the store, and returns its folder.
func (s *Store) backupBeforeUpgrade(version string, now time.Time) (string, error) {
	dataDir := filepath.Dir(s.path)
	dest := upgradeBackupPath(dataDir, version, now)
	if err := os.MkdirAll(upgradeBackupDir(dataDir), 0700); err != nil {
		return dest, err
	}
	if err := backupStore(dest, dataDir, s.path); err != nil {
		return dest, err
	}
	return dest, nil
}

// backupOldStoreBeforeUpgrade writes the automatic backup when an older release
// made the file and migrations are pending. A fresh file has no version table
// and nothing to protect. A failed backup stops the open, so the gateway never
// migrates without one.
func (s *Store) backupOldStoreBeforeUpgrade(version string) error {
	cur, ok, err := s.storedSchemaVersion()
	if err != nil {
		return err
	}
	if !ok || cur <= 0 || cur >= len(migrations) {
		return nil
	}
	dest, err := s.backupBeforeUpgrade(version, s.now())
	if err != nil {
		s.logf("gateway.backup.before_upgrade_failed", "path", dest, "err", err)
		return upgradeBackupError(dest, err)
	}
	s.logf("gateway.backup.before_upgrade", "path", dest, "size", folderSize(dest))
	return nil
}

// openSnapshotStore opens a backup snapshot for validation without running a
// migration. Read only, so the snapshot keeps the schema of the release that
// took it and an older binary can be restored from it.
func openSnapshotStore(path string) (*Store, error) {
	connector, err := sqlitecache.New("file:"+path+"?mode=ro&_pragma=busy_timeout(5000)", stmtCacheSize)
	if err != nil {
		return nil, err
	}
	wdb := sql.OpenDB(connector)
	wdb.SetMaxOpenConns(1)
	return &Store{db: &storeDB{w: wdb}, connector: connector, path: path, now: time.Now, logf: func(string, ...any) {}, buckets: map[string]*bucket{}}, nil
}

// folderSize is the total size of the regular files under dir, for the log line
// that shows the owner how much disk an automatic backup took.
func folderSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return nil
		}
		if info, err := e.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// upgradeBackupError says plainly that nothing changed, so the log line explains
// why the gateway did not start.
func upgradeBackupError(dest string, err error) error {
	return fmt.Errorf("automatic backup before the upgrade failed at %s, so no migration ran: %w", dest, err)
}
