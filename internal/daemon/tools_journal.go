package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// skillJournalRecord describes the durable progress of a local skill operation.
// Paths and hashes refer to local data only; journal records are never uploaded.
var skillJournalHook func(string) error

func (j *skillJournal) advance(step string) error {
	j.record.Step = step
	if err := j.save(); err != nil {
		return err
	}
	if skillJournalHook != nil {
		return skillJournalHook(step)
	}
	return nil
}

type skillJournalRecord struct {
	Operation  string `json:"operation"`
	Target     string `json:"target"`
	Backup     string `json:"backup,omitempty"`
	Stage      string `json:"stage,omitempty"`
	BeforeHash string `json:"before_hash,omitempty"`
	AfterHash  string `json:"after_hash,omitempty"`
	Item       string `json:"item,omitempty"`
	Revision   int64  `json:"revision,omitempty"`
	// Lock is a Git index.lock this operation creates. LockToken is written
	// into the lock file, so recovery removes only a lock we created.
	Lock      string `json:"lock,omitempty"`
	LockToken string `json:"lock_token,omitempty"`
	// Exclude and ExcludeLine name a Git exclude line this operation added.
	Exclude     string `json:"exclude,omitempty"`
	ExcludeLine string `json:"exclude_line,omitempty"`
	Step        string `json:"step"`
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

// acquireIndexLock records the lock in the journal, then creates it with a
// private token. stable is the durable path stored in the journal; lock is the
// descriptor-anchored path used to create it. busy is true when the lock already exists. After an error
// that is not busy the caller must keep the journal record.
func (j *skillJournal) acquireIndexLock(stable, lock string) (release func(), busy bool, err error) {
	raw := make([]byte, 16)
	if _, err = rand.Read(raw); err != nil {
		return nil, false, err
	}
	token := hex.EncodeToString(raw)
	j.record.Lock, j.record.LockToken = stable, token
	if err = j.advance("locking"); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		if os.IsExist(err) {
			return nil, true, err
		}
		return nil, false, err
	}
	_, err = f.WriteString(token)
	if e := f.Close(); err == nil {
		err = e
	}
	release = func() { removeOwnedIndexLock(lock, token) }
	if err != nil {
		release()
		return nil, false, err
	}
	if err = j.advance("locked"); err != nil {
		release()
		return nil, false, err
	}
	return release, false, nil
}

// removeOwnedIndexLock removes the lock only when it holds our token.
func removeOwnedIndexLock(lock, token string) bool {
	if lock == "" || token == "" {
		return false
	}
	b, err := os.ReadFile(lock)
	if err != nil || string(b) != token {
		return false
	}
	return os.Remove(lock) == nil
}
