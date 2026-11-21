package gateway

import (
	"encoding/json"
	"fmt"

	"github.com/niparis/agent-net/internal/protocol"
)

func (s *Store) onboarding() (protocol.Onboarding, error) {
	out := protocol.Onboarding{Steps: map[string]string{}}
	_, err := stateJSON(s.db, "onboarding", &out)
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
		valid := map[string]bool{"see": true, "see-machine": true, "talk": true, "cto": true, "map": true, "work-worker": true, "work-protect": true}
		if !valid[update.Step] || (update.State != "done" && update.State != "skipped") {
			return out, fmt.Errorf("invalid onboarding step or state")
		}
		out.Steps[update.Step] = update.State
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	return out, stateSet(s.db, "onboarding", string(raw), s.now().UnixNano())
}
