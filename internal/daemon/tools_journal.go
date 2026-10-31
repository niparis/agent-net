package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// skillJournalRecord describes the durable progress of a local skill operation.
// Paths and hashes refer to local data only; journal records are never uploaded.
type skillJournalRecord struct {
	Operation  string `json:"operation"`
	Target     string `json:"target"`
	Backup     string `json:"backup,omitempty"`
	Stage      string `json:"stage,omitempty"`
	BeforeHash string `json:"before_hash,omitempty"`
	AfterHash  string `json:"after_hash,omitempty"`
	Step       string `json:"step"`
}

type skillJournal struct {
	path   string
	record skillJournalRecord
}

func newSkillJournal(state string, record skillJournalRecord) (*skillJournal, error) {
	dir := filepath.Join(state, "skill-journal")
	if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, errors.New("unsafe skill journal directory")
	}
	f, err := os.CreateTemp(dir, ".journal-id-")
	if err != nil {
		return nil, err
	}
	path := f.Name()
	if err = f.Close(); err != nil {
		os.Remove(path)
		return nil, err
	}
	if err = os.Remove(path); err != nil {
		return nil, err
	}
	j := &skillJournal{path: path + ".json", record: record}
	if err = j.save(); err != nil {
		os.Remove(j.path)
		return nil, err
	}
	return j, nil
}

func (j *skillJournal) save() error {
	data, err := json.Marshal(j.record)
	if err != nil {
		return err
	}
	dir := filepath.Dir(j.path)
	f, err := os.CreateTemp(dir, ".journal-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, j.path); err != nil {
		return err
	}
	return syncSkillJournalDir(dir)
}

func (j *skillJournal) finish() error {
	if err := os.Remove(j.path); err != nil {
		return err
	}
	return syncSkillJournalDir(filepath.Dir(j.path))
}

func syncSkillJournalDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
