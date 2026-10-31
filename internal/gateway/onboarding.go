package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/niparis/agent-net/internal/protocol"
)

func (s *Store) onboarding() (protocol.Onboarding, error) {
	out := protocol.Onboarding{Steps: map[string]string{}}
	var raw string
	err := s.db.QueryRow(`SELECT value FROM owner_settings WHERE key = ?`, "onboarding").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	err = json.Unmarshal([]byte(raw), &out)
	if out.Steps == nil {
		out.Steps = map[string]string{}
	}
	return out, err
}

func (s *Store) Onboarding() (protocol.Onboarding, error) {
	s.hiddenMu.Lock()
	defer s.hiddenMu.Unlock()
	return s.onboarding()
}

func (s *Store) UpdateOnboarding(update protocol.OnboardingUpdate, restart bool) (protocol.Onboarding, error) {
	s.hiddenMu.Lock()
	defer s.hiddenMu.Unlock()
	out, err := s.onboarding()
	if err != nil {
		return out, err
	}
	if restart {
		out.Steps = map[string]string{}
		out.Disabled = false
	} else if update.Disabled != nil {
		if update.Step != "" || update.State != "" {
			return out, fmt.Errorf("invalid onboarding update")
		}
		out.Disabled = *update.Disabled
	} else {
		valid := map[string]bool{"see": true, "see-machine": true, "talk": true, "cto": true, "map": true, "work-worker": true, "work-protect": true, "tools": true}
		if !valid[update.Step] || (update.State != "done" && update.State != "skipped") {
			return out, fmt.Errorf("invalid onboarding step or state")
		}
		out.Steps[update.Step] = update.State
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	_, err = s.db.Exec(`INSERT INTO owner_settings (key,value,updated_at) VALUES (?,?,?) ON CONFLICT (key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, "onboarding", string(raw), s.now().UnixNano())
	return out, err
}
