package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// forgetSkill drops a manifest reference and the last owned exclude line.
func (d *Daemon) forgetSkill(path string) error {
	manifest := filepath.Join(d.cfg.StateDir, "skill-manifest.json")
	b, err := os.ReadFile(manifest)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var entries []skillManifestEntry
	if err = json.Unmarshal(b, &entries); err != nil {
		return err
	}
	var removed *skillManifestEntry
	kept := make([]skillManifestEntry, 0, len(entries))
	for _, e := range entries {
		if e.Path == path {
			v := e
			removed = &v
		} else {
			kept = append(kept, e)
		}
	}
	if removed == nil {
		return nil
	}
	if removed.ExcludeOwned && removed.Exclude != "" && removed.ExcludeLine != "" {
		referenced := false
		for _, e := range kept {
			if e.Exclude == removed.Exclude && e.ExcludeLine == removed.ExcludeLine {
				referenced = true
				break
			}
		}
		if !referenced {
			if err = removeOwnedExclude(removed.Exclude, removed.ExcludeLine); err != nil {
				return err
			}
		}
	}
	b, err = json.Marshal(kept)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(d.cfg.StateDir, ".skill-manifest-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return os.Rename(name, manifest)
}

func removeOwnedExclude(path, line string) error {
	if !filepath.IsAbs(path) || filepath.Base(path) != "exclude" || filepath.Base(filepath.Dir(path)) != "info" {
		return errors.New("unsafe exclude path")
	}
	root, closeRoot, err := openSkillRoot(filepath.Dir(path), ".")
	if err != nil {
		return err
	}
	defer closeRoot()
	safe := filepath.Join(root, "exclude")
	unlock, err := lockSkillExclude(safe)
	if err != nil {
		return err
	}
	defer unlock()
	before, err := readSkillExclude(safe)
	if err != nil {
		return err
	}
	lines := bytes.SplitAfter(before, []byte("\n"))
	count := 0
	for _, l := range lines {
		if bytes.Equal(l, []byte(line+"\n")) {
			count++
		}
	}
	if count != 1 {
		return nil
	}
	after := bytes.Replace(before, []byte(line+"\n"), nil, 1)
	f, err := os.CreateTemp(root, ".agent-net-exclude-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	info, err := os.Stat(safe)
	if err == nil {
		err = f.Chmod(info.Mode().Perm())
	}
	if err == nil {
		_, err = f.Write(after)
	}
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	current, err := readSkillExclude(safe)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, before) {
		return errors.New("exclude changed")
	}
	return os.Rename(tmp, safe)
}
