package gateway

import (
	"database/sql"
	"encoding/json"

	"github.com/niparis/agent-net/internal/settings"
)

// cutoverDeliveryPipelineTx moves explicit legacy shorthand rows to ordered
// pipeline components. A backup retains even invalid legacy values.
func (s *Store) cutoverDeliveryPipelineTx(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT key,level,scope,item,value,revision FROM setting_values WHERE key IN
		('delivery.tests_before_merge','delivery.merge_authority','delivery.deploy_rule') ORDER BY key,level,scope,item`)
	if err != nil {
		return err
	}
	type legacy struct {
		key, level, scope, item, value string
		revision                       int64
	}
	var all []legacy
	for rows.Next() {
		var v legacy
		if err := rows.Scan(&v.key, &v.level, &v.scope, &v.item, &v.value, &v.revision); err != nil {
			rows.Close()
			return err
		}
		all = append(all, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(all) == 0 {
		return err
	}
	if err := bumpSettingsRevision(tx); err != nil {
		return err
	}
	var revision int64
	if err := tx.QueryRow(`SELECT revision FROM setting_revision WHERE id=1`).Scan(&revision); err != nil {
		return err
	}
	seq := 0
	for _, v := range all {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO delivery_settings_legacy(key,level,scope,item,value,revision) VALUES(?,?,?,?,?,?)`,
			v.key, v.level, v.scope, v.item, v.value, v.revision); err != nil {
			return err
		}
		canonical := settings.DeliveryCanonicalKey(v.key)
		value, err := settings.DeliveryCanonicalValue(v.key, json.RawMessage(v.value))
		if err != nil {
			value = json.RawMessage(v.value)
		}
		res, err := tx.Exec(`INSERT OR IGNORE INTO setting_values(key,level,scope,item,value,revision) VALUES(?,?,?,?,?,?)`,
			canonical, v.level, v.scope, v.item, string(value), revision)
		if err != nil {
			return err
		}
		inserted, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if inserted != 0 {
			seq++
			if _, err := tx.Exec(`INSERT INTO setting_history(revision,seq,key,level,scope,item,old_value,new_value,actor,reason,cause,at) VALUES(?,?,?,?,?,?,NULL,?,?,?,'cutover',?)`,
				revision, seq, canonical, v.level, v.scope, v.item, string(value), settingsCutoverActor, "migrate delivery pipeline", s.now().UnixNano()); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`DELETE FROM setting_values WHERE key=? AND level=? AND scope=? AND item=?`, v.key, v.level, v.scope, v.item); err != nil {
			return err
		}
	}
	return nil
}
