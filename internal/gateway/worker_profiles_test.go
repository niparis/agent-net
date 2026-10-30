package gateway

import (
	"github.com/niparis/agent-net/internal/protocol"
	"testing"
)

func TestWorkerProfileRoster(t *testing.T) {
	s, _ := newTestStore(t)
	p := protocol.WorkerProfile{Name: "opencode fake/m1", Harness: "opencode", Model: "fake/m1", AddedBy: "s_test", AddedAt: 123}
	if err := s.AddWorkerProfile(p); err != nil {
		t.Fatal(err)
	}
	profiles, err := s.WorkerProfiles()
	if err != nil || len(profiles) != 1 || profiles[0] != p {
		t.Fatalf("list %+v %v", profiles, err)
	}
	if err := s.AddWorkerProfile(p); err == nil {
		t.Fatal("duplicate accepted")
	}
	if err := s.RemoveWorkerProfile(p.Name); err != nil {
		t.Fatal(err)
	}
	profiles, err = s.WorkerProfiles()
	if err != nil || len(profiles) != 0 {
		t.Fatalf("after removal %+v %v", profiles, err)
	}
}
