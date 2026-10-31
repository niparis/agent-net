package gateway

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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

// toolSnapshot binds plans to registry, observations, versions and revisions.
func toolSnapshot(tx *sql.Tx) (string, error) {
	h := sha256.New()
	for _, query := range []string{
		`SELECT id,hostname,os,version,seen_at,harnesses FROM nodes ORDER BY id`,
		`SELECT node_id,path,name,remote,default_branch FROM projects ORDER BY node_id,path`,
		`SELECT node_id,report FROM inventory_reports ORDER BY node_id`,
		`SELECT id,kind,name,hash FROM tool_identities ORDER BY id`,
		`SELECT item,hash,commit_id FROM skill_versions ORDER BY item,hash`,
		`SELECT item,revision,assignment,version FROM tool_revisions ORDER BY item,revision`,
		`SELECT node_id,harness,repo_root,repo_worktree,repo_project FROM sessions WHERE repo_root<>'' AND repo_worktree<>'' ORDER BY node_id,harness,repo_root,repo_worktree,repo_project`,
	} {
		rows, err := tx.Query(query)
		if err != nil {
			return "", err
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return "", err
		}
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err = rows.Scan(ptrs...); err != nil {
				rows.Close()
				return "", err
			}
			b, err := json.Marshal(vals)
			if err != nil {
				rows.Close()
				return "", err
			}
			h.Write(b)
			h.Write([]byte{0})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (s *Store) ToolSnapshot() (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	v, err := toolSnapshot(tx)
	if err != nil {
		return "", err
	}
	return v, tx.Commit()
}
func (s *Store) SaveToolPlan(item string, a protocol.ToolAssignment, targets []protocol.ToolPlanTarget, actor string) (protocol.ToolPlan, error) {
	return s.saveToolPlan(item, a, targets, actor, "")
}
func (s *Store) saveToolPlan(item string, a protocol.ToolAssignment, targets []protocol.ToolPlanTarget, actor, expected string) (protocol.ToolPlan, error) {
	var p protocol.ToolPlan
	if actor != "owner" && actor != "cto" {
		return p, errors.New("owner authority required")
	}
	a, err := normalizeAssignment(a)
	if err != nil {
		return p, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	var version string
	if err = tx.QueryRow(`SELECT hash FROM tool_identities WHERE id=?`, item).Scan(&version); err != nil {
		return p, errors.New("tool item not found")
	}
	for _, name := range a.Projects {
		var exists int
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM projects WHERE name=?)`, name).Scan(&exists); err != nil {
			return p, err
		}
		if exists == 0 {
			return p, errors.New("project is not registered")
		}
	}
	snapshot, err := toolSnapshot(tx)
	if err != nil {
		return p, err
	}
	if expected != "" && snapshot != expected {
		return p, errors.New("plan out of date")
	}
	if targets == nil {
		targets = []protocol.ToolPlanTarget{}
	}
	p = protocol.ToolPlan{ID: protocol.NewID("plan"), Item: item, Version: version, Assignment: a, Targets: targets, Conflicts: []string{}, State: "pending", Actor: actor, At: s.now().UnixNano()}
	for _, target := range targets {
		if target.Conflict != "" {
			p.Conflicts = append(p.Conflicts, target.Conflict)
		}
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return p, err
	}
	if len(raw) > 1<<20 {
		return p, errors.New("plan exceeds limit")
	}
	if _, err = tx.Exec(`INSERT INTO tool_plans(id,item,snapshot,plan) VALUES(?,?,?,?)`, p.ID, item, snapshot, string(raw)); err != nil {
		return p, err
	}
	if err = auditToolTx(tx, p.Item, p.ID, "plan", actor, 0, p.Targets, "proposed", p.At); err != nil {
		return p, err
	}
	return p, tx.Commit()
}
func (s *Store) applyToolPlan(id, actor string) (protocol.ToolRevision, error) {
	var r protocol.ToolRevision
	if actor != "owner" && actor != "cto" {
		return r, errors.New("owner authority required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return r, err
	}
	defer tx.Rollback()
	var snapshot, raw string
	var applied int
	if err = tx.QueryRow(`SELECT snapshot,plan,applied FROM tool_plans WHERE id=?`, id).Scan(&snapshot, &raw, &applied); err != nil {
		return r, errors.New("plan not found")
	}
	if applied != 0 {
		return r, errors.New("plan already applied")
	}
	current, err := toolSnapshot(tx)
	if err != nil {
		return r, err
	}
	if snapshot != current {
		return r, errors.New("plan out of date")
	}
	var p protocol.ToolPlan
	if err = json.Unmarshal([]byte(raw), &p); err != nil {
		return r, err
	}
	r, err = assignToolTx(tx, p.Item, p.Assignment, actor, "plan:"+id, s.now().UnixNano())
	if err != nil {
		return r, err
	}
	if _, err = tx.Exec(`UPDATE tool_plans SET applied=1 WHERE id=?`, id); err != nil {
		return r, err
	}
	if err = auditToolTx(tx, p.Item, id, "apply", actor, r.Revision, p.Targets, "pending", r.At); err != nil {
		return r, err
	}
	return r, tx.Commit()
}

func auditToolTx(tx *sql.Tx, item, plan, action, actor string, revision int64, targets []protocol.ToolPlanTarget, result string, at int64) error {
	b, err := json.Marshal(targets)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO tool_audit(item,plan_id,action,actor,revision,targets,result,at) VALUES(?,?,?,?,?,?,?,?)`, item, plan, action, actor, revision, string(b), result, at)
	return err
}
func (s *Store) ToolHistory(item string) ([]protocol.ToolAuditEvent, error) {
	rows, err := s.db.Query(`SELECT id,item,plan_id,action,actor,revision,targets,result,at FROM tool_audit WHERE item=? ORDER BY id`, item)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.ToolAuditEvent{}
	size := 0
	for rows.Next() {
		var e protocol.ToolAuditEvent
		var raw string
		if err = rows.Scan(&e.ID, &e.Item, &e.PlanID, &e.Action, &e.Actor, &e.Revision, &raw, &e.Result, &e.At); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &e.Targets); err != nil {
			return nil, err
		}
		size += len(raw) + len(e.Item) + len(e.PlanID) + 128
		if size > 3<<20 {
			return nil, errors.New("history exceeds response limit")
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) recordRefusedToolApply(id, actor string) error {
	if actor != "owner" && actor != "cto" {
		return errors.New("owner authority required")
	}
	_, err := s.db.Exec(`INSERT INTO tool_audit(item,plan_id,action,actor,revision,targets,result,at) SELECT item,id,'apply',?,0,json_extract(plan,'$.targets'),'refused',? FROM tool_plans WHERE id=?`, actor, s.now().UnixNano(), id)
	return err
}
func (s *Store) ApplyToolPlan(id, actor string) (protocol.ToolRevision, error) {
	r, err := s.applyToolPlan(id, actor)
	if err != nil && (actor == "owner" || actor == "cto") {
		if auditErr := s.recordRefusedToolApply(id, actor); auditErr != nil {
			return r, auditErr
		}
	}
	return r, err
}
