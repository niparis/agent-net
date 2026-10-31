package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

func normalizeAssignment(a protocol.ToolAssignment) (protocol.ToolAssignment, error) {
	switch a.Scope {
	case "global", "uninstalled":
		if len(a.Projects) != 0 {
			return a, errors.New("scope takes no projects")
		}
		a.Projects = nil
	case "projects":
		if len(a.Projects) == 0 || len(a.Projects) > 200 {
			return a, errors.New("projects scope needs projects")
		}
		sort.Strings(a.Projects)
		for i, p := range a.Projects {
			if p == "" || len(p) > protocol.ProjectMaxFieldLen || strings.TrimSpace(p) != p || (i > 0 && a.Projects[i-1] == p) {
				return a, errors.New("invalid project list")
			}
		}
	default:
		return a, errors.New("scope must be global, projects or uninstalled")
	}
	return a, nil
}
func assignToolTx(tx *sql.Tx, item string, a protocol.ToolAssignment, actor, source string, now int64) (protocol.ToolRevision, error) {
	var r protocol.ToolRevision
	var err error
	a, err = normalizeAssignment(a)
	if err != nil {
		return r, err
	}
	if actor != "owner" && actor != "cto" {
		return r, errors.New("owner authority required")
	}
	if source == "" || len(source) > 128 {
		return r, errors.New("invalid source")
	}
	var hash string
	if err = tx.QueryRow(`SELECT hash FROM tool_identities WHERE id=?`, item).Scan(&hash); err != nil {
		return r, errors.New("tool item not found")
	}
	for _, p := range a.Projects {
		var exists int
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM projects WHERE name=?)`, p).Scan(&exists); err != nil {
			return r, err
		}
		if exists == 0 {
			return r, errors.New("project is not registered")
		}
	}
	var revision int64
	if err = tx.QueryRow(`SELECT COALESCE(MAX(revision),0)+1 FROM tool_revisions WHERE item=?`, item).Scan(&revision); err != nil {
		return r, err
	}
	b, err := json.Marshal(a)
	if err != nil {
		return r, err
	}
	if _, err = tx.Exec(`INSERT INTO tool_revisions(item,revision,assignment,version,actor,source,at) VALUES(?,?,?,?,?,?,?)`, item, revision, string(b), hash, actor, source, now); err != nil {
		return r, err
	}
	return protocol.ToolRevision{Item: item, Revision: revision, Assignment: a, Version: hash, Actor: actor, Source: source, At: now, State: "pending"}, nil
}

// AssignTool records a desired revision. Callers must enforce owner authority.
func (s *Store) AssignTool(item string, a protocol.ToolAssignment, actor, source string) (protocol.ToolRevision, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.ToolRevision{}, err
	}
	defer tx.Rollback()
	r, err := assignToolTx(tx, item, a, actor, source, s.now().UnixNano())
	if err != nil {
		return r, err
	}
	err = tx.Commit()
	return r, err
}
