package daemon

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/niparis/agent-net/internal/settings"
)

// SettingsSnapshot returns a detached view. Call once per unit of work.
func (d *Daemon) SettingsSnapshot() settings.Snapshot {
	if p := d.settingsSnapshot.Load(); p != nil {
		return p.Clone()
	}
	return settings.Snapshot{Recipe: settings.RecipeHash(), Maturity: map[string]string{}}
}
func (d *Daemon) loadSettingsSnapshot() error {
	var raw string
	err := d.st.db.QueryRow(`SELECT payload FROM settings_snapshot WHERE id=1`).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	snap, err := d.cleanSettingsSnapshot([]byte(raw))
	if err != nil {
		if d.log != nil {
			d.log.Printf("settings snapshot: %v", err)
		}
		return nil
	}
	d.settingsSnapshot.Store(&snap)
	return nil
}
func (d *Daemon) cleanSettingsSnapshot(raw []byte) (settings.Snapshot, error) {
	var wire struct {
		Revision *int64            `json:"revision"`
		Recipe   string            `json:"recipe"`
		Values   []settings.Value  `json:"values"`
		Maturity map[string]string `json:"maturity"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return settings.Snapshot{}, err
	}
	if wire.Revision == nil || *wire.Revision < 0 {
		return settings.Snapshot{}, fmt.Errorf("invalid revision")
	}
	snap := settings.Snapshot{Revision: *wire.Revision, Recipe: wire.Recipe, Values: []settings.Value{}, Maturity: wire.Maturity}
	recipe := settings.Standard()
	seen := map[string]bool{}
	for _, v := range wire.Values {
		def, ok := recipe.Definition(v.Key)
		allowed := false
		for _, level := range def.Levels {
			if level == v.Level {
				allowed = true
			}
		}
		id := v.Key + "\x00" + v.Level + "\x00" + v.Scope + "\x00" + v.Item
		if !ok || !allowed || seen[id] || v.Item != "" || (v.Level != "network" && v.Level != "repo") || (v.Level == "network" && v.Scope != "network") || (v.Level == "repo" && v.Scope == "") {
			if d.log != nil {
				d.log.Printf("settings: ignoring unknown or invalid address %s", v.Key)
			}
			continue
		}
		b, err := def.Codec(v.Value)
		if err != nil {
			if d.log != nil {
				d.log.Printf("settings: ignoring invalid %s: %v", v.Key, err)
			}
			continue
		}
		v.Value = b
		seen[id] = true
		snap.Values = append(snap.Values, v)
	}
	return snap, nil
}
func (d *Daemon) acceptSettingsSnapshot(raw []byte, first bool) error {
	snap, err := d.cleanSettingsSnapshot(raw)
	if err != nil {
		return err
	}
	d.settingsMu.Lock()
	defer d.settingsMu.Unlock()
	if old := d.settingsSnapshot.Load(); old != nil && !first && snap.Revision < old.Revision {
		return nil
	}
	b, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	_, err = d.st.db.Exec(`INSERT INTO settings_snapshot(id,revision,payload,received_at) VALUES(1,?,?,?) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision,payload=excluded.payload,received_at=excluded.received_at`, snap.Revision, string(b), time.Now().UnixNano())
	if err != nil {
		return err
	}
	d.settingsSnapshot.Store(&snap)
	return nil
}
