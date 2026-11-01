package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestOnboardingPersistsAndValidatesUpdates(t *testing.T) {
	path := seededStorePath(t, "gateway.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	disabled := true
	for _, update := range []protocol.OnboardingUpdate{{Step: "see", State: "done"}, {Disabled: &disabled}} {
		if _, err := s.UpdateOnboarding(update, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.UpdateOnboarding(protocol.OnboardingUpdate{Step: "unknown", State: "done"}, false); err == nil {
		t.Fatal("accepted unknown step")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Onboarding()
	if err != nil || got.Steps["see"] != "done" || !got.Disabled {
		t.Fatalf("after reopen: %+v, %v", got, err)
	}
	got, err = s.UpdateOnboarding(protocol.OnboardingUpdate{}, true)
	if err != nil || len(got.Steps) != 0 || got.Disabled {
		t.Fatalf("restart: %+v, %v", got, err)
	}
}
