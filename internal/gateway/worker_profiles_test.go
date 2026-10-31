package gateway

import (
	"reflect"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestWorkerProfileAvailability(t *testing.T) {
	available := true
	profiles := []protocol.WorkerProfile{
		{Name: "terra pi", Harness: "pi", Model: "openai-codex/gpt-6.1-terra"},
		{Name: "offline", Harness: "codex", Model: "gpt-6.1-sol"},
		{Name: "works", Harness: "opencode", Model: "fake/m1"},
	}
	nodes := []protocol.NodeView{
		{NodeID: "n_pi", Hostname: "pi-host", Online: true, Harnesses: []protocol.HarnessInfo{{Name: "pi", Models: []string{"openai/gpt-6.1-terra", "openai/gpt-6.1-sol", "openai/gpt-6.1-luna", "openai/gpt-6.1"}}}},
		{NodeID: "n_codex", Hostname: "offline-host", Online: false, Harnesses: []protocol.HarnessInfo{{Name: "codex", Models: []string{"gpt-6.1-sol"}}}},
		{NodeID: "n_oc", Hostname: "oc-host", Online: true, Harnesses: []protocol.HarnessInfo{{Name: "opencode", Models: []string{"fake/m1"}}}},
	}
	got := withWorkerProfileAvailability(profiles, nodes)
	if len(got) != 3 {
		t.Fatalf("profiles: %+v", got)
	}
	if got[0].Available == nil || !*got[0].Available || len(got[0].MachineAvailability) != 1 || got[0].MachineAvailability[0].ResolvedModel != "openai/gpt-6.1-terra" {
		t.Errorf("provider prefix should resolve: %+v", got[0])
	}
	if len(got[0].MachinesChecked) != 1 || got[0].MachinesChecked[0] != "pi-host" {
		t.Errorf("profile machine checks: %+v", got[0])
	}
	if got[1].Available != nil || len(got[1].MachinesChecked) != 0 {
		t.Errorf("offline-only harness should be unknown: %+v", got[1])
	}
	if got[2].Available == nil || *got[2].Available != available || len(got[2].Closest) != 0 || got[2].MachinesChecked[0] != "oc-host" {
		t.Errorf("available profile: %+v", got[2])
	}
}

func TestWorkerProfileAvailabilityResolvesAcrossProviders(t *testing.T) {
	profiles := []protocol.WorkerProfile{
		{Name: "terra pi", Harness: "pi", Model: "openai-codex/gpt-6.1-terra"},
		{Name: "other", Harness: "pi", Model: "other/x"},
	}
	nodes := []protocol.NodeView{
		{NodeID: "n_mac", Hostname: "mac", Online: true, Harnesses: []protocol.HarnessInfo{{Name: "pi", Models: []string{"openai-codex/gpt-6.1-terra"}}}},
		{NodeID: "n_research", Hostname: "research", Online: true, Harnesses: []protocol.HarnessInfo{{Name: "pi", Models: []string{"openai/gpt-6.1-terra"}}}},
		{NodeID: "n_ambiguous", Hostname: "ambiguous", Online: true, Harnesses: []protocol.HarnessInfo{{Name: "pi", Models: []string{"openai/x", "azure/x"}}}},
	}

	got := withWorkerProfileAvailability(profiles, nodes)
	if got[0].Available == nil || !*got[0].Available || len(got[0].MachineAvailability) != 3 {
		t.Fatalf("profile should resolve on both provider variants: %+v", got[0])
	}
	if got[0].MachineAvailability[0].Machine != "ambiguous" || got[0].MachineAvailability[0].Available ||
		got[0].MachineAvailability[1].Machine != "mac" || !got[0].MachineAvailability[1].Available ||
		got[0].MachineAvailability[2].Machine != "research" || !got[0].MachineAvailability[2].Available ||
		got[0].MachineAvailability[2].ResolvedModel != "openai/gpt-6.1-terra" {
		t.Fatalf("per-machine provider resolution: %+v", got[0].MachineAvailability)
	}
	if got[1].Available == nil || *got[1].Available || len(got[1].MachineAvailability) != 3 {
		t.Fatalf("ambiguous name should resolve nowhere: %+v", got[1])
	}
	var ambiguous []string
	for _, machine := range got[1].MachineAvailability {
		if machine.Machine == "ambiguous" {
			ambiguous = machine.Ambiguous
		}
	}
	if !reflect.DeepEqual(ambiguous, []string{"azure/x", "openai/x"}) {
		t.Fatalf("ambiguous providers not reported: %v", ambiguous)
	}
}

func TestWorkerProfileRoster(t *testing.T) {
	s, _ := newTestStore(t)
	p := protocol.WorkerProfile{Name: "opencode fake/m1", Harness: "opencode", Model: "fake/m1", AddedBy: "s_test", AddedAt: 123}
	if err := s.AddWorkerProfile(p); err != nil {
		t.Fatal(err)
	}
	profiles, err := s.WorkerProfiles()
	if err != nil || len(profiles) != 1 || !reflect.DeepEqual(profiles[0], p) {
		t.Fatalf("list %+v %v", profiles, err)
	}
	replacement := p
	replacement.Model = "fake/m2"
	if err := s.AddWorkerProfile(replacement); err != nil {
		t.Fatalf("replace: %v", err)
	}
	profiles, err = s.WorkerProfiles()
	if err != nil || len(profiles) != 1 || profiles[0].Model != "fake/m2" {
		t.Fatalf("after replacement %+v %v", profiles, err)
	}
	if err := s.RemoveWorkerProfile(p.Name); err != nil {
		t.Fatal(err)
	}
	profiles, err = s.WorkerProfiles()
	if err != nil || len(profiles) != 0 {
		t.Fatalf("after removal %+v %v", profiles, err)
	}
}
