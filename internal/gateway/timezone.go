package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/niparis/agent-net/internal/protocol"
)

const timeZoneSettingsKey = "time_zone"

// timeZoneSetting reads the owner's display time zone. The default is auto:
// the zone of the owner's own machine.
func (s *Store) timeZoneSetting() (protocol.TimeZoneSettings, error) {
	var name string
	err := s.db.QueryRow(`SELECT value FROM owner_settings WHERE key = ?`, timeZoneSettingsKey).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.TimeZoneSettings{Name: protocol.TimeZoneAuto}, nil
	}
	if err != nil {
		return protocol.TimeZoneSettings{}, err
	}
	clean, err := protocol.NormalizeTimeZone(name)
	if err != nil {
		// A value written by hand and not a zone name means auto.
		return protocol.TimeZoneSettings{Name: protocol.TimeZoneAuto}, nil
	}
	return protocol.TimeZoneSettings{Name: clean}, nil
}

func (g *Gateway) handleTimeZoneSettings(w http.ResponseWriter, r *http.Request) {
	node, ok := g.nodeAuthed(r)
	if !ok {
		http.Error(w, "unauthorized", 401)
		return
	}
	if r.Method == http.MethodGet {
		out, err := g.store.timeZoneSetting()
		writeJSON(w, out, err)
		return
	}
	var in protocol.TimeZoneSettings
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		http.Error(w, "bad time zone setting", 400)
		return
	}
	name, err := protocol.NormalizeTimeZone(in.Name)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	_, err = g.store.db.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, timeZoneSettingsKey, name, g.store.now().UnixNano())
	if err == nil {
		g.store.Event("timezone.set", node, "", "", name)
	}
	writeJSON(w, protocol.TimeZoneSettings{Name: name}, err)
}
