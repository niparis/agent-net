package gateway

import (
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/repoinfo"
)

// The project registry. A row says that a folder on one machine is a project
// that workers may start sessions in, even if no session ever ran there. The
// machine's own daemon checks the folder before it asks for the row, so the
// gateway only checks the shape of what it is given.

// validProjectEntry cleans and checks one entry from a daemon.
func validProjectEntry(e protocol.ProjectEntry) (protocol.ProjectEntry, error) {
	for _, v := range []string{e.Name, e.Path, e.Remote, e.DefaultBranch} {
		if len(v) > protocol.ProjectMaxFieldLen {
			return e, reject("a project field is longer than %d bytes", protocol.ProjectMaxFieldLen)
		}
		for _, r := range v {
			if !unicode.IsPrint(r) {
				return e, reject("a project field has a control character")
			}
		}
	}
	if !filepath.IsAbs(e.Path) || filepath.Clean(e.Path) != e.Path {
		return e, reject("project path %q must be an absolute, clean path", e.Path)
	}
	if e.Name == "" {
		e.Name = filepath.Base(e.Path)
	}
	e.Remote = repoinfo.StripCredentials(e.Remote)
	return e, nil
}

// AddProjects registers entries on a node. An entry whose path is already
// registered on the node is not added again: its name, remote and default
// branch are refreshed, and the time it was last checked. With DryRun nothing
// is changed. The answer says which entries are new and which existed.
func (s *Store) AddProjects(nodeID string, in protocol.ProjectAdd) (protocol.ProjectAddResult, error) {
	res := protocol.ProjectAddResult{Added: []protocol.ProjectEntry{}, Existing: []protocol.ProjectEntry{}}
	if len(in.Projects) == 0 {
		return res, reject("no projects given")
	}
	if len(in.Projects) > protocol.ProjectMaxPerCall {
		return res, reject("at most %d projects per call", protocol.ProjectMaxPerCall)
	}
	var entries []protocol.ProjectEntry
	seen := map[string]bool{}
	for _, e := range in.Projects {
		e, err := validProjectEntry(e)
		if err != nil {
			return res, err
		}
		if !seen[e.Path] {
			seen[e.Path] = true
			entries = append(entries, e)
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	now := s.now().UnixNano()
	for _, e := range entries {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM projects WHERE node_id = ? AND path = ?`, nodeID, e.Path).Scan(&n); err != nil {
			return res, err
		}
		if n > 0 {
			res.Existing = append(res.Existing, e)
			if !in.DryRun {
				if _, err := tx.Exec(`UPDATE projects SET name = ?, remote = ?, default_branch = ?, checked_at = ?, graph_excluded = ?
					WHERE node_id = ? AND path = ?`, e.Name, e.Remote, e.DefaultBranch, now, graphExclusion(e.GraphExcluded, e.GraphChecked), nodeID, e.Path); err != nil {
					return res, err
				}
			}
			continue
		}
		res.Added = append(res.Added, e)
		if !in.DryRun {
			if _, err := tx.Exec(`INSERT INTO projects (node_id, path, name, remote, default_branch, added_by_session,
				added_by_harness, added_at, checked_at, graph_excluded) VALUES (?,?,?,?,?,?,?,?,?,?)`, nodeID, e.Path, e.Name, e.Remote,
				e.DefaultBranch, in.AddedBySession, in.AddedByHarness, now, now, graphExclusion(e.GraphExcluded, e.GraphChecked)); err != nil {
				return res, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return res, err
	}
	if !in.DryRun {
		host := s.hostname(nodeID)
		for _, e := range res.Added {
			s.Event("project.added", nodeID, in.AddedBySession, "", strings.TrimPrefix(pairs("name", e.Name, "path", e.Path), " "))
			s.logf("project.added", "node", nodeID, "host", host, "session", in.AddedBySession,
				"harness", in.AddedByHarness, "name", e.Name, "path", e.Path, "remote", e.Remote)
		}
	}
	return res, nil
}

// RemoveProject deletes the registration of one project on a node, picked by
// path or, if no path is given, by name (case-insensitive). A name that fits
// several projects on the node is refused. A project that only has sessions
// (never registered) cannot be removed.
func (s *Store) RemoveProject(nodeID, name, path, byNode string) (protocol.ProjectEntry, error) {
	var out protocol.ProjectEntry
	var q string
	var arg string
	switch {
	case path != "":
		q, arg = `SELECT path, name, remote, default_branch FROM projects WHERE node_id = ? AND path = ?`, path
	case name != "":
		q, arg = `SELECT path, name, remote, default_branch FROM projects WHERE node_id = ? AND lower(name) = lower(?) ORDER BY path`, name
	default:
		return out, reject("give a project name or path")
	}
	rows, err := s.db.Query(q, nodeID, arg)
	if err != nil {
		return out, err
	}
	var found []protocol.ProjectEntry
	for rows.Next() {
		var e protocol.ProjectEntry
		if err := rows.Scan(&e.Path, &e.Name, &e.Remote, &e.DefaultBranch); err != nil {
			rows.Close()
			return out, err
		}
		found = append(found, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	host := s.hostname(nodeID)
	switch len(found) {
	case 0:
		return out, reject("no registered project %q on %s", arg, host)
	case 1:
	default:
		var paths []string
		for _, e := range found {
			paths = append(paths, e.Path)
		}
		return out, reject("%d registered projects on %s are named %q; give the path: %s", len(found), host, arg, strings.Join(paths, ", "))
	}
	out = found[0]
	if _, err := s.db.Exec(`DELETE FROM projects WHERE node_id = ? AND path = ?`, nodeID, out.Path); err != nil {
		return out, err
	}
	s.Event("project.removed", byNode, "", "", strings.TrimPrefix(pairs("name", out.Name, "path", out.Path, "node", nodeID), " "))
	s.logf("project.removed", "node", byNode, "target", nodeID, "host", host, "name", out.Name, "path", out.Path)
	return out, nil
}

// registeredProjects lists the registry as project rows, keeping only
// projects with the given name (case-insensitive) when name is set.
func (s *Store) registeredProjects(name string) ([]protocol.ProjectView, error) {
	rows, err := s.db.Query(`SELECT p.node_id, COALESCE(n.hostname, ''), p.path, p.name, p.remote, p.default_branch,
		p.added_by_session, p.added_by_harness, p.added_at, p.checked_at
		FROM projects p LEFT JOIN nodes n ON n.id = p.node_id ORDER BY p.path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.ProjectView
	for rows.Next() {
		var v protocol.ProjectView
		var by, harness string
		var added, checked int64
		if err := rows.Scan(&v.NodeID, &v.Hostname, &v.Path, &v.Name, &v.Remote, &v.DefaultBranch, &by, &harness, &added, &checked); err != nil {
			return nil, err
		}
		if name != "" && !strings.EqualFold(v.Name, name) {
			continue
		}
		v.Source = protocol.SourceRegistered
		v.AddedBy = strings.TrimSpace(harness + " " + by)
		v.AddedAt = added / int64(time.Second)
		v.CheckedAt = checked / int64(time.Second)
		v.Worktrees, v.Harnesses = []string{}, []string{}
		out = append(out, v)
	}
	return out, rows.Err()
}
