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

func Set(dir string) error    { return change(dir, true) }
func Remove(dir string) error { return change(dir, false) }

func change(dir string, set bool) error {
	if !filepath.IsAbs(dir) {
		return errors.New("Claude trust path must be absolute")
	}
	configDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if configDir == "" {
		var err error
		configDir, err = os.UserHomeDir()
		if err != nil {
			return err
		}
	}
	path := filepath.Join(configDir, ".claude.json")
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
