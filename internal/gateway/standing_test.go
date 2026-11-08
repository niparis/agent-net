package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestStandingOrdersStore(t *testing.T) {
	s, _ := newTestStore(t)
	a, err := s.SaveStandingOrder(protocol.StandingOrdersRequest{Text: "  do it  ", Source: protocol.StandingSourceCTO})
	if err != nil || a.Text != "do it" || a.Source != protocol.StandingSourceCTO || a.SavedAt == 0 || a.ID == "" {
		t.Fatalf("save: %+v %v", a, err)
	}
	b, err := s.SaveStandingOrder(protocol.StandingOrdersRequest{Text: "second"})
	if err != nil || b.Source != protocol.StandingSourceOwner {
		t.Fatalf("source defaults to owner: %+v %v", b, err)
	}
	list, _ := s.StandingOrders()
	if len(list) != 2 {
		t.Fatalf("list %+v", list)
	}
	removed, err := s.RemoveStandingOrder(a.ID)
	if err != nil || removed.Text != "do it" {
		t.Fatalf("remove: %+v %v", removed, err)
	}
	if _, err := s.RemoveStandingOrder(a.ID); err == nil {
		t.Fatal("removing twice should fail")
	}
	back, err := s.SaveStandingOrder(protocol.StandingOrdersRequest{Restore: removed})
	if err != nil || back.ID != a.ID || back.SavedAt != a.SavedAt || back.Source != protocol.StandingSourceCTO {
		t.Fatalf("restore keeps id, time and source: %+v %v", back, err)
	}
	if _, err := s.SaveStandingOrder(protocol.StandingOrdersRequest{Text: " "}); err == nil {
		t.Fatal("empty text must fail")
	}
	if _, err := s.SaveStandingOrder(protocol.StandingOrdersRequest{Text: strings.Repeat("x", 2001)}); err == nil {
		t.Fatal("long text must fail")
	}
}
