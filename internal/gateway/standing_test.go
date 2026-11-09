package gateway

import (
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
}

func TestStandingSearchHistory(t *testing.T) {
	s, _ := newTestStore(t)
	a, err := s.SaveStandingOrder(protocol.StandingOrdersRequest{Text: "workers coordinate workers", Tags: []string{"teams"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveStandingOrder(protocol.StandingOrdersRequest{Text: "workers with a long unrelated instruction about disk space and other topics", Tags: []string{"disk"}}); err != nil {
		t.Fatal(err)
	}
	hits, err := s.SearchStandingOrders(protocol.StandingOrdersRequest{Query: "workers", Tag: "teams"})
	if err != nil || len(hits) != 1 || hits[0].ID != a.ID || hits[0].Snippet == "" {
		t.Fatalf("%+v %v", hits, err)
	}
	all, err := s.SearchStandingOrders(protocol.StandingOrdersRequest{Query: "workers"})
	if err != nil || len(all) != 2 || all[0].ID != a.ID {
		t.Fatalf("rank: %+v %v", all, err)
	}
	_, err = s.UpdateStandingOrder(protocol.StandingOrdersRequest{ID: a.ID, Text: "updated"})
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.StandingOrderHistory(a.ID)
	if err != nil || len(h) != 1 || h[0].Text != a.Text {
		t.Fatalf("%+v %v", h, err)
	}
}

func TestStandingOrdersOldMigration(t *testing.T) {
	s, _ := newTestStore(t)
	_, err := s.db.Exec(`DROP TRIGGER standing_orders_ai; DROP TRIGGER standing_orders_ad; DROP TRIGGER standing_orders_au;
 DROP TABLE standing_orders_fts; DROP TABLE standing_order_history;
 ALTER TABLE standing_orders DROP COLUMN tags;
 ALTER TABLE standing_orders DROP COLUMN pinned;
 ALTER TABLE standing_orders DROP COLUMN updated_at;
 ALTER TABLE standing_orders DROP COLUMN kind;
 ALTER TABLE standing_orders DROP COLUMN area;
 ALTER TABLE standing_orders DROP COLUMN ref;
 INSERT INTO standing_orders VALUES('old','legacy workers',1,'owner');`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE schema_version SET version=?", len(migrations)-2); err != nil {
		t.Fatal(err)
	}
	if err = s.migrate(); err != nil {
		t.Fatal(err)
	}
	hits, err := s.SearchStandingOrders(protocol.StandingOrdersRequest{Query: "legacy"})
	if err != nil || len(hits) != 1 || hits[0].ID != "old" {
		t.Fatalf("%+v %v", hits, err)
	}
}
