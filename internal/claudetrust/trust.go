// Package claudetrust manages trust for agent-net's own Claude directories.
package claudetrust

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Set trusts dir. A folder that is already trusted needs no write, so it does
// not wait for the lock a running Claude Code may hold.
func Set(dir string) error {
	if ok, err := Trusted(dir); err == nil && ok {
		return nil
	}
	return change(dir, true)
}
func Remove(dir string) error { return change(dir, false) }

// staleLock is the age at which Claude Code's config lock counts as left
// behind. Claude Code itself treats its lock as stale after 10 seconds.
const staleLock = 30 * time.Second

// RemoveStaleLock removes Claude Code's config lock when it was left behind:
// Claude Code refreshes the lock while it holds it, so one untouched for
// staleLock belongs to a Claude that crashed. It reports whether it did.
func RemoveStaleLock(lock string) bool {
	info, err := os.Stat(lock)
	if err != nil || time.Since(info.ModTime()) <= staleLock {
		return false
	}
	return os.Remove(lock) == nil
}

// configPath is the Claude Code configuration file that holds folder trust.
func configPath() (dir, path string, err error) {
	dir = os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		if dir, err = os.UserHomeDir(); err != nil {
			return "", "", err
		}
	}
	return dir, filepath.Join(dir, ".claude.json"), nil
}

// Trusted reports whether Claude Code already trusts dir. A folder that was
// never trusted, or a machine with no Claude configuration, is false with no
// error. A configuration that cannot be read or parsed is an error.
func Trusted(dir string) (bool, error) {
	if !filepath.IsAbs(dir) {
		return false, errors.New("Claude trust path must be absolute")
	}
	_, path, err := configPath()
	if err != nil {
		return false, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var root struct {
		Projects map[string]map[string]json.RawMessage `json:"projects"`
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		return false, fmt.Errorf("invalid Claude config")
	}
	entry, ok := root.Projects[dir]
	if !ok {
		return false, nil
	}
	var accepted bool
	if v, ok := entry["hasTrustDialogAccepted"]; ok {
		if err := json.Unmarshal(v, &accepted); err != nil {
			return false, fmt.Errorf("invalid Claude project")
		}
	}
	return accepted, nil
}

func change(dir string, set bool) error {
	if !filepath.IsAbs(dir) {
		return errors.New("Claude trust path must be absolute")
	}
	configDir, path, err := configPath()
	if err != nil {
		return err
	}
	if !set {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return nil
		}
	}
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return err
	}
	// Claude Code uses a directory at this path for its config lock.
	lock := path + ".lock"
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := os.Mkdir(lock, 0700)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if RemoveStaleLock(lock) {
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("Claude config lock is held: %s", lock)
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer os.Remove(lock)
	raw, err := os.ReadFile(path)
	mode := os.FileMode(0600)
	if errors.Is(err, os.ErrNotExist) {
		if !set {
			return nil
		}
		raw = []byte(`{}`)
	} else if err != nil {
		return err
	} else {
		info, e := os.Stat(path)
		if e != nil {
			return e
		}
		mode = info.Mode().Perm()
	}
	var root map[string]json.RawMessage
	if err = json.Unmarshal(raw, &root); err != nil || root == nil {
		return fmt.Errorf("invalid Claude config")
	}
	projects := map[string]json.RawMessage{}
	if v, ok := root["projects"]; ok {
		if err = json.Unmarshal(v, &projects); err != nil || projects == nil {
			return errors.New("invalid Claude projects")
		}
	}
	entry := map[string]json.RawMessage{}
	if v, ok := projects[dir]; ok {
		if err = json.Unmarshal(v, &entry); err != nil || entry == nil {
			return errors.New("invalid Claude project")
		}
	} else if !set {
		return nil
	}
	if set {
		entry["hasTrustDialogAccepted"] = json.RawMessage("true")
	} else {
		delete(entry, "hasTrustDialogAccepted")
	}
	if len(entry) == 0 {
		delete(projects, dir)
	} else {
		projects[dir], err = json.Marshal(entry)
		if err != nil {
			return err
		}
	}
	root["projects"], err = json.Marshal(projects)
	if err != nil {
		return err
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	tmp, err := os.CreateTemp(configDir, ".claude.json.agent-net-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(mode); err == nil {
		_, err = tmp.Write(out)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}
