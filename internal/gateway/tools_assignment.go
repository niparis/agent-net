package gateway

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/temppath"
)

// projectKnownTx reports whether the gateway knows a project name: registered,
// or seen in a stored session whose folder is a project (decision 0048). A
// Git working tree is a seen project when it is not temporary and the session did not
// opt out of the network.
func (s *Store) projectKnownTx(tx *sql.Tx, name string) (bool, error) {
	var exists int
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM projects WHERE name=?)`, name).Scan(&exists); err != nil {
		return false, err
	}
	if exists != 0 {
		return true, nil
	}
	rows, err := tx.Query(`SELECT cwd, repo_root, repo_project, no_project FROM sessions WHERE harness != 'owner' AND repo_root<>'' AND (repo_project=? OR repo_project='')`, name)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	roots := s.tempRoots()
	for rows.Next() {
		var cwd, root, project string
		var noProject int
		if err = rows.Scan(&cwd, &root, &project, &noProject); err != nil {
			return false, err
		}
		path := root
		if path == "" || noProject != 0 || temppath.Under(path, roots) {
			continue
		}
		if project == "" {
			project = filepath.Base(path)
		}
		if project == name {
			return true, nil
		}
	}
	return false, rows.Err()
}

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
	if len(a.Machines) == 0 {
		a.Machines = nil
		return a, nil
	}
	if len(a.Machines) > 200 {
		return a, errors.New("too many machines")
	}
	a.Machines = append([]string(nil), a.Machines...)
	sort.Strings(a.Machines)
	for i, m := range a.Machines {
		if m == "" || len(m) > protocol.ProjectMaxFieldLen || strings.TrimSpace(m) != m || (i > 0 && a.Machines[i-1] == m) {
			return a, errors.New("invalid machine list")
		}
	}
	return a, nil
}
func (s *Store) assignToolTx(tx *sql.Tx, item string, a protocol.ToolAssignment, actor, source string, now int64) (protocol.ToolRevision, error) {
	var r protocol.ToolRevision
	var err error
	a, err = normalizeAssignment(a)
	if err != nil {
		return r, err
	}
	if !validToolChangeActor(actor) {
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
		known, err := s.projectKnownTx(tx, p)
		if err != nil {
			return r, err
		}
		if !known {
			return r, errors.New("project is not known")
		}
	}
	for _, n := range a.Machines {
		var exists int
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM nodes WHERE id=?)`, n).Scan(&exists); err != nil {
			return r, err
		}
		if exists == 0 {
			return r, errors.New("machine is not enrolled")
		}
	}
	names, err := s.knownToolProjectsTx(tx)
	if err != nil {
		return r, err
	}
	if err = initToolProjectSettingTx(tx, item, a.Scope == "global", names); err != nil {
		return r, err
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
	r, err := s.assignToolTx(tx, item, a, actor, source, s.now().UnixNano())
	if err != nil {
		return r, err
	}
	err = tx.Commit()
	return r, err
}

// toolSnapshot binds plans to registry, observations, versions and revisions.
// It leaves out sessions and the new-projects settings: a session starting
// anywhere, or a setting row created on first read, does not change what a
// plan writes, and must not make every pending plan out of date.
func (s *Store) toolSnapshot(tx *sql.Tx, item string) (string, error) {
	h := sha256.New()
	var kind, name string
	if item != "" {
		if err := tx.QueryRow("SELECT kind,name FROM tool_identities WHERE id=?", item).Scan(&kind, &name); err != nil {
			return "", errors.New("plan out of date")
		}
	}
	for _, query := range []string{
		`SELECT id,hostname,os FROM nodes ORDER BY id`,
		`SELECT node_id,path,name,remote,default_branch FROM projects ORDER BY node_id,path`,
		`SELECT node_id,report FROM inventory_reports ORDER BY node_id`,
		`SELECT id,kind,name,hash FROM tool_identities ORDER BY id`,
		`SELECT item,hash,commit_id FROM skill_versions ORDER BY item,hash`,
		`SELECT item,revision,assignment,version FROM tool_revisions ORDER BY item,revision`,
	} {
		var args []any
		if item != "" {
			for _, table := range []string{"tool_identities", "skill_versions", "tool_revisions"} {
				if strings.Contains(query, "FROM "+table) {
					column := "item"
					if table == "tool_identities" {
						column = "id"
					}
					query = strings.Replace(query, " ORDER BY", " WHERE "+column+"=? ORDER BY", 1)
					args = []any{item}
				}
			}
		}
		rows, err := tx.Query(query, args...)
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
			if strings.Contains(query, "FROM inventory_reports") {
				var raw string
				switch v := vals[1].(type) {
				case string:
					raw = v
				case []byte:
					raw = string(v)
				}
				var report protocol.InventoryReport
				if err = json.Unmarshal([]byte(raw), &report); err != nil {
					rows.Close()
					return "", err
				}
				if item != "" {
					filtered := make([]protocol.InventoryItem, 0, len(report.Items))
					for _, observed := range report.Items {
						if string(observed.Kind) == kind && observed.Name == name {
							filtered = append(filtered, observed)
						}
					}
					report.Items = filtered
				}
				// A report with no items is stored as null or as an empty
				// list, depending on the scan. Both mean the same.
				if len(report.Items) == 0 {
					report.Items = nil
				}
				report.ScannedAt = time.Time{}
				for i := range report.Items {
					report.Items[i].ScannedAt = time.Time{}
				}
				normalized, e := json.Marshal(report)
				if e != nil {
					rows.Close()
					return "", e
				}
				vals[1] = string(normalized)
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
func (s *Store) ToolSnapshot(item string) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	v, err := s.toolSnapshot(tx, item)
	if err != nil {
		return "", err
	}
	return v, tx.Commit()
}
func (s *Store) SaveToolPlan(item string, a protocol.ToolAssignment, targets []protocol.ToolPlanTarget, actor string) (protocol.ToolPlan, error) {
	return s.saveToolPlan(item, a, targets, actor, "", "", nil)
}
func (s *Store) saveToolPlan(item string, a protocol.ToolAssignment, targets []protocol.ToolPlanTarget, actor, expected, operation string, repoTargets []protocol.ToolRepoTarget, validate ...bool) (protocol.ToolPlan, error) {
	var p protocol.ToolPlan
	if !validToolChangeActor(actor) {
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
		known, err := s.projectKnownTx(tx, name)
		if err != nil {
			return p, err
		}
		if !known {
			return p, errors.New("project is not known")
		}
	}
	for _, n := range a.Machines {
		var exists int
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM nodes WHERE id=?)`, n).Scan(&exists); err != nil {
			return p, err
		}
		if exists == 0 {
			return p, errors.New("machine is not enrolled")
		}
	}
	var baseRevision int64
	if err = tx.QueryRow(`SELECT COALESCE(MAX(revision),0) FROM tool_revisions WHERE item=?`, item).Scan(&baseRevision); err != nil {
		return p, err
	}
	snapshot, err := s.toolSnapshot(tx, item)
	if err != nil {
		return p, err
	}
	if expected != "" && snapshot != expected {
		return p, errors.New("plan out of date")
	}
	if targets == nil {
		targets = []protocol.ToolPlanTarget{}
	}
	p = protocol.ToolPlan{ID: protocol.NewID("plan"), Item: item, Version: version, BaseRevision: baseRevision, Assignment: a, Targets: targets, Operation: operation, RepoTargets: repoTargets, Conflicts: []string{}, State: "pending", Actor: actor, At: s.now().UnixNano()}
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
	if err = auditToolTx(tx, p.Item, p.ID, "plan", actor, p.BaseRevision, p.Targets, "proposed", p.At, &p.Assignment); err != nil {
		return p, err
	}
	if len(validate) > 0 && validate[0] {
		return p, nil
	}
	return p, tx.Commit()
}
func (s *Store) applyToolPlan(id, actor string, validate ...bool) (protocol.ToolRevision, error) {
	var r protocol.ToolRevision
	if !validToolChangeActor(actor) {
		return r, errors.New("owner authority required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return r, err
	}
	defer tx.Rollback()
	r, err = s.applyToolPlanTx(tx, id, actor)
	if err != nil {
		return r, err
	}
	if len(validate) > 0 && validate[0] {
		return r, nil
	}
	return r, tx.Commit()
}

func (s *Store) applyToolPlanTx(tx *sql.Tx, id, actor string) (protocol.ToolRevision, error) {
	var r protocol.ToolRevision
	var err error
	var snapshot, raw string
	var applied int
	if err = tx.QueryRow(`SELECT snapshot,plan,applied FROM tool_plans WHERE id=?`, id).Scan(&snapshot, &raw, &applied); err != nil {
		return r, errors.New("plan not found")
	}
	if applied != 0 {
		return r, errors.New("plan already applied")
	}
	var p protocol.ToolPlan
	if err = json.Unmarshal([]byte(raw), &p); err != nil {
		return r, err
	}
	current, err := s.toolSnapshot(tx, p.Item)
	if err != nil {
		return r, err
	}
	if snapshot != current {
		return r, errors.New("plan out of date")
	}
	for _, target := range p.Targets {
		if target.Action != "remove" {
			continue
		}
		var kind string
		if err = tx.QueryRow(`SELECT kind FROM tool_identities WHERE id=?`, p.Item).Scan(&kind); err != nil {
			return r, err
		}
		if kind == "skill" {
			var imported int
			if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM skill_versions WHERE item=? AND hash=?)`, p.Item, p.Version).Scan(&imported); err != nil {
				return r, err
			}
			if imported == 0 {
				rows, e := tx.Query(`SELECT node_id,observation FROM tool_locations WHERE identity_id=?`, p.Item)
				if e != nil {
					return r, e
				}
				remaining := false
				for rows.Next() {
					var node, observation string
					if e = rows.Scan(&node, &observation); e != nil {
						break
					}
					var obs protocol.InventoryItem
					if e = json.Unmarshal([]byte(observation), &obs); e != nil {
						break
					}
					assigned := p.Assignment.Scope == "global" && obs.Scope == protocol.InventoryUser
					for _, project := range p.Assignment.Projects {
						if p.Assignment.Scope == "projects" && obs.Scope == protocol.InventoryProject && obs.Project == project {
							assigned = true
						}
					}
					// A machine the assignment does not name keeps its copy.
					if !p.Assignment.OnMachine(node) {
						assigned = true
					}
					for _, removal := range p.Targets {
						if removal.Action == "remove" && removal.NodeID == node && removal.Source == obs.Location {
							assigned = false
						}
					}
					remaining = remaining || assigned
				}
				if e == nil {
					e = rows.Err()
				}
				rows.Close()
				if e != nil {
					return r, e
				}
				if !remaining {
					return r, errors.New("this is the last copy and it is not imported: import it first")
				}
			}
		}
		break
	}
	r, err = s.assignToolTx(tx, p.Item, p.Assignment, actor, "plan:"+id, s.now().UnixNano())
	if err != nil {
		return r, err
	}
	if _, err = tx.Exec(`UPDATE tool_plans SET applied=1 WHERE id=?`, id); err != nil {
		return r, err
	}
	if err = auditToolTx(tx, p.Item, id, "apply", actor, r.Revision, p.Targets, "pending", r.At, &p.Assignment); err != nil {
		return r, err
	}
	return r, nil
}

// auditToolTx writes one audit event. a is the assignment the event is about,
// or nil when it has none (an MCP import, for example).
func auditToolTx(tx *sql.Tx, item, plan, action, actor string, revision int64, targets []protocol.ToolPlanTarget, result string, at int64, a *protocol.ToolAssignment) error {
	b, err := json.Marshal(targets)
	if err != nil {
		return err
	}
	assignment := ""
	if a != nil {
		raw, e := json.Marshal(a)
		if e != nil {
			return e
		}
		assignment = string(raw)
	}
	_, err = tx.Exec(`INSERT INTO tool_audit(item,plan_id,action,actor,revision,targets,result,at,assignment) VALUES(?,?,?,?,?,?,?,?,?)`, item, plan, action, actor, revision, string(b), result, at, assignment)
	return err
}
func (s *Store) ToolHistory(item string) ([]protocol.ToolAuditEvent, error) {
	rows, err := s.db.Query(`SELECT id,item,plan_id,action,actor,revision,targets,result,at,assignment FROM tool_audit WHERE item=? ORDER BY id`, item)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.ToolAuditEvent{}
	size := 0
	for rows.Next() {
		var e protocol.ToolAuditEvent
		var raw, assignment string
		if err = rows.Scan(&e.ID, &e.Item, &e.PlanID, &e.Action, &e.Actor, &e.Revision, &raw, &e.Result, &e.At, &assignment); err != nil {
			return nil, err
		}
		if assignment != "" {
			var a protocol.ToolAssignment
			if json.Unmarshal([]byte(assignment), &a) == nil {
				e.Assignment = &a
			}
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
	if !validToolChangeActor(actor) {
		return errors.New("owner authority required")
	}
	_, err := s.db.Exec(`INSERT INTO tool_audit(item,plan_id,action,actor,revision,targets,result,at,assignment) SELECT item,id,'apply',?,COALESCE(json_extract(plan,'$.base_revision'),0),json_extract(plan,'$.targets'),'refused',?,COALESCE(json_extract(plan,'$.assignment'),'') FROM tool_plans WHERE id=?`, actor, s.now().UnixNano(), id)
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

func (s *Store) recordRefusedToolPlan(item, actor string) error {
	_, err := s.db.Exec(`INSERT INTO tool_audit(item,plan_id,action,actor,revision,targets,result,at) SELECT id,'','plan',?,COALESCE((SELECT MAX(revision) FROM tool_revisions WHERE item=tool_identities.id),0),'[]','refused',? FROM tool_identities WHERE id=?`, actor, s.now().UnixNano(), item)
	return err
}

// undoAction maps an applied action to the action that reverses it.
func undoAction(action string) string {
	switch action {
	case "copy":
		return "undo_copy"
	case "remove":
		return "undo_remove"
	case "undo_copy":
		return "copy"
	case "undo_remove":
		return "remove"
	}
	return ""
}

// UndoTool records a new audited revision that reverses the item's newest
// revision. It reuses the assignment revision path: the new revision takes the
// previous assignment and a plan whose targets reverse every target that was
// applied. Targets that were never applied have nothing to reverse. Machines
// that are offline keep their target pending until they reconnect.
func (s *Store) UndoTool(item, actor string, validate ...bool) (protocol.ToolRevision, protocol.ToolPlan, error) {
	var r protocol.ToolRevision
	var p protocol.ToolPlan
	if !validToolChangeActor(actor) {
		return r, p, errors.New("owner authority required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return r, p, err
	}
	defer tx.Rollback()
	var revision int64
	var source, planRaw string
	if err = tx.QueryRow(`SELECT r.revision,r.source,COALESCE(p.plan,'') FROM tool_revisions r LEFT JOIN tool_plans p ON r.source='plan:' || p.id AND p.applied=1 WHERE r.item=? AND r.revision=(SELECT MAX(revision) FROM tool_revisions WHERE item=?)`, item, item).Scan(&revision, &source, &planRaw); err != nil {
		return r, p, errors.New("nothing to undo")
	}
	if planRaw == "" {
		return r, p, errors.New("the newest revision has no plan to undo")
	}
	var last protocol.ToolPlan
	if err = json.Unmarshal([]byte(planRaw), &last); err != nil {
		return r, p, err
	}
	previous := protocol.ToolAssignment{Scope: "uninstalled"}
	if last.Previous != nil {
		previous = *last.Previous
	}
	var rawAssignment string
	if err = tx.QueryRow(`SELECT assignment FROM tool_revisions WHERE item=? AND revision=?`, item, revision-1).Scan(&rawAssignment); err == nil {
		if err = json.Unmarshal([]byte(rawAssignment), &previous); err != nil {
			return r, p, err
		}
	} else if err != sql.ErrNoRows {
		return r, p, err
	}
	rows, err := tx.Query(`SELECT target_index FROM tool_results WHERE item=? AND revision=? AND state='applied'`, item, revision)
	if err != nil {
		return r, p, err
	}
	applied := map[int]bool{}
	for rows.Next() {
		var i int
		if err = rows.Scan(&i); err != nil {
			rows.Close()
			return r, p, err
		}
		applied[i] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return r, p, err
	}
	targets := []protocol.ToolPlanTarget{}
	for i, t := range last.Targets {
		action := undoAction(t.Action)
		if action == "" || !applied[i] {
			continue
		}
		t.Action, t.State, t.Conflict, t.Fingerprint = action, "pending", "", ""
		targets = append(targets, t)
	}
	snapshot, err := s.toolSnapshot(tx, item)
	if err != nil {
		return r, p, err
	}
	now := s.now().UnixNano()
	p = protocol.ToolPlan{ID: protocol.NewID("plan"), Item: item, Version: last.Version, BaseRevision: revision, Assignment: previous, Targets: targets, Conflicts: []string{}, State: "pending", Actor: actor, At: now, UndoOf: revision}
	raw, err := json.Marshal(p)
	if err != nil {
		return r, p, err
	}
	if _, err = tx.Exec(`INSERT INTO tool_plans(id,item,snapshot,plan,applied) VALUES(?,?,?,?,1)`, p.ID, item, snapshot, string(raw)); err != nil {
		return r, p, err
	}
	r, err = s.assignToolTx(tx, item, previous, actor, "plan:"+p.ID, now)
	if err != nil {
		return r, p, err
	}
	if err = auditToolTx(tx, item, p.ID, "undo", actor, r.Revision, targets, "pending", now, &previous); err != nil {
		return r, p, err
	}
	if len(validate) > 0 && validate[0] {
		return r, p, nil
	}
	return r, p, tx.Commit()
}
