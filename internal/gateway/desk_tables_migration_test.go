package gateway

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Desk tables rework (owner review 2026-11-21): fold cto_intake,
// delivery_step_inbox and ask_sort_hide_receipts into inbox_items and desk_log,
// keep one status field on inbox_items, and drop dup_of and back_note.

const deskTablesReworkMarker = "desk tables rework"

// recreateDeskDroppedTables restores the tables and inbox_items columns that
// deskTablesMigration dropped, for tests that seed old rows or rewind
// schema_version before it.
func recreateDeskDroppedTables(t *testing.T, db repoDB) {
	t.Helper()
	restoreTablesAt(t, db, migrationVersion(t, deskTablesMarker)-1, "cto_intake", "delivery_step_inbox", "ask_sort_hide_receipts")
	var hasInbox int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='inbox_items'`).Scan(&hasInbox); err != nil {
		t.Fatal(err)
	}
	if hasInbox == 0 {
		return
	}
	for _, col := range []struct{ name, spec string }{
		{"state", `TEXT NOT NULL DEFAULT ''`},
		{"dup_of", `TEXT NOT NULL DEFAULT ''`},
		{"back_note", `TEXT NOT NULL DEFAULT ''`},
	} {
		var has int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('inbox_items') WHERE name=?`, col.name).Scan(&has); err != nil {
			t.Fatal(err)
		}
		if has == 0 {
			if _, err := db.Exec(`ALTER TABLE inbox_items ADD COLUMN ` + col.name + ` ` + col.spec); err != nil {
				t.Fatalf("restore inbox_items.%s: %v", col.name, err)
			}
		}
	}
}

func schemaSnapshot(t *testing.T, s *Store) (objects map[string]string, columns map[string][]string) {
	t.Helper()
	objects = map[string]string{}
	columns = map[string][]string{}
	rows, err := s.db.Query(`SELECT type, name, COALESCE(sql,'') FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var typ, name, stmt string
		if err := rows.Scan(&typ, &name, &stmt); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		objects[typ+":"+name] = strings.Join(strings.Fields(stmt), " ")
		if typ == "table" {
			tables = append(tables, name)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	for _, tbl := range tables {
		crows, err := s.db.Query(`SELECT name, type, "notnull", COALESCE(dflt_value,''), pk FROM pragma_table_info(?) ORDER BY cid`, tbl)
		if err != nil {
			t.Fatal(err)
		}
		for crows.Next() {
			var name, typ, dflt string
			var nn, pk int
			if err := crows.Scan(&name, &typ, &nn, &dflt, &pk); err != nil {
				crows.Close()
				t.Fatal(err)
			}
			columns[tbl] = append(columns[tbl], name+"|"+typ+"|"+dflt)
		}
		if err := crows.Err(); err != nil {
			crows.Close()
			t.Fatal(err)
		}
		crows.Close()
	}
	return objects, columns
}

func seedPreDeskReworkDB(t *testing.T, s *Store, now int64) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO nodes(id,hostname,os,token_hash,created_at) VALUES('n1','box','linux','h',1)`,
		`INSERT INTO sessions(id,node_id,harness,native_id,name,online,last_seen) VALUES
			('s_chief','n1','claude','nc','chief',1,1),
			('s_asker','n1','codex','na','asker',1,1)`,
		`INSERT OR REPLACE INTO gateway_state(key,value,updated_at) VALUES('chief_session','s_chief',1)`,
		`INSERT INTO agents(id,display_name,created_at,updated_at) VALUES('a-net','agent-net lead',0,0)`,
		`INSERT INTO repos(id,display_name,created_at,updated_at,lead_agent_id) VALUES('agent-net','agent-net',0,0,'a-net')`,
		`INSERT INTO delivery_changes(id,project,machine,branch,head_commit,revision,origin,title,state,created_at,updated_at)
			VALUES('ch_mig','demo','n1','feat','deadbeef',2,'{}','Migrate title','open',1,1)`,
		// Existing inbox_items covering each status/state/back_note and targets for alert_ask, stuck_ask, delivery_step_inbox.
		`INSERT INTO inbox_items(id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status,answer,answered_at,options,asker_name,change_title,from_kind,source_ref,about_kind,about_id,next_who,why_you,detail,priority,owner,state,category,back_note) VALUES
			('i_alert_1','cto_alert','agent-net','','CTO alert: box went silent','','','',10,'open','',0,'[]','agent-net','','gateway','cto-alert:ci_alert','machine','n1','chief','Lead that owns the cause: agent-net lead.','CTO intake entry ci_alert',1,'chief','verifying','outage',''),
			('i_stuck_1','quota_stuck','agent-net','','Work is stuck: zai exhausted','','','',20,'open','',0,'[]','agent-net','','gateway','quota-stuck:zai','','','chief','Get the work moving','CTO intake entry ci_stuck',0,'chief','verifying','normal',''),
			('i_step_1','approval','s_asker','demo','Approve merge for ch_mig','','','',30,'open','',0,'[]','asker','Migrate title','session','','','','owner','','',0,'chief','ready','normal',''),
			('i_carried','question','s_asker','demo','Rotate key?','','','',40,'answered','yes',45,'["yes","no"]','asker','','session','','','','owner','','',0,'chief','carried_out','normal',''),
			('i_back_1','question','s_asker','demo','Back with detail?','','','',50,'open','',0,'[]','asker','','session','','','','chief','','original detail',0,'chief','verifying','normal','back from carol: offline for 12 h'),
			('i_back_2','question','s_asker','demo','Back empty detail?','','','',55,'open','',0,'[]','asker','','session','','','','chief','','',0,'chief','verifying','normal','only back note'),
			('i_closed','question','s_asker','demo','Closed ask','','','',60,'closed','done',65,'[]','asker','','session','','','','owner','','',0,'chief','closed','normal',''),
			('i_replaced','question','s_asker','demo','Replaced ask','','','',70,'replaced','',75,'[]','asker','','session','','','','owner','','',0,'chief','closed','normal',''),
			('i_approved','approval','s_asker','demo','Approved ask','','','',80,'approved','approve',85,'[]','asker','','session','','','','owner','','',0,'chief','answered','normal',''),
			('i_dismissed','question','s_asker','demo','Dismissed ask','','','',90,'dismissed','',95,'[]','asker','','session','','','','owner','','',0,'chief','closed','normal','')`,
		// carried_out is set by UPDATE state='carried_out' after insert (the insert trigger sets state from status).
		`UPDATE inbox_items SET state='carried_out' WHERE id='i_carried'`,
		// cto_intake rows: quiet open digest, closed entry, open with alert_ask, open with stuck_ask.
		`INSERT INTO cto_intake(id,kind,subject,severity,text,detail,first_seen,last_seen,count,state,alert_ask,stuck_ask,lead,closed_at,close_reason) VALUES
			('ci_quiet','phone_paired','phone-1',3,'Paired phone-1','detail-q',100,200,2,'open','','','',0,''),
			('ci_closed','quota','old-prov',2,'old-prov ran out','',50,60,3,'closed','','','',70,'resolved'),
			('ci_alert','machine_silent','n1',1,'box went silent','silent detail',10,15,1,'open','i_alert_1','','agent-net lead',0,''),
			('ci_stuck','quota','zai',2,'zai exhausted','',20,25,2,'open','','i_stuck_1','agent-net lead',0,'')`,
		// delivery_step_inbox row.
		`INSERT INTO delivery_step_inbox(change_id,revision,step,for_step,after_merge,commit_hash,item_id)
			VALUES('ch_mig',2,'approve','merge',0,'deadbeef','i_step_1')`,
		// ask_sorts + ask_sort_hide_receipts (one active, one already undone).
		`INSERT INTO ask_sorts(id,ask_id,at,sorter,result,reason,hidden_at) VALUES
			(10,'i_closed',65,'s_chief','close','done',100),
			(11,'i_closed',66,'s_chief','close','done',100),
			(12,'i_closed',67,'s_chief','close','done',0)`,
		`INSERT INTO ask_sort_hide_receipts(id,ids,undone) VALUES
			('rcpt_active','[10,11]',0),
			('rcpt_undone','[12]',1)`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	_ = now
}

func TestDeskTablesMigrationCopiesAndDrops(t *testing.T) {
	v := migrationVersion(t, deskTablesReworkMarker)
	path := filepath.Join(t.TempDir(), "old.db")
	s := migrateOnly(t, path, v-1)
	defer s.Close()
	seedPreDeskReworkDB(t, s, s.now().UnixNano())

	if err := replayMigrations(t, s); err != nil {
		t.Fatal(err)
	}

	// Dropped tables are gone.
	for _, tbl := range []string{"cto_intake", "delivery_step_inbox", "ask_sort_hide_receipts"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&n); err != nil || n != 0 {
			t.Errorf("table %s still exists (%d, %v)", tbl, n, err)
		}
	}

	// Dropped columns are gone; new dedupe columns exist on inbox_items.
	cols := map[string]bool{}
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info('inbox_items')`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		cols[name] = true
	}
	rows.Close()
	for _, gone := range []string{"state", "dup_of", "back_note"} {
		if cols[gone] {
			t.Errorf("column inbox_items.%s still exists", gone)
		}
	}
	for _, want := range []string{"dedupe_key", "count", "first_seen", "last_seen", "severity", "override_facts"} {
		if !cols[want] {
			t.Errorf("column inbox_items.%s missing", want)
		}
	}

	// cto_intake migrated: 3 open entries, alert_ask and stuck_ask merged (not duplicated).
	intake, err := s.CTOIntake(0)
	if err != nil {
		t.Fatal(err)
	}
	if intake.Total != 3 || len(intake.Entries) != 3 {
		t.Fatalf("CTOIntake = %+v, want 3 open entries", intake)
	}
	byKey := map[string]protocol.IntakeEntry{}
	for _, e := range intake.Entries {
		byKey[e.Kind+":"+e.Subject] = e
	}
	if e := byKey["phone_paired:phone-1"]; e.ID != "ci_quiet" || e.Count != 2 || e.Severity != 3 || e.Alert != "" || e.Stuck != "" {
		t.Errorf("quiet entry = %+v", e)
	}
	if e := byKey["machine_silent:n1"]; e.ID != "i_alert_1" || e.Alert != "i_alert_1" || e.Severity != 1 || e.Count != 1 {
		t.Errorf("merged alert entry = %+v", e)
	}
	if e := byKey["quota:zai"]; e.ID != "i_stuck_1" || e.Stuck != "i_stuck_1" || e.Severity != 2 || e.Count != 2 {
		t.Errorf("merged stuck entry = %+v", e)
	}
	var dupRows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE id IN ('ci_alert','ci_stuck')`).Scan(&dupRows); err != nil || dupRows != 0 {
		t.Errorf("alert_ask/stuck_ask created %d second rows, want 0 (%v)", dupRows, err)
	}
	var closedStatus, closedAnswer string
	var closedCount int
	if err := s.db.QueryRow(`SELECT status,answer,count FROM inbox_items WHERE id='ci_closed'`).Scan(&closedStatus, &closedAnswer, &closedCount); err != nil {
		t.Fatalf("closed cto_intake row missing: %v", err)
	}
	if closedStatus != "closed" || closedAnswer != "resolved" || closedCount != 3 {
		t.Errorf("closed cto_intake row = %q %q %d", closedStatus, closedAnswer, closedCount)
	}

	// delivery_step_inbox migrated to about_kind='step' and ListAsks/ListInbox expose Delivery.
	var aboutKind, aboutID string
	if err := s.db.QueryRow(`SELECT about_kind,about_id FROM inbox_items WHERE id='i_step_1'`).Scan(&aboutKind, &aboutID); err != nil {
		t.Fatal(err)
	}
	if aboutKind != "step" || aboutID != "ch_mig:2:approve:merge:0:deadbeef" {
		t.Errorf("step ask about = %q %q", aboutKind, aboutID)
	}
	asks, err := s.ListAsks()
	if err != nil {
		t.Fatal(err)
	}
	foundStep := false
	for _, a := range asks {
		if a.ID == "i_step_1" {
			foundStep = true
			if a.AboutKind != protocol.AskAboutChange || a.AboutID != "ch_mig" || a.Delivery == nil || a.Delivery.Commit != "deadbeef" || a.Delivery.Step != "approve" || a.Delivery.For != "merge" {
				t.Errorf("ListAsks step item = %+v", a)
			}
		}
		if a.ID == "ci_quiet" {
			t.Errorf("quiet cto_intake row leaked into ListAsks: %+v", a)
		}
	}
	if !foundStep {
		t.Errorf("i_step_1 missing from ListAsks")
	}

	// inbox_items single status field + back_note moved to detail.
	for id, want := range map[string]string{
		"i_carried":   protocol.DeskCarriedOut,
		"i_closed":    "closed",
		"i_replaced":  "replaced",
		"i_approved":  "approved",
		"i_dismissed": "dismissed",
	} {
		var st string
		if err := s.db.QueryRow(`SELECT status FROM inbox_items WHERE id=?`, id).Scan(&st); err != nil || st != want {
			t.Errorf("%s status = %q (%v), want %q", id, st, err, want)
		}
	}
	var d1, d2 string
	if err := s.db.QueryRow(`SELECT detail FROM inbox_items WHERE id='i_back_1'`).Scan(&d1); err != nil || !strings.Contains(d1, "original detail") || !strings.Contains(d1, "back from carol: offline for 12 h") {
		t.Errorf("i_back_1 detail = %q (%v)", d1, err)
	}
	if err := s.db.QueryRow(`SELECT detail FROM inbox_items WHERE id='i_back_2'`).Scan(&d2); err != nil || d2 != "only back note" {
		t.Errorf("i_back_2 detail = %q (%v)", d2, err)
	}

	// ask_sort_hide_receipts moved to desk_log: active receipt undoes, undone receipt refused.
	if _, _, err := s.SortAsk("n1", protocol.AskSortRequest{SessionID: "s_chief", AskSortArgs: protocol.AskSortArgs{Result: "hide", Mode: "undo", Receipt: "rcpt_undone"}}); err == nil {
		t.Error("already-undone migrated receipt was accepted")
	}
	undoRes, _, err := s.SortAsk("n1", protocol.AskSortRequest{SessionID: "s_chief", AskSortArgs: protocol.AskSortArgs{Result: "hide", Mode: "undo", Receipt: "rcpt_active"}})
	if err != nil {
		t.Fatalf("undo migrated receipt: %v", err)
	}
	if undoRes.HiddenIDs == nil || !reflect.DeepEqual(*undoRes.HiddenIDs, []int64{10, 11}) {
		t.Errorf("undo hidden_ids = %+v, want [10 11]", undoRes.HiddenIDs)
	}

	// Replaying the migration is idempotent.
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version >= ?`, v); err != nil {
		t.Fatal(err)
	}
	if err := replayMigrations(t, s); err != nil {
		t.Fatalf("replay: %v", err)
	}
}

func TestDeskTablesFreshMatchesUpgradedSchema(t *testing.T) {
	v := migrationVersion(t, deskTablesReworkMarker)
	freshPath := filepath.Join(t.TempDir(), "fresh.db")
	fresh, err := openStore(freshPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()

	upPath := filepath.Join(t.TempDir(), "upgraded.db")
	up := migrateOnly(t, upPath, v-1)
	seedPreDeskReworkDB(t, up, up.now().UnixNano())
	if err := up.Close(); err != nil {
		t.Fatal(err)
	}
	up, err = openStore(upPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()

	freshObj, freshCols := schemaSnapshot(t, fresh)
	upObj, upCols := schemaSnapshot(t, up)
	if !reflect.DeepEqual(freshObj, upObj) {
		for k, v := range freshObj {
			if upObj[k] != v {
				t.Errorf("schema diff on %s:\nfresh:    %s\nupgraded: %s", k, v, upObj[k])
			}
		}
		for k, v := range upObj {
			if _, ok := freshObj[k]; !ok {
				t.Errorf("extra object in upgraded %s: %s", k, v)
			}
		}
	}
	if !reflect.DeepEqual(freshCols, upCols) {
		for k, v := range freshCols {
			if !reflect.DeepEqual(upCols[k], v) {
				t.Errorf("columns diff on %s:\nfresh:    %v\nupgraded: %v", k, v, upCols[k])
			}
		}
	}
}

// Exercises Desk operations across ask kinds and states after the rework:
// owner asks, sort (owner, send, close, merge, withdraw, done, undo, hide),
// inbox_list, asks_list, inbox_answer, chief_sorts, ask_history, ask_undo,
// desk_log, and cto_intake_find (single-row dedupe, wake, quota_stuck, close).
func TestDeskOperationsAfterRework(t *testing.T) {
	s, c, chief, asker, cto, node := deskRig(t)
	if _, err := s.db.Exec(`INSERT INTO agents(id,display_name,created_at,updated_at) VALUES('a-net','agent-net lead',0,0);
		INSERT INTO repos(id,display_name,created_at,updated_at,lead_agent_id) VALUES('agent-net','agent-net',0,0,'a-net')`); err != nil {
		t.Fatal(err)
	}

	// 1. Owner ask -> sort to owner -> inbox_list + asks_list + inbox_answer -> done (carried_out).
	q1, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: asker, Node: "demo", Text: "Ship v2?", Options: []string{"yes", "no"}, FromKind: protocol.AskFromSession})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{Ask: q1.ID, Result: protocol.AskSortOwner, Reason: "decision", Priority: 2, WhyYou: "only you"}}); err != nil {
		t.Fatal(err)
	}
	if asks, err := s.ListAsks(); err != nil || len(asks) != 1 || asks[0].DeskState != protocol.DeskReady {
		t.Fatalf("ListAsks = %+v, %v", asks, err)
	}
	if items, err := s.ListInbox(); err != nil || len(items) != 1 || items[0].ID != q1.ID {
		t.Fatalf("ListInbox = %+v, %v", items, err)
	}
	if _, err := s.AnswerInbox(q1.ID, "yes"); err != nil {
		t.Fatal(err)
	}
	if res, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{Ask: q1.ID, Result: protocol.AskSortDone, Reason: "shipped"}}); err != nil || res.State != protocol.DeskCarriedOut {
		t.Fatalf("done sort = %+v, %v", res, err)
	}

	// 2. Sort send to CTO -> chief_sorts + ask_history + ask_undo.
	q2, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: asker, Node: "demo", Text: "Upgrade Go?", Options: []string{"yes", "no"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{Ask: q2.ID, Result: protocol.AskSortSend, To: cto, Reason: "infra"}}); err != nil {
		t.Fatal(err)
	}
	if asks, err := s.ListAsks(); err != nil || len(asks) != 1 || asks[0].Owner != protocol.DeskOwnerCTO || asks[0].DeskState != protocol.DeskWithCTO {
		t.Fatalf("ListAsks with CTO = %+v, %v", asks, err)
	}
	if hist, err := s.AskSortHistory(q2.ID); err != nil || len(hist) != 1 {
		t.Fatalf("AskSortHistory = %+v, %v", hist, err)
	}
	if sorts, err := s.ChiefSorts(time.Time{}); err != nil || len(sorts) < 2 {
		t.Fatalf("ChiefSorts = %+v, %v", sorts, err)
	}
	if _, err := s.UndoAsk(q2.ID, "owner"); err != nil {
		t.Fatalf("UndoAsk: %v", err)
	}
	if asks, err := s.ListAsks(); err != nil || len(asks) != 1 || asks[0].Owner != protocol.DeskOwnerChief || asks[0].DeskState != protocol.DeskReady {
		t.Fatalf("ListAsks after undo = %+v, %v", asks, err)
	}

	// 3. Batch hide + undo via desk_log.
	stall := chiefAskKind(t, s, asker, "session_stall", "stalled")
	sortAsk(t, s, chief, node, protocol.AskSortArgs{Ask: stall, Result: protocol.AskSortClose, Reason: "handled"})
	hideArgs := protocol.AskSortArgs{Result: "hide", Mode: "preview", Kind: "session_stall", HideResult: "close", Before: c.now().Add(time.Hour).UTC().Format(time.RFC3339)}
	prev, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: hideArgs})
	if err != nil || prev.Token == "" {
		t.Fatalf("hide preview: %+v %v", prev, err)
	}
	hideArgs.Mode, hideArgs.Token = "apply", prev.Token
	app, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: hideArgs})
	if err != nil || app.Receipt == "" {
		t.Fatalf("hide apply: %+v %v", app, err)
	}
	if _, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{Result: "hide", Mode: "undo", Receipt: app.Receipt}}); err != nil {
		t.Fatalf("hide undo: %v", err)
	}
	if log, err := s.DeskLog(20); err != nil || len(log) == 0 {
		t.Fatalf("DeskLog = %+v, %v", log, err)
	}

	// 4. CTO intake lives in one inbox_items row through dedupe, wake and close.
	ev := intakeEvent{Kind: protocol.IntakeHarnessUpdate, Subject: "box/codex", Text: "codex update failed"}
	first, err := s.RecordIntake(ev)
	if err != nil {
		t.Fatal(err)
	}
	c.advance(61 * time.Minute)
	second, err := s.RecordIntake(ev)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.Count != 2 || second.Alert != first.ID || second.Severity != protocol.IntakeSeverityWake {
		t.Fatalf("woken intake = %+v, first %+v", second, first)
	}
	var ctoRows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE dedupe_key=?`, protocol.IntakeHarnessUpdate+":box/codex").Scan(&ctoRows); err != nil || ctoRows != 1 {
		t.Fatalf("intake created %d rows, want 1 (%v)", ctoRows, err)
	}
	if found, err := s.CTOIntake(0); err != nil || found.Total != 1 || found.Entries[0].Alert != first.ID {
		t.Fatalf("CTOIntake = %+v, %v", found, err)
	}
	if err := s.CloseIntake(protocol.IntakeHarnessUpdate, "box/codex", intakeResolved); err != nil {
		t.Fatal(err)
	}
	if found, err := s.CTOIntake(0); err != nil || found.Total != 0 {
		t.Fatalf("CTOIntake after close = %+v, %v", found, err)
	}
}
