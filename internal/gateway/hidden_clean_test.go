package gateway

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The preference is cleaned before it is stored: trimmed, de-duplicated and
// sorted, and bounded in size, so a bad or huge request cannot fill the store.
func TestHiddenItemsAreCleanedAndBounded(t *testing.T) {
	s, _ := newTestStore(t)
	got, err := s.SetHiddenItems(protocol.HiddenItems{Sessions: []string{"s_b", "  s_a ", "s_b"}, Nodes: []string{" n_2", "n_1", "n_2 "}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Sessions, []string{"s_a", "s_b"}) || !reflect.DeepEqual(got.Nodes, []string{"n_1", "n_2"}) {
		t.Fatalf("not trimmed, de-duplicated and sorted: %+v", got)
	}
	if _, err = s.SetHiddenItems(protocol.HiddenItems{Sessions: []string{strings.Repeat("x", maxHiddenItemLength)}}); err != nil {
		t.Fatalf("an entry at the limit was refused: %v", err)
	}
	bad := map[string]protocol.HiddenItems{
		"too long":   {Sessions: []string{strings.Repeat("x", maxHiddenItemLength+1)}},
		"control":    {Nodes: []string{"a\x00b"}},
		"newline":    {Nodes: []string{"a\nb"}},
		"empty":      {Sessions: []string{"   "}},
		"too many":   {Sessions: manyIDs(maxHiddenItems + 1)},
		"many nodes": {Nodes: manyIDs(maxHiddenItems + 1)},
	}
	for name, items := range bad {
		if _, err := s.SetHiddenItems(items); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := s.SetHiddenItems(protocol.HiddenItems{Sessions: manyIDs(maxHiddenItems)}); err != nil {
		t.Fatalf("a list at the limit was refused: %v", err)
	}
	// A refused request keeps the stored preference.
	if _, err := s.SetHiddenItems(protocol.HiddenItems{Nodes: []string{"keep"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetHiddenItems(protocol.HiddenItems{Nodes: []string{"a\x00"}}); err == nil {
		t.Fatal("control character accepted")
	}
	if cur, err := s.HiddenItems(); err != nil || !reflect.DeepEqual(cur.Nodes, []string{"keep"}) {
		t.Fatalf("stored preference changed by a refused request: %+v %v", cur, err)
	}
}

func manyIDs(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "s_" + strconv.Itoa(i)
	}
	return out
}

// An online session keeps its hidden entry however long ago it was last seen.
func TestHiddenOnlineSessionKeepsItsEntryWhateverItsAge(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("fake-host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	sid, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "long-lived"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE sessions SET online=1, last_seen=? WHERE id=?`, now.Add(-90*24*time.Hour).UnixNano(), sid); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetHiddenItems(protocol.HiddenItems{Sessions: []string{sid}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.HiddenItems()
	if err != nil || !reflect.DeepEqual(got.Sessions, []string{sid}) {
		t.Fatalf("online session lost its hidden entry: %+v %v", got, err)
	}
}
