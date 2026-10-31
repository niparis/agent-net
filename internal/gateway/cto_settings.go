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
const ctoTitleKey = "cto_title"

// CTOTitle reads the owner-facing CTO name. A missing row means no owner has
// been asked yet: the title is the default and Set is false.
func (s *Store) CTOTitle() (protocol.CTOTitle, error) {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM owner_settings WHERE key = ?`, ctoTitleKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.CTOTitle{Title: protocol.DefaultCTOTitle}, nil
	}
	if err != nil {
		return protocol.CTOTitle{}, err
	}
	title, err := protocol.NormalizeCTOTitle(raw)
	if err != nil || title == "" {
		title = protocol.DefaultCTOTitle
	}
	return protocol.CTOTitle{Title: title, Set: true}, nil
}

// SetCTOTitle saves the owner-facing name for every machine. An empty value
// stores the default, so a later setup on this gateway does not ask again.
func (s *Store) SetCTOTitle(raw string) error {
	title, err := protocol.NormalizeCTOTitle(raw)
	if err != nil {
		return err
	}
	if title == "" {
		title = protocol.DefaultCTOTitle
	}
	_, err = s.db.Exec(`INSERT INTO owner_settings (key, value, updated_at) VALUES (?,?,?)
 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, ctoTitleKey, title, s.now().UnixNano())
	return err
}

func (s *Store) CTOSettings() (protocol.CTOSettings, error) {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM owner_settings WHERE key = ?`, ctoSettingsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		title, terr := s.CTOTitle()
		if terr != nil {
			return protocol.CTOSettings{}, terr
		}
		return protocol.CTOSettings{Title: title.Title, Harness: "opencode", Compaction: protocol.CTOCompactionSettings{}.Normalized()}, nil
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
	title, err := s.CTOTitle()
	if err != nil {
		return protocol.CTOSettings{}, err
	}
	settings.Title = title.Title
	return settings, nil
}

func (s *Store) SetCTOSettings(in protocol.CTOSettings) error {
	in.Compaction = in.Compaction.Normalized()
	title := in.Title
	in.Title = ""
	if err := s.SetCTOTitle(title); err != nil {
		return err
	}
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
	if in.Title != nil {
		title, err := protocol.NormalizeCTOTitle(*in.Title)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		*in.Title = title
	}
	if in.Model == nil && in.Harness == nil && in.Compaction == nil && in.Title == nil {
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
	if in.Title != nil {
		current.Title = protocol.CTOTitleOrDefault(*in.Title)
	}
	if err := g.store.SetCTOSettings(current); err != nil {
		writeJSON(w, nil, err)
		return
	}
	g.store.Event("cto_settings.set", node, "", "", "")
	out, err := g.store.CTOSettings()
	writeJSON(w, out, err)
}

func (g *Gateway) handleCTOTitleGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.nodeAuthed(r); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	out, err := g.store.CTOTitle()
	writeJSON(w, out, err)
}

func (g *Gateway) handleCTOTitleSet(w http.ResponseWriter, r *http.Request) {
	node, ok := g.nodeAuthed(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var in protocol.CTOTitle
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		http.Error(w, "bad title", http.StatusBadRequest)
		return
	}
	if err := g.store.SetCTOTitle(in.Title); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	g.store.Event("cto_title.set", node, "", "", "")
	out, err := g.store.CTOTitle()
	writeJSON(w, out, err)
}
