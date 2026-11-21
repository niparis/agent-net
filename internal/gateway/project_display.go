package gateway

import (
	"database/sql"
	"strings"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
)

// ProjectDisplayName reads the optional name while keeping the project identity.
func (s *Store) ProjectDisplayName(project string) (protocol.ProjectDisplayResult, error) {
	out := protocol.ProjectDisplayResult{Project: project}
	err := s.db.QueryRow(`SELECT COALESCE(NULLIF(display_name,id),'') FROM repos WHERE id=?`, project).Scan(&out.Name)
	if err == sql.ErrNoRows {
		err = nil
	}
	return out, err
}

// SetProjectDisplayName saves an owner-chosen label; an empty label clears it.
func (s *Store) SetProjectDisplayName(project, name string, validate ...bool) (protocol.ProjectDisplayResult, error) {
	out := protocol.ProjectDisplayResult{Project: project}
	if !validGraphName(project) {
		return out, reject("invalid project identity")
	}
	name = strings.TrimSpace(name)
	if len(name) > protocol.ProjectMaxFieldLen {
		return out, reject("display name too long")
	}
	for _, r := range name {
		if !unicode.IsPrint(r) {
			return out, reject("invalid display name")
		}
	}
	var exists bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM projects WHERE name=? UNION ALL SELECT 1 FROM sessions WHERE repo_project=?)`, project, project).Scan(&exists); err != nil {
		return out, err
	}
	if !exists {
		return out, reject("unknown project %q", project)
	}
	if len(validate) > 0 && validate[0] {
		return out, nil
	}
	stored := name
	if stored == "" {
		stored = project
	}
	now := s.now().UnixNano()
	_, err := s.db.Exec(`INSERT INTO repos(id,display_name,created_at,updated_at) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET display_name=excluded.display_name,updated_at=excluded.updated_at`, project, stored, now, now)
	out.Name = name
	return out, err
}
