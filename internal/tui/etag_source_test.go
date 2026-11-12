package tui

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// changesGateway answers changes_owner_find like the gateway and the daemon do:
// three states, pages of two rows, an ETag for each state's first page, and
// NotModified when the ETag matches. It counts the asks.
type changesGateway struct {
	mu       sync.Mutex
	etags    map[string]string // current ETag by state
	pages    map[string][][]protocol.ChangeView
	asks     int
	laterAsk int
}

func (g *changesGateway) call(_ context.Context, method string, params any) (json.RawMessage, error) {
	a := params.(protocol.ChangesFindArgs)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.asks++
	pages := g.pages[a.State]
	index := 0
	if a.Cursor != "" {
		g.laterAsk++
		index = 1
	} else if a.IfETag != "" && a.IfETag == g.etags[a.State] {
		return json.Marshal(protocol.ChangesFindResult{NotModified: true, ETag: g.etags[a.State]})
	}
	if index >= len(pages) {
		return json.Marshal(protocol.ChangesFindResult{Items: []protocol.ChangeView{}})
	}
	res := protocol.ChangesFindResult{Items: pages[index], ETag: g.etags[a.State]}
	if index+1 < len(pages) {
		res.NextCursor = "next"
	}
	return json.Marshal(res)
}

func changeRow(id string) protocol.ChangeView {
	return protocol.ChangeView{Change: protocol.Change{ID: id, Project: "demo", State: protocol.ChangeOpen, UpdatedAt: 100}}
}

func newChangesGateway() *changesGateway {
	return &changesGateway{
		etags: map[string]string{protocol.ChangeListActive: "a1", protocol.ChangeDone: "d1", protocol.ChangeStopped: "s1"},
		pages: map[string][][]protocol.ChangeView{
			protocol.ChangeListActive: {{changeRow("c1"), changeRow("c2")}, {changeRow("c3")}},
			protocol.ChangeDone:       {{changeRow("c4")}},
			protocol.ChangeStopped:    {},
			"unused":                  nil,
		},
	}
}

// The changes topic moves with every message, but the list does not. A page
// the gateway says is the same is not read again, nor are the pages after it,
// and the view still gets every row.
func TestChangesDoNotRereadPagesTheGatewayCallsUnchanged(t *testing.T) {
	gw := newChangesGateway()
	now := time.Unix(1000, 0)
	src := newGatewayDelivery(gw.call, func() time.Time { return now })
	src.SetFullRead(true)
	first, err := src.Changes(context.Background(), "")
	if err != nil || len(first) != 4 {
		t.Fatalf("first read: %d rows, %v", len(first), err)
	}
	asksAfterFirst, laterAfterFirst := gw.asks, gw.laterAsk
	if laterAfterFirst != 1 {
		t.Fatalf("first read asked for %d later pages, want 1", laterAfterFirst)
	}
	src.SetFullRead(false)
	now = now.Add(5 * time.Second)
	second, err := src.Changes(context.Background(), "")
	if err != nil || len(second) != 4 {
		t.Fatalf("second read: %d rows, %v", len(second), err)
	}
	if got := gw.asks - asksAfterFirst; got != 3 {
		t.Fatalf("an unchanged list took %d asks, want the 3 first pages only", got)
	}
	if gw.laterAsk != laterAfterFirst {
		t.Fatal("an unchanged list read its later pages again")
	}
	// A change on the first page of one state reads that state again, with its
	// later pages, and leaves the others alone.
	gw.mu.Lock()
	gw.etags[protocol.ChangeListActive] = "a2"
	gw.pages[protocol.ChangeListActive][0][0] = changeRow("c9")
	gw.mu.Unlock()
	before, laterBefore := gw.asks, gw.laterAsk
	third, err := src.Changes(context.Background(), "")
	if err != nil || len(third) != 4 || third[0].ID != "c9" {
		t.Fatalf("third read: %+v %v", third, err)
	}
	if gw.asks-before != 4 || gw.laterAsk-laterBefore != 1 {
		t.Fatalf("a changed state took %d asks and %d later pages, want 4 and 1", gw.asks-before, gw.laterAsk-laterBefore)
	}
	// The safety read asks for every page again.
	src.SetFullRead(true)
	before = gw.asks
	if _, err := src.Changes(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if gw.asks-before != 4 {
		t.Fatalf("a full read took %d asks, want 4", gw.asks-before)
	}
}

// How long a change has waited depends on the time of the read, so rows that
// the gateway did not send again are still drawn with the new time.
func TestUnchangedChangesKeepTheirWaitingTime(t *testing.T) {
	gw := newChangesGateway()
	row := changeRow("c1")
	row.Change.UpdatedAt = 1000
	row.Steps = []protocol.StepStatus{{StepRef: protocol.StepRef{Step: protocol.StepAccept}, State: protocol.StepStateWaiting}}
	gw.pages[protocol.ChangeListActive] = [][]protocol.ChangeView{{row}}
	now := time.Unix(2000, 0)
	src := newGatewayDelivery(gw.call, func() time.Time { return now })
	src.SetFullRead(true)
	first, err := src.Changes(context.Background(), "")
	if err != nil || len(first) == 0 {
		t.Fatalf("first read: %v %v", first, err)
	}
	src.SetFullRead(false)
	now = now.Add(time.Hour)
	second, err := src.Changes(context.Background(), "")
	if err != nil || len(second) != len(first) {
		t.Fatalf("second read: %v %v", second, err)
	}
	if second[0].Waiting != first[0].Waiting+time.Hour {
		t.Fatalf("waiting %v then %v, want an hour more", first[0].Waiting, second[0].Waiting)
	}
}

// A changed topic loads the list again at once after a quiet time, and no
// sooner than the gap after the last load on a busy one.
func TestDeliveryTopicLoadsAreSpacedByTheGap(t *testing.T) {
	m := New(&fakeSource{snap: fakeSnapshot()})
	m = m.withDeliverySource(&fakeDelivery{})
	now := time.Unix(1000, 0)
	m.now = func() time.Time { return now }
	m.tab, m.topicActive = sessionsTab, true
	m.topicVersions = map[string]uint64{protocol.TopicChanges: 2}
	m.delivery.topicVersion, m.delivery.topicSeen = 1, true
	m.delivery.loadedAt = now.Add(-time.Second)
	if _, cmd := m.deliveryTick(); cmd != nil {
		t.Fatal("a change one second after the last load read the list again")
	}
	m.delivery.loadedAt = now.Add(-topicRefreshGap)
	if _, cmd := m.deliveryTick(); cmd == nil {
		t.Fatal("a change after the gap did not read the list")
	}
}
