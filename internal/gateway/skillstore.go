package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
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
		if _, err = s.git(nil, "init", "--bare", "--quiet", "--template="); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return s, nil
}
func (s *skillStore) git(input []byte, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"--git-dir", s.path, "-c", "core.hooksPath=/dev/null"}, args...)...)
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
		cmd := exec.Command("git", append([]string{"--git-dir", s.path, "-c", "core.hooksPath=/dev/null"}, args...)...)
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

func (g *Gateway) skillRepository() (*skillStore, error) {
	g.skillsMu.Lock()
	defer g.skillsMu.Unlock()
	if g.skills != nil {
		return g.skills, nil
	}
	s, err := openSkillStore(g.opts.DataDir, g.store)
	if err == nil {
		g.skills = s
	}
	return s, err
}

func (g *Gateway) importSkill(ctx context.Context, p protocol.ToolImport) (skillVersion, error) {
	var v skillVersion
	if p.Item == "" || p.Location.NodeID == "" || p.Location.Location == "" {
		return v, reject("import needs an item and a location")
	}
	var name, hash string
	err := g.store.db.QueryRow(`SELECT i.name,i.hash FROM tool_identities i JOIN tool_locations l ON l.identity_id=i.id WHERE i.id=? AND i.kind='skill' AND l.node_id=? AND json_extract(l.observation,'$.location')=? AND json_extract(l.observation,'$.state')='ok' LIMIT 1`, p.Item, p.Location.NodeID, p.Location.Location).Scan(&name, &hash)
	if err != nil || hash == "" {
		return v, reject("skill location is not a current observation")
	}
	host := g.conn(p.Location.NodeID)
	if host == nil {
		return v, reject("skill location is offline")
	}
	raw, err := host.request(ctx, "node.skill_bundle", protocol.SkillBundleRequest{Name: name, Hash: hash, Location: p.Location.Location}, 60*time.Second)
	if err != nil {
		return v, reject("cannot obtain skill bundle")
	}
	var result protocol.SkillBundle
	if json.Unmarshal(raw, &result) != nil {
		return v, reject("invalid skill bundle")
	}
	dir, err := os.MkdirTemp("", "agent-net-skill-import-")
	if err != nil {
		return v, errors.New("cannot stage skill import")
	}
	defer os.RemoveAll(dir)
	if inventory.UnpackSkill(result.Data, dir) != nil {
		return v, reject("invalid or oversized skill bundle")
	}
	got, err := inventory.SkillHash(dir)
	if err != nil || got != hash {
		return v, reject("skill changed since its inventory scan")
	}
	canonical, err := inventory.PackSkill(dir)
	if err != nil {
		return v, reject("cannot verify skill bundle")
	}
	repo, err := g.skillRepository()
	if err != nil {
		return v, errors.New("cannot open skill store")
	}
	v, err = repo.put(p.Item, hash, canonical, p.Location.NodeID, p.Location.Location)
	if err != nil {
		return v, errors.New("cannot save skill version")
	}
	return v, nil
}

func (g *Gateway) skillVersions(item string) (any, error) {
	rows, err := g.store.db.Query(`SELECT item,hash,commit_id,node_id,location,imported_at FROM skill_versions WHERE item=? ORDER BY imported_at,hash`, item)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := []skillVersion{}
	for rows.Next() {
		var v skillVersion
		if err = rows.Scan(&v.Item, &v.Hash, &v.Commit, &v.NodeID, &v.Location, &v.ImportedAt); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{"versions": versions}, nil
}

func (s *skillStore) read(item, hash string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var commit string
	if err := s.db.db.QueryRow(`SELECT commit_id FROM skill_versions WHERE item=? AND hash=?`, item, hash).Scan(&commit); err != nil {
		return nil, errors.New("skill version not found")
	}
	if len(commit) != 40 {
		return nil, errors.New("invalid skill commit")
	}
	if _, err := hex.DecodeString(commit); err != nil {
		return nil, errors.New("invalid skill commit")
	}
	cmd := exec.Command("git", "--git-dir", s.path, "-c", "core.hooksPath=/dev/null", "cat-file", "blob", commit+":"+skillPath(item))
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + s.path, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0"}
	out, err := cmd.Output()
	if err != nil || len(out) > inventory.MaxBundleBytes {
		return nil, errors.New("cannot read skill version")
	}
	dir, err := os.MkdirTemp("", "agent-net-skill-check-")
	if err != nil {
		return nil, errors.New("cannot verify skill version")
	}
	defer os.RemoveAll(dir)
	if inventory.UnpackSkill(out, dir) != nil {
		return nil, errors.New("invalid stored skill bundle")
	}
	got, err := inventory.SkillHash(dir)
	if err != nil || got != hash {
		return nil, errors.New("stored skill hash mismatch")
	}
	return out, nil
}
func (g *Gateway) exportSkill(item, hash string) (protocol.SkillBundle, error) {
	repo, err := g.skillRepository()
	if err != nil {
		return protocol.SkillBundle{}, errors.New("cannot open skill store")
	}
	b, err := repo.read(item, hash)
	if err != nil {
		return protocol.SkillBundle{}, err
	}
	return protocol.SkillBundle{Data: b}, nil
}
