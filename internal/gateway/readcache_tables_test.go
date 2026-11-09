package gateway

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/phrasing"
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
		{"worker log", workerLogTables, func(s *Store) { s.WorkerLog(200) }},
		{"inbox", inboxTables, func(s *Store) { s.ListInbox() }},
		{"agent", agentOneTables, func(s *Store) { s.Agent(richAgentID) }},
		{"visible graph", visibleTables, func(s *Store) { s.visibleGraph(false) }},
		{"worker failures", failuresTables, func(s *Store) { s.RecentWorkerFailures("n_readcase", "opencode") }},
		{"calibration", calibrationTables, func(s *Store) { s.Calibration() }},
		{"learned list", learnedListTables, func(s *Store) { s.OwnerLearned() }},
	}
}

// richAgentID is the session of richReadStore that the agent read is about.
const richAgentID = "s_readcase"

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
	// A session of a fixed ID, for the reads of one agent.
	if _, err := s.UpsertSession(node, protocol.SessionInfo{SessionID: richAgentID, Harness: "opencode", NativeID: "rc",
		Name: "rc", Cwd: "/w/rc", Repo: protocol.RepoInfo{Project: "cogforge"}}); err != nil {
		t.Fatal(err)
	}
	// Learned suggestions: one linked to a charter change and viewed, one
	// pending draft. The phrase cleaner is on, as in production.
	id := insertLearned(t, s, "cogforge", "purpose", "", "add", "learn things", "outcome")
	// Ready and major: the list reads the history and the view of it.
	if _, err := s.db.Exec(`UPDATE learned_suggestions SET severity='major', ready_at=1 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	var hist int64
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(id),0) FROM graph_history`).Scan(&hist); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE learned_suggestion_details SET history_id=? WHERE suggestion_id=?`, hist, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO learned_views(node,last_seen_id) VALUES('cogforge',?)`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO charter_drafts(level,scope,attr,value,source,status,drafted_by,created_at) VALUES('repo','cogforge','maturity','draft','outcome','pending','lead',1)`); err != nil {
		t.Fatal(err)
	}
	s.phraseCleaner = phrasing.New(nil)
	// A finished run, so the worker log looks up its assessment and notice.
	if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: oc1, Harness: "opencode", Model: "p/m",
		Outcome: protocol.OutcomeComplete, EndedAt: s.now().Unix(), Caller: oc2, Difficulty: "hard"}); err != nil {
		t.Fatal(err)
	}
	return s
}

// observed runs f and returns the tables and the table.column names its
// statements read, and the statements the connector could not describe.
func observed(t *testing.T, s *Store, f func()) (reads, cols map[string]bool, unknown []string) {
	t.Helper()
	reads, cols = map[string]bool{}, map[string]bool{}
	s.connector.SetObserver(func(st sqlitecache.Statement) {
		if !st.Known {
			unknown = append(unknown, st.Query)
		}
		for _, r := range st.Reads {
			reads[r] = true
		}
		for _, c := range st.ReadColumns {
			cols[c] = true
		}
	})
	defer s.connector.SetObserver(nil)
	f()
	return reads, cols, unknown
}

// undeclared returns the reads the declared keys do not name. A table read
// through some columns is named by the table or by each column. A table read
// through no column (a count of rows) is named by any key of the table: rows
// are counted by the table's inserts and deletes, which every key sees.
func undeclared(declared []string, reads, cols map[string]bool) []string {
	have := map[string]bool{}
	for _, d := range declared {
		have[d] = true
	}
	colsOf := map[string][]string{}
	for c := range cols {
		tbl, col, _ := strings.Cut(c, ".")
		colsOf[tbl] = append(colsOf[tbl], col)
	}
	var missing []string
	for tbl := range reads {
		if have[tbl] {
			continue
		}
		if len(colsOf[tbl]) == 0 {
			named := false
			for _, d := range declared {
				if strings.HasPrefix(d, tbl+".") {
					named = true
				}
			}
			if !named {
				missing = append(missing, tbl)
			}
			continue
		}
		for _, col := range colsOf[tbl] {
			if !have[tbl+"."+col] {
				missing = append(missing, tbl+"."+col)
			}
		}
	}
	sort.Strings(missing)
	return missing
}

func TestCachedReadsDeclareEveryTableTheyRead(t *testing.T) {
	check := func(t *testing.T, s *Store) {
		for _, c := range readCases() {
			s.rc = readCache{}
			reads, cols, unknown := observed(t, s, func() { c.run(s) })
			if len(unknown) > 0 {
				t.Errorf("%s: the connector could not tell the tables of: %.100s", c.name, unknown[0])
			}
			if len(reads) == 0 {
				t.Fatalf("%s: no statement was seen", c.name)
			}
			if missing := undeclared(c.tables, reads, cols); len(missing) > 0 {
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

// The sets name tables and columns that exist, spelled in lower case, once each.
func TestCachedReadTableSetsNameRealTables(t *testing.T) {
	s, _ := newTestStore(t)
	real := map[string]map[string]bool{}
	rows, err := s.db.Query(`SELECT m.name, p.name FROM sqlite_master m JOIN pragma_table_info(m.name) p WHERE m.type='table'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var tbl, col string
		rows.Scan(&tbl, &col)
		if real[tbl] == nil {
			real[tbl] = map[string]bool{}
		}
		real[tbl][col] = true
	}
	rows.Close()
	for _, c := range readCases() {
		seen := map[string]bool{}
		for _, key := range c.tables {
			tbl, col, dotted := strings.Cut(key, ".")
			if real[tbl] == nil {
				t.Errorf("%s declares %q, which is no table", c.name, key)
			} else if dotted && !real[tbl][col] {
				t.Errorf("%s declares %q, which is no column of %s", c.name, key, tbl)
			}
			if seen[key] || key != strings.ToLower(key) {
				t.Errorf("%s declares %q twice or not in lower case", c.name, key)
			}
			seen[key] = true
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

// jsonColumns are the columns that hold JSON, which the reads decode or a
// CHECK constraint validates.
var jsonColumns = map[string]string{"options": "[]", "origin": "{}", "fields": "{}", "reasons": "[]", "capabilities": "[]", "evidence": "[]"}

// checkedColumns are the table.column values a CHECK constraint accepts, for the
// columns the generic row cannot guess.
var checkedColumns = map[string]string{"delivery_changes.state": "open", "worker_runs.early_quit": "0", "worker_ratings.score": "3",
	"learned_suggestions.status": "pending", "learned_suggestions.severity": "minor", "learned_suggestions.kind": "add", "charter_drafts.level": "repo", "charter_drafts.status": "pending"}

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
		switch {
		case jsonColumns[c.name] != "":
			args = append(args, jsonColumns[c.name])
		case checkedColumns[table+"."+c.name] != "":
			args = append(args, checkedColumns[table+"."+c.name])
		case strings.Contains(typ, "INT") || strings.Contains(typ, "BOOL"):
			args = append(args, n)
		default:
			args = append(args, fmt.Sprintf("zz%d", n))
		}
	}
	_, err = s.db.Exec(`INSERT INTO "`+table+`"(`+strings.Join(cols, ",")+`) VALUES(`+strings.Join(marks, ",")+`)`, args...)
	return err
}

// writeKey changes what a declared key names: a table gets a row, and a
// table.column gets a changed value in its rows (a value that did not change
// moves no column, see sqlitecache.Stamp).
func writeKey(t *testing.T, s *Store, key string, n int) {
	t.Helper()
	tbl, col, dotted := strings.Cut(key, ".")
	if !dotted {
		writeTo(t, s, key, n)
		return
	}
	before := s.connector.Stamp(key)
	q := fmt.Sprintf(`UPDATE "%[1]s" SET "%[2]s" = CASE WHEN typeof("%[2]s") IN ('integer','real') THEN "%[2]s"+1 ELSE coalesce("%[2]s",'')||'probe' END`, tbl, col)
	if _, err := s.db.Exec(q); err != nil {
		t.Fatalf("write to %s: %v", key, err)
	}
	if s.connector.Stamp(key) == before {
		t.Fatalf("a write that changed %s did not move its stamp", key)
	}
}

// writeRow is how the tests change a table. A table the generic row does not
// fit has its own statement here.
var writeRowSQL = map[string]string{
	"setting_revision":     `UPDATE setting_revision SET revision=revision+1`,
	"setting_values":       `INSERT INTO setting_values(key,level,scope,item,value,revision) VALUES('probe','repo','probe','','"x"',1)`,
	"charter_values":       `INSERT INTO charter_values(level,scope,attr,value,updated_at,updated_by) VALUES('global','','probe','x',1,'t')`,
	"apps":                 `INSERT INTO apps(id,name,kind,client_id,client_name,created_at) VALUES('app_probe','probe','owner_app','c','c',1)`,
	"teams":                `INSERT INTO teams(id,name,created_at,updated_at) VALUES('t_probe','probe',1,1)`,
	"team_members":         `INSERT INTO team_members(member_kind,member_id,team_id) VALUES('repo','probe','t_probe')`,
	"worker_brief_defects": `INSERT INTO worker_brief_defects(severity,at) VALUES(1,1)`,
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
			for i, key := range c.tables {
				i, key := i, key
				t.Run(key, func(t *testing.T) {
					writeKey(t, s, key, i)
					if n := statements(s, func() { c.run(s) }); n == 0 {
						t.Errorf("a write to %s did not make the read load again", key)
					}
					// And it is kept again.
					if n := statements(s, func() { c.run(s) }); n != 0 {
						t.Errorf("after the write to %s the read was not kept (%d statements)", key, n)
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

// A session.upsert rewrites the status and the last_seen of a session on every
// report. The graph shows four columns of sessions, so a report that changes
// none of them keeps the graph, and the agent list (which shows the status)
// loads again.
func TestCachedGraphSurvivesAnUpsertThatChangesNoGraphColumn(t *testing.T) {
	s := richReadStore(t)
	graph, agents := readCases()[1], readCases()[2]
	graph.run(s)
	agents.run(s)
	in := protocol.SessionInfo{Harness: "opencode", NativeID: "a", Name: "a", Cwd: "/w/a", Status: "busy",
		Repo: protocol.RepoInfo{Project: "cogforge"}}
	node := mustNodeID(t, s)
	if _, err := s.UpsertSession(node, in); err != nil {
		t.Fatal(err)
	}
	if n := statements(s, func() { graph.run(s) }); n != 0 {
		t.Fatalf("the graph loaded again after an upsert that changed no graph column (%d statements)", n)
	}
	if n := statements(s, func() { agents.run(s) }); n == 0 {
		t.Fatal("the agent list was kept after an upsert that changed its status")
	}
}

// The same report with a new repo root changes a graph column: the graph loads
// again, and so does the graph after a new session.
func TestCachedGraphReloadsWhenASessionChangesItsRepoOrIsNew(t *testing.T) {
	s := richReadStore(t)
	graph := readCases()[1]
	graph.run(s)
	node := mustNodeID(t, s)
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: "a", Name: "a", Cwd: "/w/a",
		Repo: protocol.RepoInfo{Project: "cogforge", Root: "/w/a"}}); err != nil {
		t.Fatal(err)
	}
	if n := statements(s, func() { graph.run(s) }); n == 0 {
		t.Fatal("the graph was kept after a session changed its repo root")
	}
	graph.run(s)
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: "new", Name: "new", Cwd: "/w/new",
		Repo: protocol.RepoInfo{Project: "fresh"}}); err != nil {
		t.Fatal(err)
	}
	if n := statements(s, func() { graph.run(s) }); n == 0 {
		t.Fatal("the graph was kept after a new session")
	}
}

// A write to a column the graph does not read leaves the graph alone.
func TestCachedGraphSurvivesWritesToOtherColumnsOfSessions(t *testing.T) {
	s := richReadStore(t)
	graph := readCases()[1]
	graph.run(s)
	if _, err := s.db.Exec(`UPDATE sessions SET last_seen = last_seen + 1, status = 'idle'`); err != nil {
		t.Fatal(err)
	}
	if n := statements(s, func() { graph.run(s) }); n != 0 {
		t.Fatalf("the graph loaded again after a write to last_seen and status (%d statements)", n)
	}
}

func mustNodeID(t *testing.T, s *Store) string {
	t.Helper()
	var node string
	if err := s.db.QueryRow(`SELECT id FROM nodes LIMIT 1`).Scan(&node); err != nil {
		t.Fatal(err)
	}
	return node
}
