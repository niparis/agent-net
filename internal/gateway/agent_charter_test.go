package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestAgentCharterAndFallback(t *testing.T) {
	s, _ := newTestStore(t)
	_, id := sessionWithDescription(t, s, "fallback description")
	a, ok, err := s.Agent(id)
	if err != nil || !ok || a.Charter.Purpose != "fallback description" || !a.Charter.PurposeFallback {
		t.Fatalf("fallback: %+v %v", a, err)
	}
	if err := s.SetCharter("p", "feature_requests", "ask first", "owner"); err != nil {
		t.Fatal(err)
	}
	a, _, err = s.Agent(id)
	if err != nil || a.Charter.FeatureRequests != "ask first" {
		t.Fatalf("feature_requests: %+v %v", a.Charter, err)
	}
	long := strings.Repeat("é", 310)
	for attr, value := range map[string]string{"purpose": long, "maturity": "mature - " + long, "accepts": long, "refuses": long, "feature_requests": long} {
		if err := s.SetCharter("p", attr, value, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	a, _, err = s.Agent(id)
	if err != nil || a.Charter.Purpose != long || a.Charter.PurposeFallback || a.Charter.Maturity != "mature - "+long || a.Charter.Accepts != long || a.Charter.Refuses != long || a.Charter.FeatureRequests != long {
		t.Fatalf("get: %+v %v", a.Charter, err)
	}
	list, err := s.Agents(protocol.QueryAgents{})
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}
	c := list[0].Charter
	for _, value := range []string{c.Purpose, c.Maturity, c.Accepts, c.Refuses, c.FeatureRequests} {
		if len([]rune(value)) != 300 {
			t.Fatalf("list field length: %q", value)
		}
	}
	g, err := s.Graph()
	if err != nil || len(g.Nodes) != 1 || g.Nodes[0].Charter.FeatureRequests != long {
		t.Fatalf("graph: %+v %v", g, err)
	}
}
