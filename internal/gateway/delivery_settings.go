package gateway

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

var deliveryNodeKeys = map[string]string{
	protocol.SettingTestsBeforeMerge: "delivery.tests_before_merge",
	protocol.SettingMergeAuthority:   "delivery.merge_authority",
	protocol.SettingJudgeConsumers:   "charter.judge_consumers",
	protocol.SettingDeployRule:       "delivery.deploy_rule",
	protocol.SettingAutonomy:         "charter.autonomy_mode",
}

// cutoverDeliveryTx copies every legacy row, even invalid ones, so the resolver
// can ignore invalid values without destroying the only record of them.
func (s *Store) cutoverDeliveryTx(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT node,setting,value FROM graph_node_settings WHERE setting IN ('tests_before_merge','merge_authority','judge_consumers','deploy_rule','autonomy') ORDER BY node,setting`)
	if err != nil {
		return err
	}
	type row struct{ node, key, value string }
	var all []row
	for rows.Next() {
		var r row
		if err = rows.Scan(&r.node, &r.key, &r.value); err != nil {
			rows.Close()
			return err
		}
		all = append(all, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(all) == 0 {
		return nil
	}
	if err = bumpSettingsRevision(tx); err != nil {
		return err
	}
	var rev int64
	if err = tx.QueryRow(`SELECT revision FROM setting_revision WHERE id=1`).Scan(&rev); err != nil {
		return err
	}
	for i, r := range all {
		key := deliveryNodeKeys[r.key]
		b, _ := json.Marshal(r.value)
		var old sql.NullString
		err = tx.QueryRow(`SELECT value FROM setting_values WHERE key=? AND level='repo' AND scope=? AND item=''`, key, r.node).Scan(&old)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO setting_values(key,level,scope,item,value,revision) VALUES(?,'repo',?,'',?,?) ON CONFLICT(key,level,scope,item) DO UPDATE SET value=excluded.value,revision=excluded.revision`, key, r.node, string(b), rev); err != nil {
			return err
		}
		var oldValue any
		if old.Valid {
			oldValue = old.String
		}
		if _, err = tx.Exec(`INSERT INTO setting_history(revision,seq,key,level,scope,item,old_value,new_value,actor,reason,cause,at) VALUES(?,?,?,'repo',?,'',?,?,?,'migrate node settings','cutover',?)`, rev, i+1, key, r.node, oldValue, string(b), "agent-net cutover", s.now().UnixNano()); err != nil {
			return err
		}
	}
	return nil
}

// nodeEffectiveSettings overlays effective values so legacy graph consumers get
// the same rules even when the source is now a network setting.
func nodeEffectiveSettings(snap settings.Snapshot, name, maturity string, existing map[string]string) map[string]string {
	if existing == nil {
		existing = map[string]string{}
	}
	defaults := protocol.DefaultsForMaturity(maturity)
	base := map[string]string{protocol.SettingTestsBeforeMerge: defaults.TestsBeforeMerge, protocol.SettingMergeAuthority: defaults.MergeAuthority, protocol.SettingJudgeConsumers: defaults.JudgeConsumers, protocol.SettingDeployRule: defaults.DeployRule, protocol.SettingAutonomy: protocol.AutonomyOff}
	for old, key := range deliveryNodeKeys {
		e, err := snap.Resolve(key, settings.Where{Repo: name})
		if err != nil {
			continue
		}
		var value string
		if json.Unmarshal(e.Value, &value) != nil {
			continue
		}
		if _, explicit := existing[old]; explicit || value != base[old] {
			existing[old] = value
		}
	}
	return existing
}

func (s *Store) nodeExplicitSettings(node string) (map[string]string, error) {
	snap, err := s.SettingsSnapshot()
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, v := range snap.Values {
		if v.Level != "repo" || v.Scope != node || v.Item != "" {
			continue
		}
		for old, key := range deliveryNodeKeys {
			if key == v.Key {
				var x string
				if json.Unmarshal(v.Value, &x) == nil && protocol.ValidateNodeSetting(old, x) == nil {
					out[old] = x
				}
			}
		}
	}
	return out, nil
}

func deliveryValue(snap settings.Snapshot, key, repo string) (string, error) {
	e, err := snap.Resolve(key, settings.Where{Repo: repo})
	if err != nil {
		return "", err
	}
	var result string
	if err = json.Unmarshal(e.Value, &result); err != nil {
		return "", fmt.Errorf("%s: %w", key, err)
	}
	return result, nil
}

// removeRepoSettingsTx removes all repo values in the node-removal transaction,
// including malformed values that cannot pass the normal write validator.
func (s *Store) removeRepoSettingsTx(tx *sql.Tx, node, actor string) (bool, error) {
	rows, err := tx.Query(`SELECT key,item,value FROM setting_values WHERE level='repo' AND scope=? ORDER BY key,item`, node)
	if err != nil {
		return false, err
	}
	type row struct{ key, item, value string }
	var all []row
	for rows.Next() {
		var r row
		if err = rows.Scan(&r.key, &r.item, &r.value); err != nil {
			rows.Close()
			return false, err
		}
		all = append(all, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	if len(all) == 0 {
		return false, nil
	}
	if !actorAllowed(actor, "rule") {
		return false, reject("%s cannot remove node settings", actor)
	}
	if err = bumpSettingsRevision(tx); err != nil {
		return false, err
	}
	var rev int64
	if err = tx.QueryRow(`SELECT revision FROM setting_revision WHERE id=1`).Scan(&rev); err != nil {
		return false, err
	}
	for i, r := range all {
		if _, err = tx.Exec(`DELETE FROM setting_values WHERE key=? AND level='repo' AND scope=? AND item=?`, r.key, node, r.item); err != nil {
			return false, err
		}
		if _, err = tx.Exec(`INSERT INTO setting_history(revision,seq,key,level,scope,item,old_value,new_value,actor,reason,cause,at) VALUES(?,?,?,'repo',?,?,?,NULL,?,'remove architect','write',?)`, rev, i+1, r.key, node, r.item, r.value, actor, s.now().UnixNano()); err != nil {
			return false, err
		}
	}
	return true, nil
}
