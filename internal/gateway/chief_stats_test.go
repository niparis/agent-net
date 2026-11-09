package gateway

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// listStats is one list call of the chief that asks for the sort counts.
func listStats(t *testing.T, s *Store, node, chief string, args protocol.AskSortArgs) protocol.AskSortStats {
	t.Helper()
	args.Result = protocol.AskSortList
	res, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: args})
	if err != nil {
		t.Fatalf("list with stats %+v: %v", args, err)
	}
	if res.Stats == nil {
		t.Fatalf("list with stats %+v: no stats", args)
	}
	return *res.Stats
}

// The list action counts the successful sorts of one sorter by result, within
// the whole history, a time range or an id range. Another sorter's sorts are
// never counted.
func TestAskSortListStats(t *testing.T) {
	s, _, chief, _, node := chiefRig(t)
	base := time.Unix(1000, 0)
	insert := func(sorter, result string, at time.Time) int64 {
		t.Helper()
		res, err := s.db.Exec(`INSERT INTO ask_sorts(ask_id,at,sorter,result) VALUES(?,?,?,?)`,
			"i_"+result, at.UnixNano(), sorter, result)
		if err != nil {
			t.Fatal(err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := insert(chief, protocol.AskSortClose, base)
	insert(chief, protocol.AskSortClose, base.Add(100*time.Second))
	insert(chief, protocol.AskSortMerge, base.Add(200*time.Second))
	insert(chief, protocol.AskSortSend, base.Add(300*time.Second))
	last := insert(chief, protocol.AskSortOwner, base.Add(400*time.Second))
	insert("owner", protocol.AskSortUndo, base.Add(250*time.Second))

	all := listStats(t, s, node, chief, protocol.AskSortArgs{})
	want := map[string]int{protocol.AskSortClose: 2, protocol.AskSortMerge: 1, protocol.AskSortSend: 1, protocol.AskSortOwner: 1}
	if all.Sorter != chief {
		t.Fatalf("sorter = %q, want the caller %q", all.Sorter, chief)
	}
	if len(all.Counts) != len(want) {
		t.Fatalf("counts = %v, want %v", all.Counts, want)
	}
	for result, n := range want {
		if all.Counts[result] != n {
			t.Errorf("counts[%s] = %d, want %d", result, all.Counts[result], n)
		}
	}
	if all.FirstID != first || all.LastID != last {
		t.Fatalf("ids = %d..%d, want %d..%d", all.FirstID, all.LastID, first, last)
	}

	timed := listStats(t, s, node, chief, protocol.AskSortArgs{Since: 1200, Until: 1300})
	if len(timed.Counts) != 2 || timed.Counts[protocol.AskSortMerge] != 1 || timed.Counts[protocol.AskSortSend] != 1 {
		t.Fatalf("timed counts = %v, want one merge and one send", timed.Counts)
	}
	if timed.FirstID == all.FirstID || timed.LastID == all.LastID {
		t.Fatalf("timed ids = %d..%d, want the two middle rows", timed.FirstID, timed.LastID)
	}

	byID := listStats(t, s, node, chief, protocol.AskSortArgs{FromID: first + 1, ToID: last - 1})
	if len(byID.Counts) != 3 || byID.Counts[protocol.AskSortClose] != 1 || byID.Counts[protocol.AskSortMerge] != 1 || byID.Counts[protocol.AskSortSend] != 1 {
		t.Fatalf("id range counts = %v", byID.Counts)
	}

	owner := listStats(t, s, node, chief, protocol.AskSortArgs{Sorter: "owner"})
	if len(owner.Counts) != 1 || owner.Counts[protocol.AskSortUndo] != 1 {
		t.Fatalf("owner counts = %v, want one undo", owner.Counts)
	}
}
