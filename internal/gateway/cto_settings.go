package gateway

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"unicode"

	"github.com/niparis/agent-net/internal/harnesses"
	"github.com/niparis/agent-net/internal/protocol"
)

const ctoSettingsKey = "owner.cto_settings"
const ctoTitleKey = "owner.cto_title"

// CTOTitle reads the owner-facing CTO name. A missing row means no owner has
// been asked yet: the title is the default and Set is false.
func (s *Store) CTOTitle() (protocol.CTOTitle, error) {
	var stored string
	set, err := ownerJSON(s.db, ctoTitleKey, &stored)
	if err != nil {
		return protocol.CTOTitle{}, err
	}
	if !set {
		return protocol.CTOTitle{Title: protocol.DefaultCTOTitle}, nil
	}
	title, err := protocol.NormalizeCTOTitle(stored)
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
	return s.setOwner(ctoTitleKey, title)
}

func (s *Store) CTOSettings() (protocol.CTOSettings, error) {
	var settings protocol.CTOSettings
	if _, err := ownerJSON(s.db, ctoSettingsKey, &settings); err != nil {
		return protocol.CTOSettings{}, err
	}
	if settings.Harness == "" {
		settings.Harness = "opencode"
	}
	title, err := s.CTOTitle()
	if err != nil {
		return protocol.CTOSettings{}, err
	}
	settings.Title = title.Title
	return settings, nil
}

func (s *Store) SetCTOSettings(in protocol.CTOSettings) error {
	title := in.Title
	in.Title = ""
	if err := s.SetCTOTitle(title); err != nil {
		return err
	}
	return s.setOwner(ctoSettingsKey, in)
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
	if in.Harness != nil && !slices.Contains(harnesses.Names, *in.Harness) {
		http.Error(w, "bad harness", http.StatusBadRequest)
		return
	}
	if in.Models != nil {
		if len(*in.Models) > 8 {
			http.Error(w, "too many CTO models", http.StatusBadRequest)
			return
		}
		for _, m := range *in.Models {
			if m.IsZero() || !slices.Contains(harnesses.Names, m.Harness) {
				http.Error(w, "bad CTO model entry", http.StatusBadRequest)
				return
			}
			if strings.IndexFunc(m.Harness+m.Provider+m.Model+m.Effort, unicode.IsControl) >= 0 {
				http.Error(w, "bad CTO model entry", http.StatusBadRequest)
				return
			}
		}
	}
	if in.Title != nil {
		title, err := protocol.NormalizeCTOTitle(*in.Title)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		*in.Title = title
	}
	if in.Model == nil && in.Harness == nil && in.Title == nil && in.Models == nil {
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
	if in.Models != nil {
		current.Models = *in.Models
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
