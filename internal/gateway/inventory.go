package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	"github.com/niparis/agent-net/internal/protocol"
)

// SetInventory replaces a successful observation. A failed scan retains the
// last successful items as stale rather than treating a failure as deletion.
func (s *Store) SetInventory(nodeID string, r protocol.InventoryReport) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if r.State == protocol.InventoryError {
		var old string
		err = tx.QueryRow(`SELECT report FROM inventory_reports WHERE node_id=?`, nodeID).Scan(&old)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			var previous protocol.InventoryReport
			if err = json.Unmarshal([]byte(old), &previous); err != nil {
				return err
			}
			for i := range previous.Items {
				previous.Items[i].State = protocol.InventoryStale
				previous.Items[i].Reason = "latest scan failed"
			}
			previous.State = protocol.InventoryStale
			previous.Reason = "latest scan failed"
			previous.ScannedAt = r.ScannedAt
			r = previous
		}
	}
	r.Machine = nodeID
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO inventory_reports(node_id,report) VALUES(?,?) ON CONFLICT(node_id) DO UPDATE SET report=excluded.report`, nodeID, string(b)); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM tool_locations WHERE node_id=?`, nodeID); err != nil {
		return err
	}
	for i, item := range r.Items {
		b, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT OR IGNORE INTO tool_identities(id,kind,name,hash) VALUES(json_array(?,?,?),?,?,?)`, item.Kind, item.Name, item.Hash, item.Kind, item.Name, item.Hash); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO tool_locations(node_id,ordinal,identity_id,observation) VALUES(?,?,json_array(?,?,?),?)`, nodeID, i, item.Kind, item.Name, item.Hash, string(b)); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`DELETE FROM tool_identities WHERE NOT EXISTS (SELECT 1 FROM tool_locations WHERE identity_id=tool_identities.id) AND NOT EXISTS (SELECT 1 FROM skill_versions WHERE item=tool_identities.id) AND NOT EXISTS (SELECT 1 FROM tool_revisions WHERE item=tool_identities.id) AND NOT EXISTS (SELECT 1 FROM mcp_servers m WHERE tool_identities.kind='mcp' AND tool_identities.name=m.name AND tool_identities.hash='federated-'||m.added_at)`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Inventories() ([]protocol.InventoryReport, error) {
	rows, err := s.db.Query(`SELECT i.node_id,i.report,COALESCE(n.hostname,'') FROM inventory_reports i LEFT JOIN nodes n ON n.id=i.node_id ORDER BY i.node_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reports := []protocol.InventoryReport{}
	for rows.Next() {
		var id, raw, host string
		if err = rows.Scan(&id, &raw, &host); err != nil {
			return nil, err
		}
		var r protocol.InventoryReport
		if err = json.Unmarshal([]byte(raw), &r); err != nil {
			return nil, err
		}
		r.NodeID = id
		r.Machine = host
		if r.Machine == "" {
			r.Machine = id
		}
		reports = append(reports, r)
	}
	return reports, rows.Err()
}

// inventoryPage splits reports by item and limits encoded response size.
func inventoryPage(reports []protocol.InventoryReport, offset int) (protocol.InventoryPage, error) {
	const maxBytes = 240 << 10
	if offset < 0 {
		return protocol.InventoryPage{}, errors.New("invalid inventory offset")
	}
	page := protocol.InventoryPage{Reports: []protocol.InventoryReport{}}
	index := 0
	for _, report := range reports {
		items := report.Items
		if len(items) == 0 {
			items = []protocol.InventoryItem{{}}
		}
		for _, item := range items {
			if index < offset {
				index++
				continue
			}
			one := report
			one.Items = nil
			if len(report.Items) > 0 {
				one.Items = []protocol.InventoryItem{item}
			}
			page.Reports = append(page.Reports, one)
			b, err := json.Marshal(page)
			if err != nil {
				return page, err
			}
			if len(b) > maxBytes {
				page.Reports = page.Reports[:len(page.Reports)-1]
				if len(page.Reports) == 0 {
					return page, errors.New("inventory item exceeds response limit")
				}
				page.Next = index
				return page, nil
			}
			index++
		}
	}
	return page, nil
}

func markInventoryOffline(r *protocol.InventoryReport) {
	r.State = protocol.InventoryStale
	for i := range r.Items {
		r.Items[i].State = protocol.InventoryStale
	}
}

// ToolsItems combines observations by immutable kind, name and content hash.
func (s *Store) ToolsItems(f protocol.ToolsFilter, online func(string) bool) ([]protocol.ToolItem, error) {
	rows, err := s.db.Query(`SELECT i.id,i.kind,i.name,i.hash,l.node_id,COALESCE(n.hostname,''),l.observation,COALESCE(p.name,'')
 FROM tool_identities i JOIN tool_locations l ON l.identity_id=i.id LEFT JOIN nodes n ON n.id=l.node_id
 LEFT JOIN projects p ON p.node_id=l.node_id AND p.path=json_extract(l.observation,'$.project_root') AND p.name=json_extract(l.observation,'$.project')
 ORDER BY i.kind,i.name,i.hash,l.node_id,l.ordinal`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	all := map[string]*protocol.ToolItem{}
	matched := map[string]bool{}
	order := []string{}
	variants := map[string]map[string]bool{}
	for rows.Next() {
		var id, kind, name, hash, node, host, raw, project string
		if err = rows.Scan(&id, &kind, &name, &hash, &node, &host, &raw, &project); err != nil {
			return nil, err
		}
		key := kind + "\x00" + name
		if variants[key] == nil {
			variants[key] = map[string]bool{}
		}
		variants[key][id] = true
		var obs protocol.InventoryItem
		if err = json.Unmarshal([]byte(raw), &obs); err != nil {
			return nil, err
		}
		if !online(node) {
			obs.State = protocol.InventoryStale
		}
		if obs.Scope == protocol.InventoryProject {
			obs.Project = project
		}
		if (f.Kind == "" || kind == f.Kind) && (f.Harness == "" || obs.Harness == f.Harness) && (f.Project == "" || obs.Project == f.Project) && (f.State == "" || obs.State == f.State) {
			matched[id] = true
		}
		item := all[id]
		if item == nil {
			item = &protocol.ToolItem{ID: id, Kind: kind, Name: name, Hash: hash, State: protocol.InventoryOK}
			all[id] = item
			order = append(order, id)
		}
		if host == "" {
			host = node
		}
		loc := protocol.ToolLocation{NodeID: node, Machine: host, InventoryItem: obs}
		if project != "" && obs.Scope == protocol.InventoryProject {
			loc.GraphNode = project
		}
		item.Locations = append(item.Locations, loc)
		if obs.Scope == protocol.InventoryUser {
			item.Global = true
		} else if project != "" {
			item.Projects = appendUnique(item.Projects, project)
		} else {
			item.Unassigned = true
		}
		item.Harnesses = appendUnique(item.Harnesses, obs.Harness)
		item.Machines = appendUnique(item.Machines, host)
		if obs.State == protocol.InventoryError || (obs.State == protocol.InventoryStale && item.State != protocol.InventoryError) {
			item.State = obs.State
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	retained, err := s.db.Query(`SELECT i.id,i.kind,i.name,i.hash FROM tool_identities i WHERE NOT EXISTS (SELECT 1 FROM tool_locations l WHERE l.identity_id=i.id) AND (EXISTS (SELECT 1 FROM skill_versions v WHERE v.item=i.id) OR EXISTS (SELECT 1 FROM tool_revisions r WHERE r.item=i.id) OR (i.kind='mcp' AND i.hash LIKE 'federated-%')) ORDER BY i.kind,i.name,i.hash`)
	if err != nil {
		return nil, err
	}
	for retained.Next() {
		var id, kind, name, hash string
		if err = retained.Scan(&id, &kind, &name, &hash); err != nil {
			retained.Close()
			return nil, err
		}
		if isFederatedHash(hash) {
			// A registry server is not a copy on a machine, so it is not a
			// variant of a native entry with the same name.
			all[id] = &protocol.ToolItem{ID: id, Kind: kind, Name: name, Hash: hash, Locations: []protocol.ToolLocation{}, Harnesses: []string{}, Machines: []string{}, Projects: []string{}, State: protocol.InventoryOK, Federated: true}
			order = append(order, id)
			continue
		}
		key := kind + "\x00" + name
		if variants[key] == nil {
			variants[key] = map[string]bool{}
		}
		variants[key][id] = true
		all[id] = &protocol.ToolItem{ID: id, Kind: kind, Name: name, Hash: hash, Locations: []protocol.ToolLocation{}, Unassigned: true, State: "unobserved"}
		order = append(order, id)
		if (f.Kind == "" || f.Kind == kind) && f.Harness == "" && f.Project == "" && (f.State == "" || f.State == "unobserved") {
			matched[id] = true
		}
	}
	err = retained.Err()
	retained.Close()
	if err != nil {
		return nil, err
	}
	revisions, err := s.db.Query(`SELECT r.item,r.revision,r.assignment,r.version,r.actor,r.source,r.at FROM tool_revisions r WHERE r.revision=(SELECT MAX(t.revision) FROM tool_revisions t WHERE t.item=r.item)`)
	if err != nil {
		return nil, err
	}
	for revisions.Next() {
		var r protocol.ToolRevision
		var raw string
		if err = revisions.Scan(&r.Item, &r.Revision, &raw, &r.Version, &r.Actor, &r.Source, &r.At); err != nil {
			revisions.Close()
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &r.Assignment); err != nil {
			revisions.Close()
			return nil, err
		}
		r.State = "pending"
		if item := all[r.Item]; item != nil {
			item.Desired = &r
			item.Applied = "pending"
		}
	}
	err = revisions.Err()
	revisions.Close()
	if err != nil {
		return nil, err
	}

	// Compare reported outcomes with all targets in the applied plan. Missing
	// reports remain pending, including those belonging to offline machines.
	resultRows, err := s.db.Query(`SELECT r.item,r.revision,p.plan FROM tool_revisions r JOIN tool_plans p ON r.source='plan:' || p.id AND p.applied=1 WHERE r.revision=(SELECT MAX(x.revision) FROM tool_revisions x WHERE x.item=r.item)`)
	if err != nil {
		return nil, err
	}
	plans := map[string]protocol.ToolPlan{}
	for resultRows.Next() {
		var id, raw string
		var revision int64
		if err = resultRows.Scan(&id, &revision, &raw); err != nil {
			resultRows.Close()
			return nil, err
		}
		var plan protocol.ToolPlan
		if err = json.Unmarshal([]byte(raw), &plan); err != nil {
			resultRows.Close()
			return nil, err
		}
		plans[id] = plan
	}
	err = resultRows.Err()
	resultRows.Close()
	if err != nil {
		return nil, err
	}
	resultRows, err = s.db.Query(`SELECT item,revision,target_index,node_id,state,reason FROM tool_results ORDER BY item,target_index`)
	if err != nil {
		return nil, err
	}
	for resultRows.Next() {
		var r protocol.ToolResult
		if err = resultRows.Scan(&r.Item, &r.Revision, &r.Index, &r.NodeID, &r.State, &r.Reason); err != nil {
			resultRows.Close()
			return nil, err
		}
		if item := all[r.Item]; item != nil && item.Desired != nil && item.Desired.Revision == r.Revision {
			if p, ok := plans[r.Item]; ok && r.Index >= 0 && r.Index < len(p.Targets) {
				target := p.Targets[r.Index]
				r.Harness = target.Harness
				r.Project = target.Project
				r.Checkout = target.Checkout
			}
			item.Results = append(item.Results, r)
		}
	}
	err = resultRows.Err()
	resultRows.Close()
	if err != nil {
		return nil, err
	}
	for id, plan := range plans {
		item := all[id]
		if item == nil || item.Desired == nil {
			continue
		}
		for _, r := range item.Results {
			if r.State == "applied" {
				item.Applied = "partial"
				break
			}
		}
		if len(plan.Targets) > 0 && len(item.Results) == len(plan.Targets) {
			allApplied := true
			for _, r := range item.Results {
				if r.State != "applied" {
					allApplied = false
					if item.Applied != "partial" {
						item.Applied = r.State
					}
				}
			}
			if allApplied {
				item.Applied = "applied"
			}
		}
	}
	if err = s.fillFederatedItems(all, f, matched, online); err != nil {
		return nil, err
	}
	out := make([]protocol.ToolItem, 0, len(order))
	for _, id := range order {
		item := all[id]
		if !matched[id] {
			continue
		}
		item.Variants = len(variants[item.Kind+"\x00"+item.Name])
		out = append(out, *item)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Hash < b.Hash
	})
	return out, nil
}
func appendUnique(list []string, value string) []string {
	for _, v := range list {
		if v == value {
			return list
		}
	}
	return append(list, value)
}

func (s *Store) ToolsItem(id string, online func(string) bool) (protocol.ToolDetail, error) {
	items, err := s.ToolsItems(protocol.ToolsFilter{}, online)
	if err != nil {
		return protocol.ToolDetail{}, err
	}
	var found *protocol.ToolItem
	for i := range items {
		if items[i].ID == id {
			found = &items[i]
			break
		}
	}
	if found == nil {
		return protocol.ToolDetail{}, errors.New("tool item not found")
	}
	out := protocol.ToolDetail{Item: *found, VariantDiffs: []protocol.ToolVariantDiff{}}
	files := func(item protocol.ToolItem) map[string]bool {
		m := map[string]bool{}
		for _, l := range item.Locations {
			for _, p := range l.Files {
				m[p] = true
			}
		}
		return m
	}
	base := files(*found)
	for _, v := range items {
		if v.ID == id || v.Kind != found.Kind || v.Name != found.Name {
			continue
		}
		other := files(v)
		d := protocol.ToolVariantDiff{ID: v.ID, Hash: v.Hash, Added: []string{}, Removed: []string{}}
		for p := range other {
			if !base[p] {
				d.Added = append(d.Added, p)
			}
		}
		for p := range base {
			if !other[p] {
				d.Removed = append(d.Removed, p)
			}
		}
		sort.Strings(d.Added)
		sort.Strings(d.Removed)
		out.VariantDiffs = append(out.VariantDiffs, d)
	}
	return out, nil
}

func toolsPage(items []protocol.ToolItem, offset int) (protocol.ToolsPage, error) {
	page := protocol.ToolsPage{Items: []protocol.ToolItem{}}
	if offset < 0 || offset > len(items) {
		return page, errors.New("invalid tools offset")
	}
	for i := offset; i < len(items); i++ {
		page.Items = append(page.Items, items[i])
		b, err := json.Marshal(page)
		if err != nil {
			return page, err
		}
		if len(b) > 240<<10 {
			page.Items = page.Items[:len(page.Items)-1]
			if len(page.Items) == 0 {
				return page, errors.New("tool item exceeds response limit")
			}
			page.Next = i
			return page, nil
		}
	}
	return page, nil
}
