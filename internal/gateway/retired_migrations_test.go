package gateway

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// retiredTables are the tables of the removed subsystems: session dues,
// learned suggestions, findings and permission proposals.
var retiredTables = []string{"session_dues", "learned_suggestions", "findings", "permission_proposals"}

// retiredDropVersion is the version of the retired tables drop.
func retiredDropVersion(t *testing.T) int {
	t.Helper()
	return migrationVersion(t, "retired subsystems: session dues")
}

// openAfterRetiredDrop builds a database just before the retired tables drop,
// runs seed on it and opens the store, which runs the drop.
func openAfterRetiredDrop(t *testing.T, seed ...string) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway.db")
	old := migrateOnly(t, path, retiredDropVersion(t)-1)
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

// replayRetiredDrop rewinds s to just before the drop, puts the findings and
// permission_proposals tables back with seed, and runs the drop again, as a
// restored backup would.
func replayRetiredDrop(t *testing.T, s *Store, seed ...string) *Store {
	t.Helper()
	execSeed(t, s, `DELETE FROM schema_version WHERE version >= `+strconv.Itoa(retiredDropVersion(t)), permissionProposalMigration,
		`CREATE TABLE findings (id INTEGER PRIMARY KEY AUTOINCREMENT, subject_kind TEXT NOT NULL, subject TEXT NOT NULL, finding TEXT NOT NULL,
			evidence TEXT NOT NULL DEFAULT '[]', reporter TEXT NOT NULL, node_id TEXT NOT NULL DEFAULT '', at INTEGER NOT NULL, status TEXT NOT NULL DEFAULT 'new')`)
	execSeed(t, s, seed...)
	return reopenAsIs(t, s)
}

// reopenAsIs closes s and opens its file again. Unlike reopen, it puts no
// dropped table back first, so the migrations see the database as it is.
func reopenAsIs(t *testing.T, s *Store) *Store {
	t.Helper()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := OpenStore(s.path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { s2.Close() })
	return s2
}

// execSeed runs each statement on s.
func execSeed(t *testing.T, s *Store, qs ...string) {
	t.Helper()
	for _, q := range qs {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// A database from before the drop keeps its other data: the retired tables go,
// the automatic playbook notes that findings made go unless pinned, and the
// owner's notes stay.
func TestRetiredTablesDropKeepsOwnerNotes(t *testing.T) {
	s := openAfterRetiredDrop(t,
		`INSERT INTO session_dues(id,session_id,what,created_at) VALUES('d_1','s_1','flip',1)`,
		`INSERT INTO learned_suggestions(node,field,edge_kind,kind,phrase,evidence,status,severity,created_at,ready_at) VALUES('demo','rules','','add','p','[]','pending','minor',1,1)`,
		`INSERT INTO findings(subject_kind,subject,finding,evidence,reporter,node_id,at,status) VALUES('model','terra','slow','[]','s_1','n_1',1,'new')`,
		`INSERT INTO permission_proposals(id,targets,reason,proposed_by,created_at) VALUES('pp_1','[]','r','s_1',1)`,
		`INSERT INTO playbook(subject_kind,subject,harness,note,source,support,track_line,pinned,stale,created_at,updated_at) VALUES
			('model','terra','','auto note','auto',3,'',0,0,1,1),
			('model','luna','','pinned auto note','auto',3,'',1,0,1,1),
			('model','sol','','owner note','owner',0,'',0,0,1,1)`)
	for _, name := range retiredTables {
		if tableExists(t, s, name) {
			t.Errorf("table %s is still in the schema", name)
		}
	}
	notes, err := s.PlaybookNotes()
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string]bool{}
	for _, n := range notes {
		kept[n.Subject] = true
	}
	if len(notes) != 2 || !kept["luna"] || !kept["sol"] {
		t.Fatalf("playbook notes after the drop: %+v", notes)
	}
}

// Replaying the migrations after the drop must not touch the dropped tables:
// the permission columns migration runs again on a store that has no
// permission_proposals table.
func TestMigrationsReplayAfterRetiredDrop(t *testing.T) {
	s := openAfterRetiredDrop(t)
	execSeed(t, s, `DELETE FROM schema_version WHERE version >= `+strconv.Itoa(migrationVersion(t, "permission decided reason column")))
	reopenAsIs(t, s)
}

// Each pending permission proposal becomes one message to the chief of staff,
// with the permission_apply call the CTO runs on the owner's words. A decided
// proposal is not sent, and a replay sends nothing twice.
func TestRetiredDropSendsPendingPermissionsToTheChief(t *testing.T) {
	seed := []string{
		`INSERT INTO permission_proposals(id,kind,rules,targets,reason,proposed_by,proposed_name,status,created_at) VALUES
			('pp_rules','permissions','["Bash(go test:*)"]','[{"name":"demo","node_id":"n1","hostname":"box","path":"/src/demo"}]','run the tests','s_asker','asker','pending',1),
			('pp_trust','trust','[]','[{"name":"","node_id":"n1","hostname":"box","path":"/src/other"}]','folder trust for a start','agent-net','agent-net','pending',2),
			('pp_done','permissions','["Bash(ls:*)"]','[{"name":"demo","node_id":"n1","hostname":"box","path":"/src/demo"}]','old','s_asker','asker','approved',3)`,
	}
	s := openAfterRetiredDrop(t, append([]string{
		`INSERT INTO nodes(id,hostname,os,token_hash,created_at) VALUES('n1','box','linux','h',1)`,
		`INSERT INTO sessions(id,node_id,harness,native_id,name,online,last_seen) VALUES
			('s_chief','n1','claude','nc','chief',1,1),
			('s_asker','n1','codex','na','asker',1,1)`,
		`INSERT OR REPLACE INTO gateway_state(key,value,updated_at) VALUES('chief_session','s_chief',1)`,
	}, seed...)...)
	check := func(s *Store) {
		t.Helper()
		got := chiefMessages(t, s, "s_chief")
		if len(got) != 2 {
			t.Fatalf("messages to the chief: %+v", got)
		}
		rules, trust := got[ownerItemKeyPrefix+"perm_migrate:pp_rules"], got[ownerItemKeyPrefix+"perm_migrate:pp_trust"]
		if rules.sender != "s_asker" || rules.typ != "request" {
			t.Errorf("rules proposal message from %s as %s, want a request from the proposer", rules.sender, rules.typ)
		}
		for _, want := range []string{"pp_rules", "asker", "Bash(go test:*)", "demo on box", "run the tests",
			`cto_op {"method":"permission_apply","args":{"kind":"permissions","owner_words":"\u003cquote the owner's words\u003e","rules":["Bash(go test:*)"],"targets":["demo"]}}`} {
			if !strings.Contains(rules.body, want) {
				t.Errorf("rules proposal message lacks %q:\n%s", want, rules.body)
			}
		}
		if trust.sender != agentNetSender || trust.typ != "notification" {
			t.Errorf("trust proposal message from %s as %s, want a notification from agent-net", trust.sender, trust.typ)
		}
		for _, want := range []string{"pp_trust", "/src/other", "folder trust for a start",
			`cto_op {"method":"permission_apply","args":{"kind":"trust","machine":"box","owner_words":"\u003cquote the owner's words\u003e","targets":["/src/other"]}}`} {
			if !strings.Contains(trust.body, want) {
				t.Errorf("trust proposal message lacks %q:\n%s", want, trust.body)
			}
		}
	}
	check(s)
	check(replayRetiredDrop(t, s, seed...))
}

// With no chief of staff, a pending proposal goes to the CTO as a notice.
func TestRetiredDropSendsPendingPermissionsToTheCTOWithoutAChief(t *testing.T) {
	s := openAfterRetiredDrop(t,
		`INSERT INTO nodes(id,hostname,os,token_hash,created_at) VALUES('n1','box','linux','h',1)`,
		`INSERT INTO sessions(id,node_id,harness,native_id,name,online,last_seen) VALUES
			('s_cto','n1','cto','nc','cto',1,1),
			('s_asker','n1','codex','na','asker',1,1)`,
		`INSERT OR REPLACE INTO gateway_state(key,value,updated_at) VALUES('cto_session','s_cto',1)`,
		`INSERT INTO permission_proposals(id,kind,rules,targets,reason,proposed_by,proposed_name,status,created_at) VALUES
			('pp_rules','permissions','["Bash(go test:*)"]','[{"name":"demo","node_id":"n1","hostname":"box","path":"/src/demo"}]','run the tests','s_asker','asker','pending',1)`)
	got := chiefMessages(t, s, "s_cto")
	m, ok := got[ownerItemKeyPrefix+"perm_migrate:pp_rules"]
	if len(got) != 1 || !ok || m.sender != agentNetSender || m.typ != "notification" {
		t.Fatalf("messages to the CTO: %+v", got)
	}
}

type storedMessage struct{ sender, typ, body string }

// chiefMessages are the stored messages to one session, by idempotency key.
func chiefMessages(t *testing.T, s *Store, to string) map[string]storedMessage {
	t.Helper()
	rows, err := s.db.Query(`SELECT idem_key,sender,type,body FROM messages WHERE recipient=? AND state='stored'`, to)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]storedMessage{}
	for rows.Next() {
		var key string
		var m storedMessage
		if err := rows.Scan(&key, &m.sender, &m.typ, &m.body); err != nil {
			t.Fatal(err)
		}
		out[key] = m
	}
	return out
}

// Findings become owner-wide gateway notes, one per finding, so what agents
// learned survives the drop. A replay adds no second copy.
func TestRetiredDropKeepsFindingsAsOwnerNotes(t *testing.T) {
	seed := []string{`INSERT INTO findings(id,subject_kind,subject,finding,evidence,reporter,node_id,at,status) VALUES
		(1,'model','terra','slow on long diffs','["run 12"]','s_1','n_1',1700000000,'new'),
		(2,'model','terra','skips tests','[]','s_2','n_1',1700000100,'applied'),
		(3,'harness','Open Code','needs a restart','[]','s_1','n_1',1700000200,'new')`}
	s := openAfterRetiredDrop(t, seed...)
	check := func(s *Store) {
		t.Helper()
		notes, err := s.visibleNotes("", "", "owner")
		if err != nil {
			t.Fatal(err)
		}
		byName := map[string]string{}
		for _, n := range notes {
			byName[n.Name] = n.Text
		}
		want := map[string][]string{
			"finding-1-model-terra":       {"slow on long diffs", "model terra", "run 12"},
			"finding-2-model-terra":       {"skips tests", "model terra"},
			"finding-3-harness-open-code": {"needs a restart", "harness Open Code"},
		}
		if len(byName) != len(want) {
			t.Fatalf("owner notes after the drop: %+v", byName)
		}
		for name, parts := range want {
			text, ok := byName[name]
			if !ok {
				t.Errorf("no note %s in %+v", name, byName)
			}
			for _, p := range parts {
				if !strings.Contains(text, p) {
					t.Errorf("note %s lacks %q:\n%s", name, p, text)
				}
			}
		}
	}
	check(s)
	check(replayRetiredDrop(t, s, seed...))
}
