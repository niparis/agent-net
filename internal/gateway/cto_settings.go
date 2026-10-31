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
		return protocol.CTOSettings{Harness: "opencode", Compaction: protocol.CTOCompactionSettings{}.Normalized()}, nil
	}
	if err != nil {
		return protocol.CTOSettings{}, err
	}
	var settings protocol.CTOSettings
	if err = json.Unmarshal([]byte(raw), &settings); err != nil {
		return protocol.CTOSettings{}, err
	}
	if settings.Harness == "" {
		settings.Harness = "opencode"
	}
	settings.Compaction = settings.Compaction.Normalized()
	return settings, nil
}

func (s *Store) SetCTOSettings(in protocol.CTOSettings) error {
	in.Compaction = in.Compaction.Normalized()
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO owner_settings (key, value, updated_at) VALUES (?,?,?)
 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, ctoSettingsKey, string(b), s.now().UnixNano())
	return err
}

// validCompaction rejects values the daemon cannot use. Zero means the
// default, so only values outside the usable ranges are errors.
func validCompaction(c protocol.CTOCompactionSettings) bool {
	if c.ThresholdPercent != 0 && (c.ThresholdPercent < 10 || c.ThresholdPercent > 100) {
		return false
	}
	if c.FloorTokens < 0 || c.FloorTokens > 10_000_000 {
		return false
	}
	if c.GrowthTokens < 0 || c.GrowthTokens > 10_000_000 {
		return false
	}
	if len(c.CacheWindowSeconds) > 32 {
		return false
	}
	for provider, seconds := range c.CacheWindowSeconds {
		if provider == "" || len(provider) > 64 || strings.IndexFunc(provider, unicode.IsControl) >= 0 {
			return false
		}
		if seconds < 1 || seconds > 24*60*60 {
			return false
		}
	}
	return true
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
	var in protocol.CTOSettingsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		http.Error(w, "bad settings", http.StatusBadRequest)
		return
	}
	if in.Model != nil && (len(*in.Model) > 256 || strings.IndexFunc(*in.Model, unicode.IsControl) >= 0) {
		http.Error(w, "bad model", http.StatusBadRequest)
		return
	}
	if in.Harness != nil && *in.Harness != "opencode" && *in.Harness != "claude" {
		http.Error(w, "bad harness", http.StatusBadRequest)
		return
	}
	if in.Compaction != nil && !validCompaction(*in.Compaction) {
		http.Error(w, "bad compaction settings", http.StatusBadRequest)
		return
	}
	if in.Model == nil && in.Harness == nil && in.Compaction == nil {
		http.Error(w, "nothing to change", http.StatusBadRequest)
		return
	}
	current, err := g.store.CTOSettings()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	if in.Model != nil {
		current.Model = *in.Model
	}
	if in.Harness != nil {
		current.Harness = *in.Harness
	}
	if in.Compaction != nil {
		current.Compaction = *in.Compaction
	}
	if err := g.store.SetCTOSettings(current); err != nil {
		writeJSON(w, nil, err)
		return
	}
	g.store.Event("cto_settings.set", node, "", "", "")
	out, err := g.store.CTOSettings()
	writeJSON(w, out, err)
}
