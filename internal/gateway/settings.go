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
func bumpSettingsRevision(tx *sql.Tx) error {
	_, err := tx.Exec(`UPDATE setting_revision SET revision=revision+1 WHERE id=1`)
	return err
}
func (s *Store) SettingsSnapshot() (settings.Snapshot, error) {
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
	out := settings.Snapshot{Recipe: settings.RecipeHash(), Values: []settings.Value{}, Maturity: map[string]string{}}
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
	return out, err
}
func actorAllowed(actor, kind string) bool {
	if actor == "owner" || strings.HasPrefix(actor, "owner (CTO proposal ") && strings.HasSuffix(actor, ")") {
		return true
	}
	return kind == "advice" && (actor == "cto" || actor == "CTO (minor)" || actor == "CTO (owner window)")
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
	recipe := settings.Standard()
	current := map[settingAddress]json.RawMessage{}
	for _, v := range before.Values {
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
		if c.Expect != nil {
			expect, err = d.Codec(c.Expect)
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
			}
			return result, reject("setting %s at %s %s changed (current %s)", c.Key, c.Level, c.Scope, currentValue)
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
	makeSnapshot := func(vals map[settingAddress]json.RawMessage) settings.Snapshot {
		snap := settings.Snapshot{Revision: before.Revision, Maturity: before.Maturity}
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
		if d.Kind != "rule" || staged[settingAddress{c.Key, "network", "network", ""}] == nil {
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
		if d.Kind != "rule" || !hasLevel(d.Levels, "repo") {
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
 UNION SELECT repo_project FROM sessions WHERE graph_excluded<>1 AND repo_project<>''`)
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
	return result, nil
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
	for rows.Next() {
		var c SettingsChange
		var old, newval sql.NullString
		if err = rows.Scan(&c.Key, &c.Level, &c.Scope, &c.Item, &old, &newval); err != nil {
			rows.Close()
			return SettingsResult{}, err
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
	if len(changes) == 0 {
		return SettingsResult{}, fmt.Errorf("no settings history for revision %d", revision)
	}
	result, err := s.writeSettingsTx(tx, changes, actor, fmt.Sprintf("undo revision %d", revision), "undo", &revision)
	if err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	if result.changed {
		s.settingsNotify()
	}
	return result, nil
}

// SettingsHistory returns public history, optionally restricted to a key or revision.
func (s *Store) SettingsHistory(key string, revision int64, owner bool) ([]protocol.SettingsHistory, error) {
	rows, err := s.db.Query(`SELECT revision,seq,key,level,scope,item,old_value,new_value,actor,reason,cause,undo_of,at FROM setting_history WHERE (?='' OR key=?) AND (?=0 OR revision=?) ORDER BY revision DESC,seq DESC`, key, key, revision, revision)
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
		if undo.Valid {
			h.UndoOf = &undo.Int64
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
