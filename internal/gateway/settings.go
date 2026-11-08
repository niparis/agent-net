package gateway

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

type SettingsChange = protocol.SettingsChange
type SettingsMove = protocol.SettingsMove
type SettingsResult struct {
	Revision int64          `json:"revision"`
	Moved    []SettingsMove `json:"moved"`
	changed  bool
}
type settingAddress struct{ key, level, scope, item string }

const deliveryGraphHistoryPrefix = "delivery.pipeline.graph."

type settingGraphChange struct {
	address settingAddress
	edge    protocol.Edge
}

func address(c SettingsChange) settingAddress { return settingAddress{c.Key, c.Level, c.Scope, c.Item} }

// settingsNotify runs only after commit. The hook is installed by the gateway.
func (s *Store) settingsNotify() {
	s.settingsHookMu.RLock()
	hook := s.settingsHook
	s.settingsHookMu.RUnlock()
	if hook != nil {
		hook()
	}
}

// deliverySettingsChanged queues approvals that become ready under a new
// delivery rule. The settings commit has already succeeded, so a queue error
// is logged and retried when the gateway starts.
func (s *Store) deliverySettingsChanged(changes []SettingsChange) {
	for _, c := range changes {
		if settings.DeliveryCanonicalKey(c.Key) != "" || strings.HasPrefix(c.Key, "delivery.pipeline.") {
			s.refreshDeliveryTasks()
			return
		}
	}
}

func (s *Store) refreshDeliveryTasks() {
	if err := s.backfillDeliveryTasks(); err != nil {
		s.logf("delivery task refresh", "err", err)
	}
}
func bumpSettingsRevision(tx *sql.Tx) error {
	_, err := tx.Exec(`UPDATE setting_revision SET revision=revision+1 WHERE id=1`)
	return err
}

// settingsTables are the tables settingsSnapshotTx reads. Keep it equal to the
// queries there (see readThrough).
var settingsTables = []string{"setting_revision", "setting_values", "graph_charter", "graph_edges"}

// SettingsSnapshot reads all settings. The read is kept until one of
// settingsTables is written, and each caller gets a copy.
func (s *Store) SettingsSnapshot() (settings.Snapshot, error) {
	v, err := s.readThrough("settings", settingsTables, false, func() (any, error) { return s.settingsSnapshotRead() })
	if err != nil {
		return settings.Snapshot{}, err
	}
	return copyOf(v.(settings.Snapshot)), nil
}

func (s *Store) settingsSnapshotRead() (settings.Snapshot, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return settings.Snapshot{}, err
	}
	defer tx.Rollback()
	out, err := settingsSnapshotTx(tx)
	if err != nil {
		return out, err
	}
	err = tx.Commit()
	return out, err
}
func settingsSnapshotTx(tx *sql.Tx) (settings.Snapshot, error) {
	out := settings.Snapshot{DeliveryCutover: true, DeliveryPipelineCutover: true, WorkerSandboxCutover: true, Recipe: settings.RecipeHash(), Values: []settings.Value{}, Maturity: map[string]string{}}
	if err := tx.QueryRow(`SELECT revision FROM setting_revision WHERE id=1`).Scan(&out.Revision); err != nil {
		return out, err
	}
	rows, err := tx.Query(`SELECT key,level,scope,item,value FROM setting_values ORDER BY key,level,scope,item`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v settings.Value
		var b string
		if err = rows.Scan(&v.Key, &v.Level, &v.Scope, &v.Item, &b); err != nil {
			rows.Close()
			return out, err
		}
		v.Value = json.RawMessage(b)
		out.Values = append(out.Values, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	// Older daemons know the shorthand keys only. These wire values are a
	// projection of the pipeline rows, never another stored rule.
	for _, v := range append([]settings.Value(nil), out.Values...) {
		if alias := settings.DeliveryShorthandKey(v.Key); alias != "" {
			value, err := settings.DeliveryShorthandValue(alias, v.Value)
			if err == nil {
				out.Values = append(out.Values, settings.Value{Key: alias, Level: v.Level, Scope: v.Scope, Item: v.Item, Value: value})
			}
		}
	}
	rows, err = tx.Query(`SELECT node,value FROM graph_charter WHERE attr='maturity' ORDER BY node`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var node, m string
		if err = rows.Scan(&node, &m); err != nil {
			rows.Close()
			return out, err
		}
		out.Maturity[node] = m
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = tx.Query(`SELECT target,kind,source FROM graph_edges WHERE kind IN ('accepts','reviews','tests','tests_after_merge','approves','approves_deploy','merges','releases','deploys','delivery_deploys','verifies') ORDER BY target,kind,source`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var a settings.DeliveryAssignment
		if err = rows.Scan(&a.Project, &a.Kind, &a.Node); err != nil {
			rows.Close()
			return out, err
		}
		ref := protocol.DeliveryEdgeRef(a.Kind)
		a.Step, a.For, a.AfterMerge = ref.Step, ref.For, ref.AfterMerge
		out.DeliveryAssignments = append(out.DeliveryAssignments, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	// Older daemons cannot express a node approver. Project the resulting
	// shorthand to them, using the stricter owner rule for custom approvers.
	for _, a := range out.DeliveryAssignments {
		alias, fallback := "", ""
		switch a.Kind {
		case "approves":
			alias, fallback = "delivery.merge_authority", "ask"
		case "approves_deploy":
			alias, fallback = "delivery.deploy_rule", "owner"
		default:
			continue
		}
		value := out.String(alias, settings.Where{Repo: a.Project})
		if value == "custom" {
			value = fallback
		}
		encoded, _ := json.Marshal(value)
		found := false
		for i := range out.Values {
			v := &out.Values[i]
			if v.Key == alias && v.Level == "repo" && v.Scope == a.Project && v.Item == "" {
				v.Value = encoded
				found = true
			}
		}
		if !found {
			out.Values = append(out.Values, settings.Value{Key: alias, Level: "repo", Scope: a.Project, Value: encoded})
		}
	}
	return out, nil
}

// settingsCutoverActor identifies an internal migration in history. It is
// reserved for history and is never accepted as a settings writer.
const settingsCutoverActor = "agent-net cutover"

func settingsActor(actor string) bool {
	switch actor {
	case "owner", "cto", "CTO (minor)", "CTO (owner window)":
		return true
	}
	const prefix = "owner (CTO proposal p_"
	if !strings.HasPrefix(actor, prefix) || !strings.HasSuffix(actor, ")") {
		return false
	}
	n := strings.TrimSuffix(strings.TrimPrefix(actor, prefix), ")")
	return n != "" && strings.Trim(n, "0123456789") == ""
}
func actorAllowed(actor, kind string) bool {
	if !settingsActor(actor) {
		return false
	}
	return actor == "owner" || actor == "cto" || strings.HasPrefix(actor, "owner (CTO proposal p_") || kind == "advice"
}

// ValidateSettings checks a batch against current state without committing it.
func (s *Store) ValidateSettings(changes []SettingsChange, actor string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = s.writeSettingsTx(tx, changes, actor, "", "write", nil)
	return err
}
func (s *Store) WriteSettings(changes []SettingsChange, actor, reason string) (SettingsResult, error) {
	return s.writeSettings(changes, actor, reason, "write", nil)
}
func (s *Store) writeSettings(changes []SettingsChange, actor, reason, cause string, undoOf *int64) (result SettingsResult, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	result, err = s.writeSettingsTx(tx, changes, actor, reason, cause, undoOf)
	if err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	if result.changed {
		s.deliverySettingsChanged(changes)
		s.settingsNotify()
	}
	return result, nil
}
func (s *Store) writeSettingsTx(tx *sql.Tx, changes []SettingsChange, actor, reason, cause string, undoOf *int64) (SettingsResult, error) {
	before, err := settingsSnapshotTx(tx)
	if err != nil {
		return SettingsResult{}, err
	}
	result := SettingsResult{Revision: before.Revision}
	changes = append([]SettingsChange(nil), changes...)
	aliases := map[settingAddress]string{}
	for i := range changes {
		c := &changes[i]
		canonical := settings.DeliveryCanonicalKey(c.Key)
		if canonical == "" {
			continue
		}
		if c.Value != nil {
			c.Value, err = settings.DeliveryCanonicalValue(c.Key, c.Value)
			if err != nil {
				return result, reject("invalid value: %v", err)
			}
		}
		if c.Expect != nil && !bytes.Equal(bytes.TrimSpace(c.Expect), []byte("null")) {
			expected := c.Expect
			c.Expect, err = settings.DeliveryCanonicalValue(c.Key, expected)
			if err != nil {
				// Invalid migrated values can be repaired only by a writer
				// that named their exact stored bytes.
				matched := false
				for _, v := range before.Values {
					if v.Key == canonical && v.Level == c.Level && v.Scope == c.Scope && v.Item == c.Item && bytes.Equal(v.Value, expected) {
						matched = true
						break
					}
				}
				if !matched {
					return result, reject("invalid expected shorthand: %v", err)
				}
				c.Expect = expected
			}
		}
		alias := c.Key
		c.Key = canonical
		aliases[address(*c)] = alias
	}
	recipe := settings.Standard()
	current := map[settingAddress]json.RawMessage{}
	for _, v := range before.Values {
		if settings.DeliveryCanonicalKey(v.Key) != "" {
			continue
		}
		current[settingAddress{v.Key, v.Level, v.Scope, v.Item}] = v.Value
	}
	staged := map[settingAddress]json.RawMessage{}
	for k, v := range current {
		staged[k] = v
	}
	seen := map[settingAddress]bool{}
	networkKeys := map[string]bool{}
	for _, c := range changes {
		d, ok := recipe.Definition(c.Key)
		if !ok {
			return result, reject("unknown setting %s", c.Key)
		}
		if !actorAllowed(actor, d.Kind) {
			return result, reject("%s cannot change %s", actor, d.Kind)
		}
		if c.Item != "" || (c.Level != "network" && c.Level != "repo") || !hasLevel(d.Levels, c.Level) || (c.Level == "network" && c.Scope != "network") || (c.Level == "repo" && c.Scope == "") {
			return result, reject("invalid setting address")
		}
		a := address(c)
		if seen[a] {
			return result, reject("duplicate setting address")
		}
		seen[a] = true
		old := current[a]
		var expect json.RawMessage
		if c.Expect != nil && !bytes.Equal(bytes.TrimSpace(c.Expect), []byte("null")) {
			expect, err = d.CodecExpected(c.Expect)
			if err != nil {
				// A broken legacy value can still be compared and repaired,
				// but only by a writer that saw its exact stored bytes.
				if !json.Valid(c.Expect) || !bytes.Equal(old, c.Expect) {
					return result, reject("invalid expected value: %v", err)
				}
				expect = c.Expect
			}
		}
		if !bytes.Equal(old, expect) {
			currentValue := string(old)
			if old == nil {
				currentValue = "not set"
			} else if alias := aliases[a]; alias != "" {
				if shorthand, err := settings.DeliveryShorthandValue(alias, old); err == nil {
					currentValue = string(shorthand)
				}
			}
			name := c.Key
			if alias := aliases[a]; alias != "" {
				name = alias
			}
			return result, reject("setting %s at %s %s changed (current %s)", name, c.Level, c.Scope, currentValue)
		}
		if c.Delete {
			if c.Value != nil {
				return result, reject("delete with value")
			}
			delete(staged, a)
		} else {
			if c.Value == nil {
				return result, reject("value required")
			}
			v, e := d.Codec(c.Value)
			if e != nil {
				return result, reject("invalid value: %v", e)
			}
			staged[a] = v
		}
		if c.Level == "network" {
			networkKeys[c.Key] = true
		}
	}
	var graphChanges []settingGraphChange
	for _, c := range changes {
		alias := aliases[address(c)]
		if c.Level != "repo" || (alias != "delivery.merge_authority" && alias != "delivery.deploy_rule") {
			continue
		}
		// Preserve explicit assignments when the underlying approver rule is unchanged.
		oldRule, oldErr := before.Resolve(c.Key, settings.Where{Repo: c.Scope})
		newSnap := settings.Snapshot{Maturity: before.Maturity, DeliveryPipelineCutover: true}
		for a, v := range staged {
			newSnap.Values = append(newSnap.Values, settings.Value{Key: a.key, Level: a.level, Scope: a.scope, Item: a.item, Value: v})
		}
		newRule, newErr := newSnap.Resolve(c.Key, settings.Where{Repo: c.Scope})
		kind := "approves"
		if alias == "delivery.deploy_rule" {
			kind = "approves_deploy"
		}
		var e protocol.Edge
		e.To, e.Kind = c.Scope, kind
		err := tx.QueryRow(`SELECT source,mode,rule FROM graph_edges WHERE target=? AND kind=?`, c.Scope, kind).Scan(&e.From, &e.Mode, &e.Rule)
		if err != nil && err != sql.ErrNoRows {
			return result, err
		}
		if err == nil && oldErr == nil && newErr == nil && bytes.Equal(oldRule.Value, newRule.Value) {
			if current[address(c)] == nil {
				delete(staged, address(c))
			} else {
				staged[address(c)] = current[address(c)]
			}
			continue
		}
		if err == nil {
			graphChanges = append(graphChanges, settingGraphChange{address: address(c), edge: e})
		}
	}
	makeSnapshot := func(vals map[settingAddress]json.RawMessage) settings.Snapshot {
		snap := settings.Snapshot{Revision: before.Revision, Maturity: before.Maturity, DeliveryPipelineCutover: true}
		for a, v := range vals {
			snap.Values = append(snap.Values, settings.Value{Key: a.key, Level: a.level, Scope: a.scope, Item: a.item, Value: v})
		}
		return snap
	}
	// Check explicit child writes against the resulting network value, not the maturity default.
	for _, c := range changes {
		if c.Level != "repo" || c.Delete {
			continue
		}
		d, _ := recipe.Definition(c.Key)
		if d.Kind != "rule" || d.OwnerScopedText() || staged[settingAddress{c.Key, "network", "network", ""}] == nil {
			continue
		}
		a := address(c)
		network, _ := makeSnapshot(staged).Resolve(c.Key, settings.Where{})
		combined, e := d.StricterValue(network.Value, staged[a])
		if e != nil {
			return result, e
		}
		if !bytes.Equal(combined, staged[a]) {
			return result, reject("network sets %s; a repo may only use a value at least as strict", network.Value)
		}
	}
	// For each changed network rule, tighten stored children and report every
	// affected repo, including maturity defaults that need no stored row.
	for key := range networkKeys {
		d, _ := recipe.Definition(key)
		if d.Kind != "rule" || d.OwnerScopedText() || !hasLevel(d.Levels, "repo") {
			continue
		}
		oldNet, _ := before.Resolve(key, settings.Where{})
		newNet, _ := makeSnapshot(staged).Resolve(key, settings.Where{})
		comb, e := d.StricterValue(oldNet.Value, newNet.Value)
		if e != nil {
			return result, e
		}
		netAddress := settingAddress{key, "network", "network", ""}
		// A removed network override releases the bound. A newly explicit
		// value establishes a bound even when it equals the recipe default.
		if staged[netAddress] == nil || (current[netAddress] != nil && bytes.Equal(comb, oldNet.Value)) {
			continue
		}
		repos := map[string]bool{}
		for repo := range before.Maturity {
			repos[repo] = true
		}
		rows, e := tx.Query(`SELECT name FROM graph_nodes WHERE kind='component'
 UNION SELECT name FROM projects WHERE graph_excluded<>1 AND name<>''
 UNION SELECT repo_project FROM sessions WHERE archived_at=0 AND graph_excluded<>1 AND repo_project<>''`)
		if e != nil {
			return result, e
		}
		for rows.Next() {
			var repo string
			if e = rows.Scan(&repo); e != nil {
				rows.Close()
				return result, e
			}
			repos[repo] = true
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return result, e
		}
		for a := range staged {
			if a.key == key && a.level == "repo" {
				repos[a.scope] = true
			}
		}
		for repo := range repos {
			a := settingAddress{key, "repo", repo, ""}
			oldEff, _ := before.Resolve(key, settings.Where{Repo: repo})
			rewritten := false
			if child, ok := staged[a]; ok {
				m, e := d.StricterValue(newNet.Value, child)
				if e != nil {
					s.logf("settings.invalid", "key", key, "level", "repo", "scope", repo, "err", e)
				} else if !bytes.Equal(m, child) {
					staged[a] = m
					rewritten = true
				}
			}
			newEff, _ := makeSnapshot(staged).Resolve(key, settings.Where{Repo: repo})
			if !bytes.Equal(oldEff.Value, newEff.Value) {
				result.Moved = append(result.Moved, SettingsMove{Key: key, Scope: repo, Old: oldEff.Value, New: newEff.Value, Rewritten: rewritten})
			}
		}
	}
	for i := range result.Moved {
		move := &result.Moved[i]
		if alias := aliases[settingAddress{move.Key, "network", "network", ""}]; alias != "" {
			if value, err := settings.DeliveryShorthandValue(alias, move.Old); err == nil {
				move.Old = value
			}
			if value, err := settings.DeliveryShorthandValue(alias, move.New); err == nil {
				move.New = value
			}
			move.Key = alias
		}
	}
	sort.Slice(result.Moved, func(i, j int) bool {
		if result.Moved[i].Key != result.Moved[j].Key {
			return result.Moved[i].Key < result.Moved[j].Key
		}
		return result.Moved[i].Scope < result.Moved[j].Scope
	})
	all := map[settingAddress]bool{}
	for a := range staged {
		all[a] = true
	}
	for a := range current {
		all[a] = true
	}
	var changed []settingAddress
	for a := range all {
		if !bytes.Equal(staged[a], current[a]) {
			changed = append(changed, a)
		}
	}
	for _, graph := range graphChanges {
		if !containsAddress(changed, graph.address) {
			changed = append(changed, graph.address)
		}
	}
	if len(changed) == 0 {
		return result, nil
	}
	sort.Slice(changed, func(i, j int) bool {
		a, b := changed[i], changed[j]
		if a.key != b.key {
			return a.key < b.key
		}
		if a.level != b.level {
			return a.level < b.level
		}
		return a.scope < b.scope
	})
	if err = bumpSettingsRevision(tx); err != nil {
		return result, err
	}
	result.Revision++
	result.changed = true
	for i, a := range changed {
		old, newval := current[a], staged[a]
		if newval == nil {
			_, err = tx.Exec(`DELETE FROM setting_values WHERE key=? AND level=? AND scope=? AND item=?`, a.key, a.level, a.scope, a.item)
		} else {
			_, err = tx.Exec(`INSERT INTO setting_values(key,level,scope,item,value,revision) VALUES(?,?,?,?,?,?) ON CONFLICT(key,level,scope,item) DO UPDATE SET value=excluded.value,revision=excluded.revision`, a.key, a.level, a.scope, a.item, string(newval), result.Revision)
		}
		if err != nil {
			return result, err
		}
		entryCause := cause
		if !seen[a] {
			entryCause = "parent"
		}
		var ov, nv any
		if old != nil {
			ov = string(old)
		}
		if newval != nil {
			nv = string(newval)
		}
		_, err = tx.Exec(`INSERT INTO setting_history(revision,seq,key,level,scope,item,old_value,new_value,actor,reason,cause,undo_of,at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, result.Revision, i+1, a.key, a.level, a.scope, a.item, ov, nv, actor, reason, entryCause, undoOf, s.now().UnixNano())
		if err != nil {
			return result, err
		}
	}
	for i, graph := range graphChanges {
		e := graph.edge
		old, _ := json.Marshal(e)
		if _, err := tx.Exec(`DELETE FROM graph_edges WHERE source=? AND target=? AND kind=?`, e.From, e.To, e.Kind); err != nil {
			return result, err
		}
		if err := graphRecord(tx, "edge", e.From, e.Kind, string(old), "", actor, s.now().UnixNano()); err != nil {
			return result, err
		}
		if _, err := tx.Exec(`INSERT INTO setting_history(revision,seq,key,level,scope,item,old_value,new_value,actor,reason,cause,undo_of,at)
			VALUES(?,?,?,?,?,'',?,NULL,?,?,?,?,?)`, result.Revision, len(changed)+i+1, deliveryGraphHistoryPrefix+e.Kind,
			"repo", e.To, string(old), actor, reason, cause, undoOf, s.now().UnixNano()); err != nil {
			return result, err
		}
	}
	return result, nil
}

func containsAddress(all []settingAddress, want settingAddress) bool {
	for _, a := range all {
		if a == want {
			return true
		}
	}
	return false
}
func hasLevel(levels []string, level string) bool {
	for _, l := range levels {
		if l == level {
			return true
		}
	}
	return false
}
func (s *Store) UndoSettings(revision int64, actor string) (SettingsResult, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return SettingsResult{}, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT key,level,scope,item,old_value,new_value FROM setting_history WHERE revision=? ORDER BY seq DESC`, revision)
	if err != nil {
		return SettingsResult{}, err
	}
	var changes []SettingsChange
	type graphUndo struct {
		key, scope string
		old, next  sql.NullString
	}
	var graphUndos []graphUndo
	for rows.Next() {
		var c SettingsChange
		var old, newval sql.NullString
		if err = rows.Scan(&c.Key, &c.Level, &c.Scope, &c.Item, &old, &newval); err != nil {
			rows.Close()
			return SettingsResult{}, err
		}
		if strings.HasPrefix(c.Key, deliveryGraphHistoryPrefix) {
			graphUndos = append(graphUndos, graphUndo{key: c.Key, scope: c.Scope, old: old, next: newval})
			continue
		}
		if old.Valid {
			c.Value = json.RawMessage(old.String)
		} else {
			c.Delete = true
		}
		if newval.Valid {
			c.Expect = json.RawMessage(newval.String)
		}
		changes = append(changes, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return SettingsResult{}, err
	}
	if len(changes) == 0 && len(graphUndos) == 0 {
		return SettingsResult{}, fmt.Errorf("no settings history for revision %d", revision)
	}
	result, err := s.writeSettingsTx(tx, changes, actor, fmt.Sprintf("undo revision %d", revision), "undo", &revision)
	if err != nil {
		return result, err
	}
	if len(graphUndos) != 0 && !actorAllowed(actor, "rule") {
		return result, reject("only the owner may undo delivery owners")
	}
	if len(graphUndos) != 0 && !result.changed {
		if err := bumpSettingsRevision(tx); err != nil {
			return result, err
		}
		result.Revision++
		result.changed = true
	}
	var seq int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM setting_history WHERE revision=?`, result.Revision).Scan(&seq); err != nil {
		return result, err
	}
	for _, undo := range graphUndos {
		var e protocol.Edge
		raw := undo.old.String
		if !undo.old.Valid {
			raw = undo.next.String
		}
		if err := json.Unmarshal([]byte(raw), &e); err != nil || e.To != undo.scope || deliveryGraphHistoryPrefix+e.Kind != undo.key {
			return result, reject("invalid delivery owner history")
		}
		var actual protocol.Edge
		actual.To, actual.Kind = e.To, e.Kind
		err := tx.QueryRow(`SELECT source,mode,rule FROM graph_edges WHERE target=? AND kind=?`, e.To, e.Kind).Scan(&actual.From, &actual.Mode, &actual.Rule)
		if err != nil && err != sql.ErrNoRows {
			return result, err
		}
		if undo.next.Valid {
			current, _ := json.Marshal(actual)
			if err != nil || string(current) != undo.next.String {
				return result, reject("delivery owner changed since that settings revision")
			}
		} else if err == nil {
			return result, reject("delivery owner changed since that settings revision")
		}
		if undo.old.Valid {
			if _, err := graphKind(tx, e.To); err != nil {
				return result, err
			}
			if protocol.DeliveryOwnerFromSource(e.From).Kind == protocol.OwnerNode {
				if _, err := graphKind(tx, e.From); err != nil {
					return result, err
				}
			}
			if _, err := tx.Exec(`INSERT INTO graph_edges(source,target,kind,mode,rule) VALUES(?,?,?,?,?)`, e.From, e.To, e.Kind, e.Mode, e.Rule); err != nil {
				return result, err
			}
		} else if _, err := tx.Exec(`DELETE FROM graph_edges WHERE source=? AND target=? AND kind=?`, e.From, e.To, e.Kind); err != nil {
			return result, err
		}
		if err := graphRecord(tx, "edge", e.From, e.Kind, undo.next.String, undo.old.String, actor, s.now().UnixNano()); err != nil {
			return result, err
		}
		seq++
		var old, next any
		if undo.next.Valid {
			old = undo.next.String
		}
		if undo.old.Valid {
			next = undo.old.String
		}
		if _, err := tx.Exec(`INSERT INTO setting_history(revision,seq,key,level,scope,item,old_value,new_value,actor,reason,cause,undo_of,at)
			VALUES(?,?,?,'repo',?,'',?,?,?,?, 'undo',?,?)`, result.Revision, seq, undo.key, undo.scope, old, next, actor,
			fmt.Sprintf("undo revision %d", revision), revision, s.now().UnixNano()); err != nil {
			return result, err
		}
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	if result.changed {
		s.deliverySettingsChanged(changes)
		s.settingsNotify()
	}
	return result, nil
}

// SettingsHistory returns public history, optionally restricted to a key or revision.
func (s *Store) SettingsHistory(key string, revision int64, owner bool) ([]protocol.SettingsHistory, error) {
	canonical := settings.DeliveryCanonicalKey(key)
	rows, err := s.db.Query(`SELECT revision,seq,key,level,scope,item,old_value,new_value,actor,reason,cause,undo_of,at FROM setting_history WHERE (?='' OR key=? OR key=?) AND (?=0 OR revision=?) ORDER BY revision DESC,seq DESC`, key, key, canonical, revision, revision)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.SettingsHistory{}
	recipe := settings.Standard()
	for rows.Next() {
		var h protocol.SettingsHistory
		var old, newval sql.NullString
		var undo sql.NullInt64
		if err := rows.Scan(&h.Revision, &h.Seq, &h.Key, &h.Level, &h.Scope, &h.Item, &old, &newval, &h.Actor, &h.Reason, &h.Cause, &undo, &h.At); err != nil {
			return nil, err
		}
		def, ok := recipe.Definition(h.Key)
		if !ok || (!owner && def.Visibility == "owner") {
			continue
		}
		if old.Valid {
			h.Old = json.RawMessage(old.String)
		}
		if newval.Valid {
			h.New = json.RawMessage(newval.String)
		}
		if canonical != "" && h.Key == canonical {
			h.Key = key
			if h.Old != nil {
				if v, err := settings.DeliveryShorthandValue(key, h.Old); err == nil {
					h.Old = v
				}
			}
			if h.New != nil {
				if v, err := settings.DeliveryShorthandValue(key, h.New); err == nil {
					h.New = v
				}
			}
		}
		if undo.Valid {
			h.UndoOf = &undo.Int64
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
