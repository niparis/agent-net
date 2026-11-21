package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
)

// The owner's own values (integrations, profiles, scores, floors, the CTO title
// and so on) are network settings under "owner." (see standard.yaml). A read is
// one point lookup; a write goes through the settings path, so it has history
// and undo. The snapshot sent to the machines leaves them out.
const ownerKeyPrefix = "owner."

type rower interface{ QueryRow(string, ...any) *sql.Row }

// ownerRaw reads the stored JSON of an owner value. ok is false when the owner
// never set it.
func ownerRaw(q rower, key string) (raw string, ok bool, err error) {
	err = q.QueryRow(`SELECT value FROM setting_values WHERE key=? AND level='network' AND scope='network' AND item=''`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return raw, err == nil, err
}

// ownerJSON and stateJSON decode a stored value into v, and say whether one was
// stored. v is left alone when none was.
func ownerJSON(q rower, key string, v any) (bool, error) {
	raw, ok, err := ownerRaw(q, key)
	return decodeStored(raw, ok, err, v)
}

func stateJSON(q rower, key string, v any) (bool, error) {
	raw, ok, err := stateGet(q, key)
	return decodeStored(raw, ok, err, v)
}

func decodeStored(raw string, ok bool, err error, v any) (bool, error) {
	if err != nil || !ok {
		return false, err
	}
	return true, json.Unmarshal([]byte(raw), v)
}

// setOwner stores an owner value, encoded as JSON, as one settings revision. A
// value that equals the stored one writes nothing. A nil value removes it, so
// the owner is "not set" again.
func (s *Store) setOwner(key string, v any) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	c := SettingsChange{Key: key, Level: "network", Scope: "network", Delete: v == nil}
	old, had, err := ownerRaw(tx, key)
	if err != nil || (v == nil && !had) {
		return err
	}
	if had {
		c.Expect = json.RawMessage(old)
	}
	if v != nil {
		if c.Value, err = json.Marshal(v); err != nil {
			return err
		}
	}
	res, err := s.writeSettingsTx(tx, []SettingsChange{c}, "owner", "owner setting", "write", nil)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if res.changed {
		s.settingsNotify()
	}
	return nil
}
