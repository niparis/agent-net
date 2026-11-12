package gateway

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Plan step (d): the migration that moves the open Desk items made before the
// front door.

// legacyAsk is an inbox row as a gateway before the front door wrote it.
type legacyAsk struct {
	id, kind, next, options, status, fromKind, sourceRef string
}

func seedLegacyAsks(t *testing.T, s *Store, asks ...legacyAsk) {
	t.Helper()
	for i, a := range asks {
		if a.status == "" {
			a.status = "open"
		}
		if a.options == "" {
			a.options = "[]"
		}
		if _, err := s.db.Exec(`INSERT INTO inbox_items (id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status,options,from_kind,source_ref,next_who)
			VALUES (?,?,'s_asker','demo',?,'','','',?,?,?,?,?,?)`,
			a.id, a.kind, "ask "+a.id, int64(i+1), a.status, a.options, a.fromKind, a.sourceRef, a.next); err != nil {
			t.Fatal(err)
		}
	}
}

// rerunDeskReroute rewinds the store to before the reroute migration and
// migrates it again, as an upgrade does.
func rerunDeskReroute(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "Desk reroute of open items")); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
}

// deskRoute reads where an item is.
func deskRoute(t *testing.T, s *Store, id string) (status, next, owner, state string) {
	t.Helper()
	if err := s.db.QueryRow(`SELECT status,next_who,owner,state FROM inbox_items WHERE id=?`, id).Scan(&status, &next, &owner, &state); err != nil {
		t.Fatal(err)
	}
	return
}

// The thirteen Desk ask kinds that went to the chief and then to the owner
// after 15 minutes (0152, Context). 0152 sends each of them to the chief.
var chiefAskKinds = []string{
	protocol.AskKindApproval, protocol.AskKindStep, protocol.AskKindQuestion, protocol.AskKindIdea,
	protocol.AskKindCheck, protocol.AskKindWaiting, protocol.AskKindRejection, protocol.AskKindEscalation,
	protocol.AskKindStall, protocol.AskKindHeld, protocol.AskKindDraft, protocol.AskKindLearned,
	protocol.AskKindProposal,
}

// seedEveryKind writes open items of every ask kind in every place an item can
// wait, and settled items beside them.
func seedEveryKind(t *testing.T, s *Store) {
	t.Helper()
	kinds := append(append([]string{}, chiefAskKinds...), protocol.AskKindSignIn, protocol.AskKindBrief, protocol.AskKindPermission,
		protocol.AskKindMachine, protocol.AskKindCTOAlert, protocol.AskKindQuotaStuck)
	var asks []legacyAsk
	for _, kind := range kinds {
		for j, next := range []string{"", protocol.AskNextOwner, protocol.AskNextChief, "s_lead"} {
			asks = append(asks, legacyAsk{id: fmt.Sprintf("i_%s_%d", kind, j), kind: kind, next: next})
		}
		asks = append(asks,
			legacyAsk{id: "i_" + kind + "_opts", kind: kind, next: protocol.AskNextOwner, options: `["yes","no"]`},
			legacyAsk{id: "i_" + kind + "_answered", kind: kind, status: "answered"},
			legacyAsk{id: "i_" + kind + "_closed", kind: kind, status: "closed"})
	}
	asks = append(asks,
		legacyAsk{id: "i_oauth", kind: protocol.AskKindApproval, next: protocol.AskNextChief, fromKind: protocol.AskFromApps, sourceRef: "app-consent:c1"},
		legacyAsk{id: "i_joined", kind: protocol.AskKindQuestion, status: protocol.ItemStatusJoined})
	seedLegacyAsks(t, s, asks...)
	if _, err := s.db.Exec(`UPDATE inbox_items SET dup_of='i_question_1' WHERE id='i_joined'`); err != nil {
		t.Fatal(err)
	}
}

func TestDeskRerouteOwnerBoundWithoutOptionsGoesToChief(t *testing.T) {
	s, c := newTestStore(t)
	seedLegacyAsks(t, s,
		legacyAsk{id: "i_q", kind: protocol.AskKindQuestion, next: protocol.AskNextOwner},
		legacyAsk{id: "i_blank", kind: protocol.AskKindCheck, next: ""})
	if _, err := s.db.Exec(`UPDATE inbox_items SET chief_sent_at=5`); err != nil {
		t.Fatal(err)
	}
	rerunDeskReroute(t, s)
	for _, id := range []string{"i_q", "i_blank"} {
		status, next, owner, state := deskRoute(t, s, id)
		if status != "open" || next != protocol.AskNextChief || owner != protocol.DeskOwnerChief || state != protocol.DeskVerifying {
			t.Fatalf("%s: status=%s next=%s owner=%s state=%s, want open with the chief, verifying", id, status, next, owner, state)
		}
		var chiefAt, sent int64
		if err := s.db.QueryRow(`SELECT chief_at,chief_sent_at FROM inbox_items WHERE id=?`, id).Scan(&chiefAt, &sent); err != nil {
			t.Fatal(err)
		}
		if chiefAt != c.now().UnixNano() || sent != 0 {
			t.Fatalf("%s: chief_at=%d chief_sent_at=%d, want the chief's wait to start now and the ask in its next batch", id, chiefAt, sent)
		}
	}
}

func TestDeskRerouteAskKindsGoToChief(t *testing.T) {
	s, _ := newTestStore(t)
	for i, kind := range chiefAskKinds {
		next := protocol.AskNextOwner
		if i%2 == 0 {
			next = ""
		}
		seedLegacyAsks(t, s, legacyAsk{id: "i_" + kind, kind: kind, next: next})
	}
	rerunDeskReroute(t, s)
	for _, kind := range chiefAskKinds {
		if _, next, owner, state := deskRoute(t, s, "i_"+kind); next != protocol.AskNextChief || owner != protocol.DeskOwnerChief || state != protocol.DeskVerifying {
			t.Errorf("%s: next=%s owner=%s state=%s, want the chief (0152)", kind, next, owner, state)
		}
	}
}

func TestDeskRerouteSignInAndOAuthStayOwnerBound(t *testing.T) {
	s, _ := newTestStore(t)
	seedLegacyAsks(t, s,
		legacyAsk{id: "i_sign", kind: protocol.AskKindSignIn, next: protocol.AskNextOwner},
		legacyAsk{id: "i_sign_chief", kind: protocol.AskKindSignIn, next: protocol.AskNextChief},
		legacyAsk{id: "i_oauth", kind: protocol.AskKindApproval, next: protocol.AskNextChief, fromKind: protocol.AskFromApps, sourceRef: "app-consent:c1"},
		legacyAsk{id: "i_public", kind: protocol.AskKindApproval, next: protocol.AskNextOwner, fromKind: protocol.AskFromApps, sourceRef: publicConsentRef})
	rerunDeskReroute(t, s)
	for _, id := range []string{"i_sign", "i_sign_chief", "i_oauth", "i_public"} {
		if status, next, _, state := deskRoute(t, s, id); status != "open" || next != protocol.AskNextOwner || state != protocol.DeskReady {
			t.Errorf("%s: status=%s next=%s state=%s, want open with the owner, ready", id, status, next, state)
		}
	}
}

func TestDeskRerouteKeepsWhatTheChiefSentTheOwner(t *testing.T) {
	s, _ := newTestStore(t)
	seedLegacyAsks(t, s,
		legacyAsk{id: "i_with_opts", kind: protocol.AskKindQuestion, next: protocol.AskNextOwner, options: `["merge","wait"]`},
		legacyAsk{id: "i_approval", kind: protocol.AskKindApproval, next: protocol.AskNextOwner},
		legacyAsk{id: "i_no_opts", kind: protocol.AskKindQuestion, next: protocol.AskNextOwner},
		legacyAsk{id: "i_timer", kind: protocol.AskKindQuestion, next: protocol.AskNextOwner, options: `["merge","wait"]`})
	for _, id := range []string{"i_with_opts", "i_approval", "i_no_opts"} {
		if _, err := s.db.Exec(`INSERT INTO ask_sorts(ask_id,at,sorter,result) VALUES(?,1,'s_chief',?)`, id, protocol.AskSortOwner); err != nil {
			t.Fatal(err)
		}
	}
	rerunDeskReroute(t, s)
	want := map[string]string{
		"i_with_opts": protocol.AskNextOwner, // the chief sent it and it has options
		"i_approval":  protocol.AskNextOwner, // an approval has fixed actions
		"i_no_opts":   protocol.AskNextChief, // a question without options is not an owner item
		"i_timer":     protocol.AskNextChief, // the timer sent it, the chief never verified it
	}
	for id, w := range want {
		if _, next, _, _ := deskRoute(t, s, id); next != w {
			t.Errorf("%s: next=%s, want %s", id, next, w)
		}
	}
}

func TestDeskRerouteGivesOpenItemsOwnerAndState(t *testing.T) {
	s, _ := newTestStore(t)
	seedLegacyAsks(t, s,
		legacyAsk{id: "i_open", kind: protocol.AskKindQuestion, next: "s_lead"},
		legacyAsk{id: "i_answered", kind: protocol.AskKindQuestion, status: "answered"})
	if _, err := s.db.Exec(`UPDATE inbox_items SET owner='', state='' WHERE id='i_open'`); err != nil {
		t.Fatal(err)
	}
	rerunDeskReroute(t, s)
	if status, next, owner, state := deskRoute(t, s, "i_open"); status != "open" || next != "s_lead" || owner != protocol.DeskOwnerChief || state != protocol.DeskVerifying {
		t.Fatalf("open item: status=%s next=%s owner=%s state=%s, want it left with s_lead, owner chief, verifying", status, next, owner, state)
	}
	if status, _, _, state := deskRoute(t, s, "i_answered"); status != "answered" || state != protocol.DeskAnswered {
		t.Fatalf("answered item: status=%s state=%s, want answered", status, state)
	}
}

func TestDeskRerouteLosesNoItem(t *testing.T) {
	s, _ := newTestStore(t)
	seedEveryKind(t, s)
	byStatus := func() map[string]int {
		t.Helper()
		rows, err := s.db.Query(`SELECT status,COUNT(*) FROM inbox_items GROUP BY status`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]int{}
		for rows.Next() {
			var status string
			var n int
			if err := rows.Scan(&status, &n); err != nil {
				t.Fatal(err)
			}
			out[status] = n
		}
		return out
	}
	before := byStatus()
	rerunDeskReroute(t, s)
	if after := byStatus(); !reflect.DeepEqual(before, after) {
		t.Fatalf("items by status: before %v, after %v; the migration must not refuse, drop or close an item", before, after)
	}
	var blank int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE status='open' AND (owner='' OR state='')`).Scan(&blank); err != nil {
		t.Fatal(err)
	}
	if blank != 0 {
		t.Fatalf("%d open items without an owner or a state", blank)
	}
}

func TestDeskRerouteIsIdempotent(t *testing.T) {
	s, c := newTestStore(t)
	seedEveryKind(t, s)
	snapshot := func() []string {
		t.Helper()
		rows, err := s.db.Query(`SELECT id,status,next_who,owner,state,chief_at,chief_sent_at,route_fixed FROM inbox_items ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var id, status, next, owner, state string
			var chiefAt, sent, fixed int64
			if err := rows.Scan(&id, &status, &next, &owner, &state, &chiefAt, &sent, &fixed); err != nil {
				t.Fatal(err)
			}
			out = append(out, fmt.Sprint(id, status, next, owner, state, chiefAt, sent, fixed))
		}
		return out
	}
	rerunDeskReroute(t, s)
	first := snapshot()
	c.t = c.t.Add(time.Hour)
	rerunDeskReroute(t, s)
	if second := snapshot(); !reflect.DeepEqual(first, second) {
		t.Fatalf("a second run changed items:\nfirst  %v\nsecond %v", first, second)
	}
}
