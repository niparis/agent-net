package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
)

const (
	hiddenItemsKey       = "hidden_items"
	keepHiddenSessionFor = 30 * 24 * time.Hour
	maxHiddenItems       = 10000
	maxHiddenItemLength  = 256
)

// HiddenItems reads the owner's network-wide TUI visibility preference.
// Entries for sessions that have ended and stayed offline for 30 days are
// removed; an online session keeps its entry regardless of its age.
func (s *Store) HiddenItems() (protocol.HiddenItems, error) {
	s.hiddenMu.Lock()
	defer s.hiddenMu.Unlock()

	var raw string
	err := s.db.QueryRow(`SELECT value FROM owner_settings WHERE key = ?`, hiddenItemsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.HiddenItems{}, nil
	}
	if err != nil {
		return protocol.HiddenItems{}, err
	}
	var items protocol.HiddenItems
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return protocol.HiddenItems{}, err
	}
	items, err = cleanHiddenItems(items)
	if err != nil {
		return protocol.HiddenItems{}, err
	}

	rows, err := s.db.Query(`SELECT id, online, last_seen FROM sessions WHERE harness <> ?`, protocol.OwnerHarness)
	if err != nil {
		return protocol.HiddenItems{}, err
	}
	type sessionState struct {
		online   bool
		lastSeen int64
	}
	known := make(map[string]sessionState, len(items.Sessions))
	wanted := make(map[string]bool, len(items.Sessions))
	for _, id := range items.Sessions {
		wanted[id] = true
	}
	for rows.Next() {
		var id string
		var online int
		var lastSeen int64
		if err := rows.Scan(&id, &online, &lastSeen); err != nil {
			rows.Close()
			return protocol.HiddenItems{}, err
		}
		if wanted[id] {
			known[id] = sessionState{online: online != 0, lastSeen: lastSeen}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return protocol.HiddenItems{}, err
	}
	rows.Close()

	cut := s.now().Add(-keepHiddenSessionFor).UnixNano()
	kept := items.Sessions[:0]
	for _, id := range items.Sessions {
		state, ok := known[id]
		if !ok || !state.online && state.lastSeen < cut {
			continue
		}
		kept = append(kept, id)
	}
	if len(kept) != len(items.Sessions) {
		items.Sessions = kept
		if err := s.writeHiddenItems(items); err != nil {
			return protocol.HiddenItems{}, err
		}
	}
	return items, nil
}

// SetHiddenItems replaces the owner's network-wide TUI visibility preference.
func (s *Store) SetHiddenItems(items protocol.HiddenItems) (protocol.HiddenItems, error) {
	clean, err := cleanHiddenItems(items)
	if err != nil {
		return protocol.HiddenItems{}, err
	}
	s.hiddenMu.Lock()
	defer s.hiddenMu.Unlock()
	if err := s.writeHiddenItems(clean); err != nil {
		return protocol.HiddenItems{}, err
	}
	return clean, nil
}

func (s *Store) writeHiddenItems(items protocol.HiddenItems) error {
	raw, err := json.Marshal(items)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO owner_settings (key, value, updated_at) VALUES (?,?,?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		hiddenItemsKey, string(raw), s.now().UnixNano())
	return err
}

func cleanHiddenItems(items protocol.HiddenItems) (protocol.HiddenItems, error) {
	cleanList := func(kind string, in []string) ([]string, error) {
		if len(in) > maxHiddenItems {
			return nil, fmt.Errorf("too many hidden %s", kind)
		}
		seen := make(map[string]bool, len(in))
		out := make([]string, 0, len(in))
		for _, value := range in {
			value = strings.TrimSpace(value)
			if value == "" || len(value) > maxHiddenItemLength || strings.IndexFunc(value, unicode.IsControl) >= 0 {
				return nil, fmt.Errorf("invalid hidden %s entry", strings.TrimSuffix(kind, "s"))
			}
			if !seen[value] {
				seen[value] = true
				out = append(out, value)
			}
		}
		sort.Strings(out)
		return out, nil
	}
	var err error
	items.Sessions, err = cleanList("sessions", items.Sessions)
	if err != nil {
		return protocol.HiddenItems{}, err
	}
	items.Nodes, err = cleanList("nodes", items.Nodes)
	if err != nil {
		return protocol.HiddenItems{}, err
	}
	return items, nil
}
