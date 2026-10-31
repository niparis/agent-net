package gateway

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/niparis/agent-net/internal/repoinfo"
)

// migrateProjectIdentities uses the remote as evidence, never the folder alone.
// A folder name that points to different repositories is ambiguous and is left
// alone. All changes and the schema version are committed together.
func migrateProjectIdentities(tx *sql.Tx) ([][2]string, error) {
	rows, err := tx.Query(`SELECT repo_root,repo_project,repo_remote FROM sessions WHERE repo_root<>'' AND repo_remote<>'' UNION ALL SELECT path,name,remote FROM projects WHERE remote<>''`)
	if err != nil {
		return nil, err
	}
	candidates := map[string]map[string]bool{}
	for rows.Next() {
		var root, old, remote string
		if err = rows.Scan(&root, &old, &remote); err != nil {
			rows.Close()
			return nil, err
		}
		name := repoinfo.ProjectFromRemote(remote)
		if !validGraphName(name) {
			continue
		}
		for _, v := range []string{filepath.Base(root), old} {
			if !validGraphName(v) {
				continue
			}
			if candidates[v] == nil {
				candidates[v] = map[string]bool{}
			}
			candidates[v][name] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var names []string
	for old, targets := range candidates {
		if len(targets) == 1 && !targets[old] {
			names = append(names, old)
		}
	}
	sort.Strings(names)
	var merges [][2]string
	for _, old := range names {
		var name string
		for name = range candidates[old] {
		}
		// Do not absorb an architect or change an identity that is also the target
		// of another remote. Either would conflate unrelated graph nodes.
		var kind string
		err = tx.QueryRow(`SELECT kind FROM graph_nodes WHERE name=?`, old).Scan(&kind)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if kind == "architect" {
			continue
		}
		if targetSet := candidates[name]; len(targetSet) > 0 && (len(targetSet) != 1 || !targetSet[name]) {
			continue
		}
		merged, err := migrateProjectName(tx, old, name)
		if err != nil {
			return nil, fmt.Errorf("project %q to %q: %w", old, name, err)
		}
		if merged {
			merges = append(merges, [2]string{old, name})
		}
	}
	return merges, nil
}

func migrateProjectName(tx *sql.Tx, old, name string) (bool, error) {
	// Different controls cannot be combined without deciding which rights win.
	// Abort the transaction instead of silently weakening a rule.
	for _, q := range []string{
		`SELECT count(*) FROM graph_node_settings a JOIN graph_node_settings b ON a.setting=b.setting WHERE a.node=? AND b.node=? AND a.value<>b.value`,
		`SELECT count(*) FROM project_policies a JOIN project_policies b ON a.node_id=b.node_id WHERE a.project=? AND b.project=? AND a.policy<>b.policy`,
		`SELECT count(*) FROM graph_edges a JOIN graph_edges b ON (CASE WHEN a.source=?1 THEN ?2 ELSE a.source END)=(CASE WHEN b.source=?1 THEN ?2 ELSE b.source END) AND (CASE WHEN a.target=?1 THEN ?2 ELSE a.target END)=(CASE WHEN b.target=?1 THEN ?2 ELSE b.target END) AND a.kind=b.kind WHERE a.rowid<b.rowid AND (a.source=?1 OR a.target=?1 OR b.source=?1 OR b.target=?1) AND (a.mode<>b.mode OR a.rule<>b.rule)`,
		`SELECT count(*) FROM graph_nodes WHERE name=?2 AND kind='architect' AND ?1<>?2`,
	} {
		var n int
		if err := tx.QueryRow(q, old, name).Scan(&n); err != nil {
			return false, err
		}
		if n != 0 {
			return false, fmt.Errorf("conflicting graph controls require owner review")
		}
	}
	var conflicts int
	if err := tx.QueryRow(`SELECT count(*) FROM graph_charter a JOIN graph_charter b ON 1=1 WHERE a.node=? AND b.node=? AND a.value<>'' AND b.value<>''`, old, name).Scan(&conflicts); err != nil {
		return false, err
	}
	// Keep each charter value. Exact duplicates need not be repeated.
	statements := []string{
		`INSERT OR IGNORE INTO graph_nodes SELECT ?,kind,created_at FROM graph_nodes WHERE name=?`,
		`INSERT INTO graph_charter SELECT ?,attr,value,updated_at,updated_by FROM graph_charter WHERE node=? ON CONFLICT(node,attr) DO UPDATE SET value=CASE WHEN excluded.value='' OR graph_charter.value=excluded.value THEN graph_charter.value WHEN graph_charter.value='' THEN excluded.value ELSE graph_charter.value || char(10) || excluded.value END, updated_at=MAX(graph_charter.updated_at,excluded.updated_at)`,
		`INSERT INTO graph_node_settings SELECT ?,setting,value,updated_at,updated_by FROM graph_node_settings WHERE node=? ON CONFLICT(node,setting) DO UPDATE SET value=CASE WHEN excluded.updated_at>graph_node_settings.updated_at THEN excluded.value ELSE graph_node_settings.value END, updated_by=CASE WHEN excluded.updated_at>graph_node_settings.updated_at THEN excluded.updated_by ELSE graph_node_settings.updated_by END, updated_at=MAX(graph_node_settings.updated_at,excluded.updated_at)`,
		`INSERT INTO project_policies SELECT node_id,?,policy,updated_at FROM project_policies WHERE project=? ON CONFLICT(node_id,project) DO UPDATE SET policy=CASE WHEN excluded.updated_at>project_policies.updated_at THEN excluded.policy ELSE project_policies.policy END, updated_at=MAX(project_policies.updated_at,excluded.updated_at)`,
		`INSERT INTO learned_views SELECT ?,last_seen_id FROM learned_views WHERE node=? ON CONFLICT(node) DO UPDATE SET last_seen_id=MIN(learned_views.last_seen_id,excluded.last_seen_id)`,
	}
	for _, q := range statements {
		if _, err := tx.Exec(q, name, old); err != nil {
			return false, err
		}
	}
	// Move edges in one pass so self-edges and collisions are handled together.
	if _, err := tx.Exec(`INSERT OR IGNORE INTO graph_edges SELECT CASE WHEN source=? THEN ? ELSE source END,CASE WHEN target=? THEN ? ELSE target END,kind,mode,rule FROM graph_edges WHERE source=? OR target=?`, old, name, old, name, old, old); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`DELETE FROM graph_edges WHERE source=? OR target=?`, old, old); err != nil {
		return false, err
	}
	for _, table := range []string{"graph_charter", "graph_node_settings", "learned_views"} {
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE node=?`, old); err != nil {
			return false, err
		}
	}
	// Project playbook notes are text too, so keep both when they collide.
	if _, err := tx.Exec(`INSERT INTO playbook SELECT subject_kind,?,harness,note,source,support,track_line,pinned,stale,created_at,updated_at FROM playbook WHERE subject_kind='project' AND subject=? ON CONFLICT(subject_kind,subject,harness) DO UPDATE SET note=CASE WHEN playbook.note=excluded.note THEN playbook.note ELSE playbook.note || char(10) || excluded.note END`, name, old); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`DELETE FROM playbook WHERE subject_kind='project' AND subject=?`, old); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`DELETE FROM project_policies WHERE project=?`, old); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`DELETE FROM graph_nodes WHERE name=?`, old); err != nil {
		return false, err
	}
	for _, q := range []string{
		`UPDATE sessions SET repo_project=? WHERE repo_project=?`, `UPDATE projects SET name=? WHERE name=?`,
		`UPDATE graph_history SET node=? WHERE node=?`, `UPDATE learned_suggestions SET node=? WHERE node=?`,
		`UPDATE learned_suggestions SET phrase=? WHERE field='edge' AND phrase=?`,
		`UPDATE graph_outcomes SET source=? WHERE source=?`, `UPDATE graph_outcomes SET target=? WHERE target=?`, `UPDATE graph_outcomes SET redirect_to=? WHERE redirect_to=?`,
		`UPDATE inbox_items SET node=? WHERE node=?`, `UPDATE charter_signals SET source=? WHERE source=?`, `UPDATE charter_signals SET target=? WHERE target=?`,
		`UPDATE learned_message_flow SET source=? WHERE source=?`, `UPDATE learned_message_flow SET target=? WHERE target=?`,
		`UPDATE findings SET subject=? WHERE subject_kind='project' AND subject=?`,
	} {
		if _, err := tx.Exec(q, name, old); err != nil {
			return false, err
		}
	}
	for _, column := range []string{"old_value", "new_value"} {
		for _, field := range []string{"from", "to"} {
			q := `UPDATE graph_history SET ` + column + `=json_set(` + column + `, '$.` + field + `', ?) WHERE type='edge' AND json_valid(` + column + `) AND json_extract(` + column + `, '$.` + field + `')=?`
			if _, err := tx.Exec(q, name, old); err != nil {
				return false, err
			}
		}
	}
	return conflicts > 0, nil
}
