package gateway

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type skillVersion struct {
	Item       string `json:"item"`
	Hash       string `json:"hash"`
	Commit     string `json:"commit"`
	NodeID     string `json:"node_id"`
	Location   string `json:"location"`
	ImportedAt int64  `json:"imported_at"`
}
type skillStore struct {
	path string
	db   *Store
	mu   sync.Mutex
}

func openSkillStore(dir string, db *Store) (*skillStore, error) {
	path := filepath.Join(dir, "skills.git")
	if err := os.MkdirAll(path, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0700); err != nil {
		return nil, err
	}
	s := &skillStore{path: path, db: db}
	if _, err := os.Stat(filepath.Join(path, "HEAD")); errors.Is(err, os.ErrNotExist) {
		if _, err = s.git(nil, "init", "--bare", "--quiet"); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return s, nil
}
func (s *skillStore) git(input []byte, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"--git-dir", s.path}, args...)...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + s.path, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_AUTHOR_NAME=agent-net", "GIT_AUTHOR_EMAIL=agent-net@localhost", "GIT_COMMITTER_NAME=agent-net", "GIT_COMMITTER_EMAIL=agent-net@localhost", "LC_ALL=C"}
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("skill store git %s failed: %w", args[0], err)
	}
	return strings.TrimSpace(string(out)), nil
}
func skillPath(item string) string {
	sum := sha256.Sum256([]byte(item))
	return hex.EncodeToString(sum[:]) + "/bundle"
}
func (s *skillStore) put(item, hash string, bundle []byte, node, location string) (skillVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var v skillVersion
	err := s.db.db.QueryRow(`SELECT item,hash,commit_id,node_id,location,imported_at FROM skill_versions WHERE item=? AND hash=?`, item, hash).Scan(&v.Item, &v.Hash, &v.Commit, &v.NodeID, &v.Location, &v.ImportedAt)
	if err == nil {
		return v, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return v, err
	}
	blob, err := s.git(bundle, "hash-object", "-w", "--stdin")
	if err != nil {
		return v, err
	}
	// A temporary index stages only paths chosen by the gateway.
	idx, err := os.CreateTemp(s.path, "index-")
	if err != nil {
		return v, err
	}
	name := idx.Name()
	idx.Close()
	os.Remove(name)
	defer os.Remove(name)
	gitIndex := func(input []byte, args ...string) (string, error) {
		args = append([]string{"-c", "core.hooksPath=/dev/null"}, args...)
		cmd := exec.Command("git", append([]string{"--git-dir", s.path}, args...)...)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + s.path, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_INDEX_FILE=" + name, "GIT_AUTHOR_NAME=agent-net", "GIT_AUTHOR_EMAIL=agent-net@localhost", "GIT_COMMITTER_NAME=agent-net", "GIT_COMMITTER_EMAIL=agent-net@localhost"}
		cmd.Stdin = bytes.NewReader(input)
		out, e := cmd.Output()
		if e != nil {
			return "", fmt.Errorf("skill store git failed: %w", e)
		}
		return strings.TrimSpace(string(out)), nil
	}
	head, headErr := s.git(nil, "rev-parse", "--verify", "HEAD")
	if headErr == nil {
		if _, err = gitIndex(nil, "read-tree", head); err != nil {
			return v, err
		}
	}
	if _, err = gitIndex(nil, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+skillPath(item)); err != nil {
		return v, err
	}
	tree, err := gitIndex(nil, "write-tree")
	if err != nil {
		return v, err
	}
	args := []string{"commit-tree", tree}
	if headErr == nil {
		args = append(args, "-p", head)
	}
	commit, err := s.git([]byte("Import skill\n"), args...)
	if err != nil {
		return v, err
	}
	if _, err = s.git(nil, "update-ref", "HEAD", commit); err != nil {
		return v, err
	}
	v = skillVersion{Item: item, Hash: hash, Commit: commit, NodeID: node, Location: location, ImportedAt: s.db.now().UnixNano()}
	_, err = s.db.db.Exec(`INSERT INTO skill_versions(item,hash,commit_id,node_id,location,imported_at) VALUES(?,?,?,?,?,?)`, v.Item, v.Hash, v.Commit, v.NodeID, v.Location, v.ImportedAt)
	return v, err
}
