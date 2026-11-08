package gateway

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/sqlitecache"
)

// A cached read names the tables it reads (see readThrough). A table missing
// from the set is a stale read, so these tests hold the sets true:
//
//   - TestCachedReadsDeclareEveryTableTheyRead runs each cached read and asks
//     SQLite which tables its statements read. A table that is not declared
//     fails the test.
//   - TestCachedReadsAreInvalidatedByEveryDeclaredTable writes a row to each
//     declared table and checks the read loads again.
//   - TestCachedReadsSurviveWritesToOtherTables checks the point of it all.

// readCase is one cached read, the tables it declares and how to run it.
type readCase struct {
	name   string
	tables []string
	run    func(s *Store)
}

func readCases() []readCase {
	return []readCase{
		{"settings", settingsTables, func(s *Store) { s.SettingsSnapshot() }},
		{"graph", graphTables, func(s *Store) { s.Graph() }},
		{"agents", agentsTables, func(s *Store) { s.Agents(protocol.QueryAgents{}) }},
		{"agents online", agentsTables, func(s *Store) {
			s.Agents(protocol.QueryAgents{OnlineOnly: true, IncludeOwners: true, Project: "cogforge", Text: "a"})
		}},
	}
}

// richReadStore is a store whose cached reads take every branch: sessions with
// delegation policies (so models are resolved), charters, edges, a loop, a
// failed model, a request that waits for a reply.
func richReadStore(t *testing.T) *Store {
	t.Helper()
	s, node, oc1, oc2, _ := policyFixture(t)
	pol := protocol.DelegationPolicy{Delegate: protocol.DelegateNative, CoderModel: "deepseek 4.1 flash", MainModel: "sol 6.1"}
	if ok, err := s.SetDelegation(oc1, protocol.ScopeSession, pol); err != nil || !ok {
		t.Fatalf("session policy: %v %v", ok, err)
	}
	if ok, err := s.SetDelegation(oc2, protocol.ScopeProject, pol); err != nil || !ok {
		t.Fatalf("project policy: %v %v", ok, err)
	}
	if err := s.RecordModelFailure(node, "opencode", "deepseek/deepseek-v4.1-flash"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordModelSuccess(node, "opencode", "sol/sol-6.1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCharter("cogforge", "purpose", "build things", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEdge(protocol.Edge{From: "cogforge", To: "other", Kind: "depends"}, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteSettings([]SettingsChange{{Key: "loop.every", Level: "repo", Scope: "cogforge", Value: settingValue(`"4h"`)}}, "owner", "t"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: oc1, Recipient: oc2, Body: "question", Type: protocol.TypeRequest}); err != nil {
		t.Fatal(err)
	}
	return s
}

// observed runs f and returns the tables its statements read and the
// statements the connector could not describe.
func observed(t *testing.T, s *Store, f func()) (reads map[string]bool, unknown []string) {
	t.Helper()
	reads = map[string]bool{}
	s.connector.SetObserver(func(st sqlitecache.Statement) {
		if !st.Known {
			unknown = append(unknown, st.Query)
		}
		for _, r := range st.Reads {
			reads[r] = true
		}
	})
	defer s.connector.SetObserver(nil)
	f()
	return reads, unknown
}

func TestCachedReadsDeclareEveryTableTheyRead(t *testing.T) {
	check := func(t *testing.T, s *Store) {
		for _, c := range readCases() {
			s.rc = readCache{}
			reads, unknown := observed(t, s, func() { c.run(s) })
			if len(unknown) > 0 {
				t.Errorf("%s: the connector could not tell the tables of: %.100s", c.name, unknown[0])
			}
			if len(reads) == 0 {
				t.Fatalf("%s: no statement was seen", c.name)
			}
			declared := map[string]bool{}
			for _, d := range c.tables {
				declared[d] = true
			}
			var missing []string
			for r := range reads {
				if !declared[r] {
					missing = append(missing, r)
				}
			}
			sort.Strings(missing)
			if len(missing) > 0 {
				t.Errorf("%s reads %v, which its declared tables do not name. Add them to the set next to the query.", c.name, missing)
			}
		}
	}
	t.Run("rich store", func(t *testing.T) { check(t, richReadStore(t)) })
	t.Run("graph refs on", func(t *testing.T) {
		s := richReadStore(t)
		s.graphRefsOn.Store(true)
		check(t, s)
	})
	t.Run("the live database", func(t *testing.T) {
		path := os.Getenv("AGENT_NET_BENCH_DB")
		if path == "" {
			t.Skip("AGENT_NET_BENCH_DB is not set")
		}
		s, err := OpenStore(path)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		check(t, s)
	})
}

// The sets name tables that exist, spelled in lower case, once each.
func TestCachedReadTableSetsNameRealTables(t *testing.T) {
	s, _ := newTestStore(t)
	real := map[string]bool{}
	rows, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n string
		rows.Scan(&n)
		real[n] = true
	}
	rows.Close()
	for _, c := range readCases() {
		seen := map[string]bool{}
		for _, tbl := range c.tables {
			if !real[tbl] {
				t.Errorf("%s declares %q, which is no table", c.name, tbl)
			}
			if seen[tbl] || tbl != strings.ToLower(tbl) {
				t.Errorf("%s declares %q twice or not in lower case", c.name, tbl)
			}
			seen[tbl] = true
		}
	}
}

// The stamps rest on the connector placing every write. A trigger or a foreign
// key action writes a table the statement does not name; the schema must not
// hide a view or an action the connector cannot see.
func TestSchemaHasNoWritesTheConnectorMisses(t *testing.T) {
	s, _ := newTestStore(t)
	var views int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='view'`).Scan(&views); err != nil || views != 0 {
		t.Fatalf("%d views (%v): a write through a view may change tables the row count does not show", views, err)
	}
	var fk int
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 0 {
		t.Fatalf("foreign_keys is %d (%v): actions write tables a statement does not name", fk, err)
	}
}

// rowFor inserts a row with fresh values into table. The values are not
// meaningful; they only have to be accepted.
func rowFor(s *Store, table string, n int) error {
	rows, err := s.db.Query(`SELECT name, type, pk FROM pragma_table_info(?)`, table)
	if err != nil {
		return err
	}
	var cols, marks []string
	var args []any
	var pks int
	type col struct {
		name, typ string
		pk        int
	}
	var all []col
	for rows.Next() {
		var c col
		if err := rows.Scan(&c.name, &c.typ, &c.pk); err != nil {
			rows.Close()
			return err
		}
		if c.pk > 0 {
			pks++
		}
		all = append(all, c)
	}
	rows.Close()
	for _, c := range all {
		typ := strings.ToUpper(c.typ)
		if c.pk == 1 && pks == 1 && typ == "INTEGER" {
			continue // the row id
		}
		cols = append(cols, `"`+c.name+`"`)
		marks = append(marks, "?")
		if strings.Contains(typ, "INT") || strings.Contains(typ, "BOOL") {
			args = append(args, n)
		} else {
			args = append(args, fmt.Sprintf("zz%d", n))
		}
	}
	_, err = s.db.Exec(`INSERT INTO "`+table+`"(`+strings.Join(cols, ",")+`) VALUES(`+strings.Join(marks, ",")+`)`, args...)
	return err
}

// writeRow is how the tests change a table. A table the generic row does not
// fit has its own statement here.
var writeRowSQL = map[string]string{
	"setting_revision": `UPDATE setting_revision SET revision=revision+1`,
	"setting_values":   `INSERT INTO setting_values(key,level,scope,item,value,revision) VALUES('probe','repo','probe','','"x"',1)`,
	"charter_values":   `INSERT INTO charter_values(level,scope,attr,value,updated_at,updated_by) VALUES('global','','probe','x',1,'t')`,
	"apps":             `INSERT INTO apps(id,name,kind,client_id,client_name,created_at) VALUES('app_probe','probe','owner_app','c','c',1)`,
	"teams":            `INSERT INTO teams(id,name,created_at,updated_at) VALUES('t_probe','probe',1,1)`,
	"team_members":     `INSERT INTO team_members(member_kind,member_id,team_id) VALUES('repo','probe','t_probe')`,
	// A charter must belong to a node, or the graph does not load.
	"graph_charter": `INSERT INTO graph_charter(node,attr,value,updated_at,updated_by) VALUES('cogforge','probe','x',1,'t')`,
}

func writeTo(t *testing.T, s *Store, table string, n int) {
	t.Helper()
	before := s.connector.Stamp(table)
	var err error
	if q, ok := writeRowSQL[table]; ok {
		_, err = s.db.Exec(q)
	} else {
		err = rowFor(s, table, 1000+n)
	}
	if err != nil {
		t.Fatalf("write to %s: %v. Add a statement for it to writeRowSQL.", table, err)
	}
	if s.connector.Stamp(table) == before {
		t.Fatalf("a write to %s did not move its stamp", table)
	}
}

// statements counts the statements f ran. A read that is served from the
// cache runs none.
func statements(s *Store, f func()) int {
	n := 0
	s.connector.SetObserver(func(sqlitecache.Statement) { n++ })
	defer s.connector.SetObserver(nil)
	f()
	return n
}

func TestCachedReadsAreInvalidatedByEveryDeclaredTable(t *testing.T) {
	for _, c := range readCases() {
		c := c
		t.Run(c.name, func(t *testing.T) {
			s := richReadStore(t)
			c.run(s)
			if n := statements(s, func() { c.run(s) }); n != 0 {
				t.Fatalf("the second read ran %d statements: it is not kept", n)
			}
			for i, table := range c.tables {
				i, table := i, table
				t.Run(table, func(t *testing.T) {
					writeTo(t, s, table, i)
					if n := statements(s, func() { c.run(s) }); n == 0 {
						t.Errorf("a write to %s did not make the read load again", table)
					}
					// And it is kept again.
					if n := statements(s, func() { c.run(s) }); n != 0 {
						t.Errorf("after the write to %s the read was not kept (%d statements)", table, n)
					}
				})
			}
		})
	}
}

// What the gateway writes all day does not touch the reads that do not use it.
func TestCachedReadsSurviveWritesToOtherTables(t *testing.T) {
	s := richReadStore(t)
	reads := []readCase{readCases()[0], readCases()[1], readCases()[2]}
	for _, c := range reads {
		c.run(s)
	}
	declared := map[string]map[string]bool{}
	for _, c := range reads {
		declared[c.name] = map[string]bool{}
		for _, tbl := range c.tables {
			declared[c.name][tbl] = true
		}
	}
	msg := ""
	for _, c := range reads {
		for _, other := range []string{"events", "deliveries", "desk_log", "delivery_evidence", "worker_runs", "session_wakes"} {
			if declared[c.name][other] {
				msg += fmt.Sprintf("%s declares %s; ", c.name, other)
			}
		}
	}
	if msg != "" {
		t.Fatal(msg)
	}
	// An acknowledgement writes deliveries, messages and events.
	var msgID string
	if err := s.db.QueryRow(`SELECT id FROM messages LIMIT 1`).Scan(&msgID); err != nil {
		t.Fatal(err)
	}
	graphOnly := []readCase{reads[0], reads[1]}
	for i := 0; i < 3; i++ {
		if _, _, err := s.ApplyDeliveryReport(msgID, []string{protocol.StateDispatched, protocol.StateDelivered, protocol.StateDispatched}[i], "t"); err != nil {
			t.Fatal(err)
		}
		s.Event("bench.event", "", "", "", "x")
		for _, c := range graphOnly {
			if n := statements(s, func() { c.run(s) }); n != 0 {
				t.Fatalf("%s loaded again after an acknowledgement (%d statements)", c.name, n)
			}
		}
	}
}
