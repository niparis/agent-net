package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/niparis/agent-net/internal/protocol"
)

const qualitySettingsKey = "quality_weighting"

func (s *Store) qualitySettings() (protocol.QualitySettings, error) {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM owner_settings WHERE key = ?`, qualitySettingsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.QualitySettings{}, nil
	}
	if err != nil {
		return protocol.QualitySettings{}, err
	}
	var out protocol.QualitySettings
	err = json.Unmarshal([]byte(raw), &out)
	return out, err
}
func (g *Gateway) handleQualitySettings(w http.ResponseWriter, r *http.Request) {
	node, ok := g.nodeAuthed(r)
	if !ok {
		http.Error(w, "unauthorized", 401)
		return
	}
	if r.Method == http.MethodGet {
		out, err := g.store.qualitySettings()
		writeJSON(w, out, err)
		return
	}
	var in protocol.QualitySettings
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		http.Error(w, "bad quality settings", 400)
		return
	}
	raw, err := json.Marshal(in)
	if err == nil {
		_, err = g.store.db.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, qualitySettingsKey, string(raw), g.store.now().UnixNano())
	}
	if err == nil {
		g.store.Event("quality.set", node, "", "", "")
	}
	writeJSON(w, in, err)
}
