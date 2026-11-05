package gateway

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/temppath"
)

const workerSandboxKey = "workers.sandbox_level"

// workerProjectNamesTx follows Graph's node discovery, including its filtering
// of implicit projects, without opening another connection during a migration.
func workerProjectNamesTx(tx *sql.Tx) (map[string][]string, error) {
	var hasArchive bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('sessions') WHERE name='archived_at')`).Scan(&hasArchive); err != nil {
		return nil, err
	}
	filter := ""
	if hasArchive {
		filter = "archived_at=0 AND "
	}
	rows, err := tx.Query(`SELECT name,1,'',2 FROM graph_nodes
 UNION ALL SELECT name,0,path,graph_excluded FROM projects WHERE graph_excluded<>1 AND name NOT IN (SELECT name FROM graph_nodes)
 UNION ALL SELECT repo_project,0,repo_root,graph_excluded FROM sessions WHERE ` + filter + `graph_excluded<>1 AND repo_project<>'' AND repo_project NOT IN (SELECT name FROM graph_nodes)
 UNION ALL SELECT source,1,'',2 FROM graph_edges WHERE source<>'*' AND source NOT IN (SELECT name FROM graph_nodes)
 UNION ALL SELECT target,1,'',2 FROM graph_edges WHERE target NOT IN (SELECT name FROM graph_nodes) ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := map[string][]string{}
	seen := map[string]bool{}
	for rows.Next() {
		var name, root string
		var explicit, checked int
		if err := rows.Scan(&name, &explicit, &root, &checked); err != nil {
			return nil, err
		}
		if explicit == 0 && (root == "" || (checked == 0 && !graphProjectRoot(root))) {
			continue
		}
		if !seen[name] {
			k := strings.ToLower(name)
			names[k] = append(names[k], name)
			seen[name] = true
		}
	}
	return names, rows.Err()
}

// legacyNodeWorkerSandboxTx reproduces the pre-cutover lookup, including its
// early default when no maturity row exists. It uses the migration transaction.
func legacyNodeWorkerSandboxTx(tx *sql.Tx, project string) (string, error) {
	var maturity string
	err := tx.QueryRow(`SELECT value FROM graph_charter WHERE node=? AND attr='maturity'`, project).Scan(&maturity)
	if err == sql.ErrNoRows {
		return protocol.WorkersSandboxed, nil
	}
	if err != nil {
		return "", err
	}
	var value string
	err = tx.QueryRow(`SELECT value FROM graph_node_settings WHERE node=? AND setting='worker_sandbox'`, project).Scan(&value)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	return protocol.NodeDefaults(maturity, map[string]string{protocol.SettingWorkerSandbox: value}).WorkerSandbox, nil
}

type legacyWorkerPolicy struct {
	workers map[string]string // machine ID to explicit, valid policy
}

type legacyWorkerMachine struct {
	host     string
	projects map[string]bool // exact names used by this machine's starts
}

// cutoverWorkerSandboxTx preserves every machine's old effective restrictions.
// Existing repo values are kept unless they would make a machine less safe.
func (s *Store) cutoverWorkerSandboxTx(tx *sql.Tx) error {
	names, err := workerProjectNamesTx(tx)
	if err != nil {
		return err
	}
	rank := map[string]int{protocol.WorkersSandboxed: 1, protocol.WorkersNoNetwork: 2, protocol.WorkersReadOnly: 3}
	strictest := func(a, b string) string {
		if rank[b] > rank[a] {
			return b
		}
		return a
	}

	graph := map[string]string{}
	rows, err := tx.Query(`SELECT node,value FROM graph_node_settings WHERE setting='worker_sandbox' ORDER BY node`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var node, value string
		if err = rows.Scan(&node, &value); err != nil {
			rows.Close()
			return err
		}
		graph[node] = value
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}

	policies := map[string]*legacyWorkerPolicy{}
	machines := map[string]map[string]*legacyWorkerMachine{} // lower-case project -> machine ID
	addMachine := func(project, id, host string, actualName bool) {
		key := strings.ToLower(strings.TrimSpace(project))
		if key == "" || id == "" {
			return
		}
		if machines[key] == nil {
			machines[key] = map[string]*legacyWorkerMachine{}
		}
		machine := machines[key][id]
		if machine == nil {
			machine = &legacyWorkerMachine{projects: map[string]bool{}}
			machines[key][id] = machine
		}
		if actualName {
			machine.projects[project] = true
		}
		if host == "" {
			host = id
		}
		machine.host = host
	}
	rows, err = tx.Query(`SELECT p.node_id,p.project,p.policy,COALESCE(n.hostname,'') FROM project_policies p LEFT JOIN nodes n ON n.id=p.node_id ORDER BY p.project,p.node_id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, project, raw, host string
		if err = rows.Scan(&id, &project, &raw, &host); err != nil {
			rows.Close()
			return err
		}
		addMachine(project, id, host, false)
		key := strings.ToLower(strings.TrimSpace(project))
		// WorkerPermissions queried the normalized project as an exact key.
		if project != key {
			continue
		}
		var p protocol.DelegationPolicy
		if json.Unmarshal([]byte(raw), &p) != nil || rank[p.Workers] == 0 {
			continue
		}
		if policies[key] == nil {
			policies[key] = &legacyWorkerPolicy{workers: map[string]string{}}
		}
		policies[key].workers[id] = p.Workers
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// Match Projects: a registered folder wins over a seen session, and
	// an unregistered session without repo metadata uses its folder name.
	registered := map[string]string{}
	rows, err = tx.Query(`SELECT p.node_id,p.name,p.path,COALESCE(n.hostname,'') FROM projects p LEFT JOIN nodes n ON n.id=p.node_id ORDER BY p.path`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, project, path, host string
		if err = rows.Scan(&id, &project, &path, &host); err != nil {
			rows.Close()
			return err
		}
		registered[id+"\x00"+path] = project
		addMachine(project, id, host, true)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = tx.Query(`SELECT a.node_id,n.hostname,a.cwd,a.repo_root,a.repo_project,a.no_project FROM sessions a JOIN nodes n ON n.id=a.node_id WHERE a.harness<>'owner' ORDER BY a.last_seen DESC,a.id`)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for rows.Next() {
		var id, host, cwd, root, project string
		var noProject int
		if err = rows.Scan(&id, &host, &cwd, &root, &project, &noProject); err != nil {
			rows.Close()
			return err
		}
		path := root
		if path == "" {
			path = cwd
		}
		if path == "" || noProject != 0 || temppath.Under(path, s.tempRoots()) {
			continue
		}
		address := id + "\x00" + path
		if seen[address] {
			continue
		}
		seen[address] = true
		if project == "" {
			project = filepath.Base(path)
		}
		// A name-filtered lookup may return a seen project whose registered
		// name differs. Equal names merge and use the registered spelling.
		if name, ok := registered[address]; ok && strings.EqualFold(name, project) {
			continue
		}
		addMachine(project, id, host, true)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}

	existing := map[string]string{}
	var network string
	rows, err = tx.Query(`SELECT level,scope,value FROM setting_values WHERE key=? AND item='' AND (level='repo' OR (level='network' AND scope='network'))`, workerSandboxKey)
	if err != nil {
		return err
	}
	for rows.Next() {
		var level, scope, raw string
		if err = rows.Scan(&level, &scope, &raw); err != nil {
			rows.Close()
			return err
		}
		if level == "repo" {
			existing[scope] = raw
		} else {
			_ = json.Unmarshal([]byte(raw), &network)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if rank[network] == 0 {
		network = protocol.WorkersSandboxed
	}

	projects := map[string]bool{}
	for name := range graph {
		projects[strings.ToLower(name)] = true
	}
	for name := range policies {
		projects[name] = true
	}
	for name := range machines {
		projects[name] = true
	}
	for name := range existing {
		projects[strings.ToLower(name)] = true
	}
	keys := make([]string, 0, len(projects))
	for k := range projects {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	notice := func(subject, body string) error {
		var exists int
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM inbox_items WHERE session_id=? AND node=? AND text=?)`, settingsCutoverActor, subject, body).Scan(&exists); err != nil {
			return err
		}
		if exists != 0 {
			return nil
		}
		_, err := s.createInboxItem(tx, protocol.InboxItem{Kind: "check", SessionID: settingsCutoverActor, Node: subject, Text: body})
		return err
	}
	type change struct {
		scope, old, desired string
		hadOld              bool
	}
	var changes []change
	for _, key := range keys {
		if len(names[key]) > 1 {
			if graphHasKey(graph, key) || policies[key] != nil {
				if err = notice(key, fmt.Sprintf("Worker sandbox cutover skipped %s: graph node names differ only by case. Resolve the names and set workers.sandbox_level for the project.", key)); err != nil {
					return err
				}
			}
			continue
		}
		scope := key
		if len(names[key]) == 1 {
			scope = names[key][0]
		}
		fallbackCache := map[string]string{}
		fallbackFor := func(project string) (string, error) {
			if v, ok := fallbackCache[project]; ok {
				return v, nil
			}
			v, e := legacyNodeWorkerSandboxTx(tx, project)
			if e == nil {
				fallbackCache[project] = v
			}
			return v, e
		}
		fallback, e := fallbackFor(scope)
		if e != nil {
			return e
		}
		minimum := fallback
		if policy := policies[key]; policy != nil {
			for _, value := range policy.workers {
				minimum = strictest(minimum, value)
			}
		}
		oldByMachine := map[string]map[string]bool{}
		for id, machine := range machines[key] {
			values := map[string]bool{}
			if p := policies[key]; p != nil && p.workers[id] != "" {
				values[p.workers[id]] = true
			} else {
				for project := range machine.projects {
					v, e := fallbackFor(project)
					if e != nil {
						return e
					}
					values[v] = true
					minimum = strictest(minimum, v)
				}
				if len(values) == 0 {
					// An orphaned policy has no known project spelling.
					// Use the mapped node's fallback rather than inventing a
					// lower-case project name the machine may never have used.
					values[fallback] = true
				}
			}
			oldByMachine[id] = values
		}
		oldRaw, hadOld := existing[scope]
		var oldValue string
		if hadOld {
			_ = json.Unmarshal([]byte(oldRaw), &oldValue)
		}
		target := minimum
		graphValue, hasGraph := graph[scope]
		// A valid legacy graph row was copied by the original migration even
		// without a maturity row. Keep that behavior when no policy replaced it.
		if policies[key] == nil && hasGraph {
			target = strictest(target, graphValue)
		}
		desired := target
		if hadOld {
			// An explicit network rule may already enforce the old floor.
			// Keep the repo address untouched whenever its effective value is safe.
			if rank[oldValue] != 0 && rank[strictest(oldValue, network)] >= rank[minimum] {
				desired = oldValue
			} else {
				desired = minimum
			}
		}
		shouldWrite := (!hadOld && (hasGraph || policies[key] != nil || rank[minimum] > rank[strictest(fallback, network)])) || (hadOld && desired != oldValue)
		if shouldWrite {
			changes = append(changes, change{scope: scope, old: oldRaw, desired: desired, hadOld: hadOld})
		}
		// If there is no repo row, the resolver still applies the maturity
		// fallback; include the network value when reporting effective changes.
		effective := strictest(desired, network)
		var affected []string
		hostCount := map[string]int{}
		for _, machine := range machines[key] {
			hostCount[machine.host]++
		}
		for id, machine := range machines[key] {
			label := protocol.NeutralizeMarkers(machine.host)
			if hostCount[machine.host] > 1 {
				label += " (" + id + ")"
			}
			for old := range oldByMachine[id] {
				if rank[old] < rank[effective] {
					affected = append(affected, fmt.Sprintf("%s: %s", label, old))
				}
			}
		}
		sort.Strings(affected)
		// Keep the normal case in one check, splitting only when the list
		// would exceed the Inbox text limit.
		prefix := fmt.Sprintf("Worker sandbox cutover for %s uses %s. Machines made stricter (previous values): ", scope, effective)
		body := prefix
		for _, machine := range affected {
			sep := ""
			if body != prefix {
				sep = ", "
			}
			if len(body)+len(sep)+len(machine)+1 > protocol.MaxBodyBytes && body != prefix {
				if err = notice(scope, body+"."); err != nil {
					return err
				}
				body = prefix
				sep = ""
			}
			body += sep + machine
		}
		if body != prefix {
			if err = notice(scope, body+"."); err != nil {
				return err
			}
		}
		if len(affected) == 0 && hadOld && shouldWrite {
			body := fmt.Sprintf("Worker sandbox cutover for %s uses %s. Its existing setting was tightened.", scope, effective)
			if err = notice(scope, body); err != nil {
				return err
			}
		}
	}
	if len(changes) == 0 {
		return nil
	}
	if err = bumpSettingsRevision(tx); err != nil {
		return err
	}
	var rev int64
	if err = tx.QueryRow(`SELECT revision FROM setting_revision WHERE id=1`).Scan(&rev); err != nil {
		return err
	}
	for i, c := range changes {
		b, _ := json.Marshal(c.desired)
		var old any
		if c.hadOld {
			old = c.old
		}
		if _, err = tx.Exec(`INSERT INTO setting_values(key,level,scope,item,value,revision) VALUES(?,'repo',?,'',?,?) ON CONFLICT(key,level,scope,item) DO UPDATE SET value=excluded.value,revision=excluded.revision`, workerSandboxKey, c.scope, string(b), rev); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO setting_history(revision,seq,key,level,scope,item,old_value,new_value,actor,reason,cause,at) VALUES(?,?,?,'repo',?,'',?,?,?,'migrate worker sandbox','cutover',?)`, rev, i+1, workerSandboxKey, c.scope, old, string(b), settingsCutoverActor, s.now().UnixNano()); err != nil {
			return err
		}
	}
	return nil
}

func graphHasKey(values map[string]string, key string) bool {
	for name := range values {
		if strings.ToLower(name) == key {
			return true
		}
	}
	return false
}
