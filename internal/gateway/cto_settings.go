package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
)

const ctoSettingsKey = "cto_settings"

func (s *Store) CTOSettings() (protocol.CTOSettings, error) {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM owner_settings WHERE key = ?`, ctoSettingsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.CTOSettings{Harness: "opencode"}, nil
	}
	if err != nil {
		return protocol.CTOSettings{}, err
	}
	var settings protocol.CTOSettings
	err = json.Unmarshal([]byte(raw), &settings)
	if settings.Harness == "" {
		settings.Harness = "opencode"
	}
	return settings, err
}

func (s *Store) SetCTOSettings(in protocol.CTOSettings) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO owner_settings (key, value, updated_at) VALUES (?,?,?)
 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, ctoSettingsKey, string(b), s.now().UnixNano())
	return err
}

func (g *Gateway) handleCTOSettingsGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.nodeAuthed(r); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	out, err := g.store.CTOSettings()
	writeJSON(w, out, err)
}

func (g *Gateway) handleCTOSettingsSet(w http.ResponseWriter, r *http.Request) {
	node, ok := g.nodeAuthed(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var in protocol.CTOSettings
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil || len(in.Model) > 256 || strings.IndexFunc(in.Model, unicode.IsControl) >= 0 {
		http.Error(w, "bad model", http.StatusBadRequest)
		return
	}
	if in.Harness == "" {
		in.Harness = "opencode"
	}
	if in.Harness != "opencode" && in.Harness != "claude" {
		http.Error(w, "bad harness", http.StatusBadRequest)
		return
	}
	if err := g.store.SetCTOSettings(in); err != nil {
		writeJSON(w, nil, err)
		return
	}
	g.store.Event("cto_settings.set", node, "", "", "")
	writeJSON(w, in, nil)
}
