package gateway

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

var deskDroppedTables = []string{"inbox_items", "ask_sorts", "desk_log", "follow_ups"}

func deskDropVersion(t *testing.T) int {
	t.Helper()
	return migrationVersion(t, dropDeskTablesMarker)
}

func openAfterDeskDrop(t *testing.T, seed ...string) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway.db")
	old := migrateOnly(t, path, deskDropVersion(t)-1)
	execSeed(t, old, seed...)
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// Open rows in inbox_items become idempotent messages to the chief of staff
// before dropDeskTablesMigration drops inbox_items, ask_sorts, desk_log and
// follow_ups. Closed rows are not sent, and replaying the drop sends nothing
// twice.
func TestDeskDropPreservesOpenInboxItemsToChief(t *testing.T) {
	seed := []string{
		`INSERT INTO inbox_items(id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status,options,asker_name,why_you,detail,about_kind,about_id) VALUES
			('i_open_agent','question','s_asker','demo','Should we rotate the prod key?','Rotate tonight','','',10,'open','["rotate","wait"]','asker','Needs owner sign-off','Key expires Friday','project','demo'),
			('i_open_sys','check','agent-net','box','Disk almost full','','','',20,'open','[]','agent-net','Owner must free space','','machine','box'),
			('i_closed','question','s_asker','demo','Already answered','','','',30,'answered','[]','asker','','','','')`,
		`INSERT INTO ask_sorts(ask_id,at,sorter,result,reason) VALUES('i_closed',35,'s_chief','close','done')`,
		`INSERT INTO desk_log(at,source,about_kind,about_id,text,detail,options) VALUES(40,'chief','ask','i_closed','Closed','','[]')`,
		`INSERT INTO follow_ups(id,event,set_by,waiter,summary,created_at,due_at,status) VALUES('fu_1','reply','s_chief','s_chief','check',10,20,'pending')`,
	}
	s := openAfterDeskDrop(t, append([]string{
		`INSERT INTO nodes(id,hostname,os,token_hash,created_at) VALUES('n1','box','linux','h',1)`,
		`INSERT INTO sessions(id,node_id,harness,native_id,name,online,last_seen) VALUES
			('s_chief','n1','claude','nc','chief',1,1),
			('s_asker','n1','codex','na','asker',1,1)`,
		`INSERT OR REPLACE INTO gateway_state(key,value,updated_at) VALUES('chief_session','s_chief',1)`,
	}, seed...)...)

	check := func(s *Store) {
		t.Helper()
		for _, tbl := range deskDroppedTables {
			if tableExists(t, s, tbl) {
				t.Errorf("table %s is still in the schema after dropDeskTablesMigration", tbl)
			}
		}
		got := chiefMessages(t, s, "s_chief")
		if len(got) != 2 {
			t.Fatalf("messages to chief: %+v", got)
		}
		agentMsg, ok := got[ownerItemKeyPrefix+"desk_migrate:i_open_agent"]
		if !ok {
			t.Fatalf("missing migrated agent ask in %+v", got)
		}
		if agentMsg.sender != "s_asker" || agentMsg.typ != protocol.TypeRequest {
			t.Errorf("agent ask sent from %s as %s, want request from s_asker", agentMsg.sender, agentMsg.typ)
		}
		for _, want := range []string{"i_open_agent", "asker", "Should we rotate the prod key?", "Key expires Friday", "Needs owner sign-off", "Rotate tonight", "rotate / wait"} {
			if !strings.Contains(agentMsg.body, want) {
				t.Errorf("agent ask message lacks %q:\n%s", want, agentMsg.body)
			}
		}
		sysMsg, ok := got[ownerItemKeyPrefix+"desk_migrate:i_open_sys"]
		if !ok {
			t.Fatalf("missing migrated system ask in %+v", got)
		}
		if sysMsg.sender != agentNetSender || sysMsg.typ != protocol.TypeNotification {
			t.Errorf("system ask sent from %s as %s, want notification from agent-net", sysMsg.sender, sysMsg.typ)
		}
		if !strings.Contains(sysMsg.body, "Disk almost full") {
			t.Errorf("system ask message lacks text:\n%s", sysMsg.body)
		}
	}
	check(s)

	// Replay the drop with the same open inbox_items restored: idempotency keys
	// must prevent duplicate chief messages.
	execSeed(t, s,
		`DELETE FROM schema_version WHERE version >= `+strconv.Itoa(deskDropVersion(t)),
		`CREATE TABLE inbox_items (id TEXT PRIMARY KEY, kind TEXT NOT NULL, session_id TEXT NOT NULL, node TEXT NOT NULL,
			text TEXT NOT NULL, recommendation TEXT NOT NULL DEFAULT '', message_id TEXT NOT NULL DEFAULT '',
			conversation_id TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, status TEXT NOT NULL,
			options TEXT NOT NULL DEFAULT '[]', asker_name TEXT NOT NULL DEFAULT '', why_you TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '', about_kind TEXT NOT NULL DEFAULT '', about_id TEXT NOT NULL DEFAULT '')`,
		seed[0],
	)
	check(reopenAsIs(t, s))
}

// With no chief of staff, an open inbox_items row goes to the CTO as a notice.
func TestDeskDropFallsBackToCTOWithoutChief(t *testing.T) {
	s := openAfterDeskDrop(t,
		`INSERT INTO nodes(id,hostname,os,token_hash,created_at) VALUES('n1','box','linux','h',1)`,
		`INSERT INTO sessions(id,node_id,harness,native_id,name,online,last_seen) VALUES
			('s_cto','n1','cto','nc','cto',1,1),
			('s_asker','n1','codex','na','asker',1,1)`,
		`INSERT OR REPLACE INTO gateway_state(key,value,updated_at) VALUES('cto_session','s_cto',1)`,
		`INSERT INTO inbox_items(id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status) VALUES
			('i_open','question','s_asker','demo','Need approval','','','',10,'open')`,
	)
	got := chiefMessages(t, s, "s_cto")
	m, ok := got[ownerItemKeyPrefix+"desk_migrate:i_open"]
	if len(got) != 1 || !ok || m.sender != agentNetSender || m.typ != protocol.TypeNotification {
		t.Fatalf("messages to CTO: %+v", got)
	}
	if !strings.Contains(m.body, "Need approval") {
		t.Fatalf("message body: %s", m.body)
	}
}

// Replaying earlier migrations that altered inbox_items, ask_sorts or desk_log
// after dropDeskTablesMigration must succeed without recreating or crashing on
// the dropped tables.
func TestMigrationsReplayAfterDeskDrop(t *testing.T) {
	s := openAfterDeskDrop(t)
	for _, marker := range []string{
		deskHideRemovalMarker,
		deskTablesMarker,
	} {
		v := migrationVersion(t, marker)
		execSeed(t, s, `DELETE FROM schema_version WHERE version >= `+strconv.Itoa(v))
		s = reopenAsIs(t, s)
		for _, tbl := range deskDroppedTables {
			if tableExists(t, s, tbl) {
				t.Fatalf("table %s reappeared after replaying from %q", tbl, marker)
			}
		}
	}
}
