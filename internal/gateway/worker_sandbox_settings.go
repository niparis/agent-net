package gateway

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

const workerSandboxKey = "workers.sandbox_level"

// workerProjectNamesTx follows Graph's node discovery, including its filtering
// of implicit projects, without opening another connection during a migration.
func workerProjectNamesTx(tx *sql.Tx) (map[string][]string, error) {
	rows, err := tx.Query(`SELECT name,1,'',2 FROM graph_nodes
 UNION ALL SELECT name,0,path,graph_excluded FROM projects WHERE graph_excluded<>1 AND name NOT IN (SELECT name FROM graph_nodes)
 UNION ALL SELECT repo_project,0,repo_root,graph_excluded FROM sessions WHERE graph_excluded<>1 AND repo_project<>'' AND repo_project NOT IN (SELECT name FROM graph_nodes)
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

// cutoverWorkerSandboxTx preserves legacy rows as a downgrade backup. Project
// policies took precedence over graph overrides, so they replace graph values;
// when machines differ the most restrictive project policy wins.
func (s *Store) cutoverWorkerSandboxTx(tx *sql.Tx) error {
	names, err := workerProjectNamesTx(tx)
	if err != nil {
		return err
	}
	values := map[string]string{}
	rows, err := tx.Query(`SELECT node,value FROM graph_node_settings WHERE setting='worker_sandbox' ORDER BY node`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var n, v string
		if err = rows.Scan(&n, &v); err != nil {
			rows.Close()
			return err
		}
		values[n] = v
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	type choice struct {
		value    string
		distinct map[string]bool
	}
	policies := map[string]*choice{}
	rows, err = tx.Query(`SELECT project,policy FROM project_policies ORDER BY project,node_id`)
	if err != nil {
		return err
	}
	rank := map[string]int{"sandboxed": 1, "no-network": 2, "read-only": 3}
	for rows.Next() {
		var name, raw string
		if err = rows.Scan(&name, &raw); err != nil {
			rows.Close()
			return err
		}
		var p protocol.DelegationPolicy
		if json.Unmarshal([]byte(raw), &p) != nil || rank[p.Workers] == 0 {
			continue
		}
		name = strings.ToLower(strings.TrimSpace(name))
		c := policies[name]
		if c == nil {
			c = &choice{distinct: map[string]bool{}}
			policies[name] = c
		}
		c.distinct[p.Workers] = true
		if rank[p.Workers] > rank[c.value] {
			c.value = p.Workers
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	ambiguous := map[string]bool{}
	for n := range values {
		if len(names[strings.ToLower(n)]) > 1 {
			ambiguous[strings.ToLower(n)] = true
		}
	}
	for key, c := range policies {
		if len(names[key]) > 1 {
			ambiguous[key] = true
			continue
		}
		scope := key
		if len(names[key]) == 1 {
			scope = names[key][0]
		}
		values[scope] = c.value
	}
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
	keys := make([]string, 0, len(ambiguous))
	for k := range ambiguous {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err = notice(k, fmt.Sprintf("Worker sandbox cutover skipped %s: graph node names differ only by case. Resolve the names and set workers.sandbox_level for the project.", k)); err != nil {
			return err
		}
	}
	keys = keys[:0]
	for k := range policies {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		c := policies[k]
		if len(c.distinct) > 1 && !ambiguous[k] {
			name := k
			if len(names[k]) == 1 {
				name = names[k][0]
			}
			if err = notice(name, fmt.Sprintf("Worker sandbox policies for %s differed between machines. The strictest legacy value was %s. An existing setting is kept; review workers.sandbox_level.", name, c.value)); err != nil {
				return err
			}
		}
	}
	keys = keys[:0]
	for n := range values {
		if !ambiguous[strings.ToLower(n)] {
			keys = append(keys, n)
		}
	}
	sort.Strings(keys)
	pending := keys[:0]
	for _, n := range keys {
		var exists int
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM setting_values WHERE key=? AND level='repo' AND scope=? AND item='')`, workerSandboxKey, n).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			pending = append(pending, n)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	if err = bumpSettingsRevision(tx); err != nil {
		return err
	}
	var rev int64
	if err = tx.QueryRow(`SELECT revision FROM setting_revision WHERE id=1`).Scan(&rev); err != nil {
		return err
	}
	for i, n := range pending {
		b, _ := json.Marshal(values[n])
		if _, err = tx.Exec(`INSERT INTO setting_values(key,level,scope,item,value,revision) VALUES(?,'repo',?,'',?,?)`, workerSandboxKey, n, string(b), rev); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO setting_history(revision,seq,key,level,scope,item,old_value,new_value,actor,reason,cause,at) VALUES(?,?,?,'repo',?,'',NULL,?,?,'migrate worker sandbox','cutover',?)`, rev, i+1, workerSandboxKey, n, string(b), settingsCutoverActor, s.now().UnixNano()); err != nil {
			return err
		}
	}
	return nil
}
