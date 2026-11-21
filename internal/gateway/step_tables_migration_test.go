package gateway

import (
	"context"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The step tables rework (owner review 2026-11-21) moves the facts of six
// tables into columns, deletes the tasks that are done and drops the tables.
// This seeds an older database with a row in every table and with done tasks,
// replays the newest migration, and checks where each fact landed.
func TestStepTablesMigrationMovesEachFact(t *testing.T) {
	s := migrateOnly(t, t.TempDir()+"/old.db", migrationVersion(t, stepTablesMarker)-1)
	defer s.Close()
	for _, q := range []string{
		`INSERT INTO repos(id,display_name,created_at,updated_at) VALUES('demo','demo',1,1)`,
		`INSERT INTO sessions(id,node_id,harness,native_id,last_seen) VALUES('s_merger','n1','claude','native-1',1),('s_one','n1','claude','native-2',1),('s_two','n1','claude','native-3',1)`,
		`INSERT INTO project_bar_next(project,next) VALUES('demo',5),('norepo',9)`,
		`INSERT INTO merge_train_fails(project,fails,last_run,updated_at) VALUES('demo',3,'run-7',1)`,
		`INSERT INTO project_mergers(project,node_id,session_id) VALUES('demo','n1','s_merger')`,
		`INSERT INTO caller_step_escalations(owner_key,woken_at,lead_offered) VALUES('agent-x',1234,1),('s_solo',5678,0)`,
		`INSERT INTO delivery_changes(id,project,machine,branch,head_commit,revision,origin,state,created_at) VALUES('ch_1','demo','n1','b','h',1,'{}','open',1)`,
		// One open step with two notices and two delegates, and two done steps.
		`INSERT INTO delivery_step_tasks(change_id,revision,step,owner_node,created_at,completed) VALUES
			('ch_1',1,'review','/delegate',1,0),('ch_1',1,'accept','/caller',1,1),('ch_1',1,'test','/merger',1,1)`,
		`INSERT INTO messages(id,conversation_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state) VALUES
			('m_one','c','agent-net','s_one','notification','x',0,'step_1',1,9,'stored'),
			('m_two','c','agent-net','s_two','notification','x',0,'step_2',1,9,'delivered'),
			('m_done','c','agent-net','s_one','notification','x',0,'step_3',1,9,'stored'),
			('m_shared','c','agent-net','s_one','notification','x',0,'step_4',1,9,'stored')`,
		`INSERT INTO delivery_step_messages(change_id,revision,step,for_step,after_merge,session_id,message_id) VALUES
			('ch_1',1,'review','',0,'s_one','m_one'),('ch_1',1,'review','',0,'s_two','m_two'),
			('ch_1',1,'accept','',0,'s_one','m_done'),
			('ch_1',1,'accept','',0,'s_two','m_shared'),('ch_1',1,'test','',0,'s_two','m_shared')`,
		`INSERT INTO delivery_step_delegates(change_id,revision,step,for_step,after_merge,node) VALUES
			('ch_1',1,'review','',0,'agent:lead'),('ch_1',1,'review','',0,'other')`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	if err := replayMigrations(t, s); err != nil {
		t.Fatal(err)
	}

	str := func(query string, args ...any) string {
		t.Helper()
		var v string
		if err := s.db.QueryRow(query, args...).Scan(&v); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return v
	}
	for _, f := range []struct{ name, query, want string }{
		{"the bar counter", `SELECT CAST(bar_next AS TEXT) FROM repos WHERE id='demo'`, "5"},
		{"a counter with no repo gets a repo", `SELECT CAST(bar_next AS TEXT) FROM repos WHERE id='norepo'`, "9"},
		{"the train fail count", `SELECT CAST(train_fails AS TEXT) FROM repos WHERE id='demo'`, "3"},
		{"the train's last run", `SELECT train_last_run FROM repos WHERE id='demo'`, "run-7"},
		{"the merger's node", `SELECT merger_node FROM repos WHERE id='demo'`, "n1"},
		{"the merger's session", `SELECT merger_session FROM repos WHERE id='demo'`, "s_merger"},
		{"an escalation that offered the lead", `SELECT value||':'||updated_at FROM gateway_state WHERE key='caller_escalation:agent-x'`, "1:1234"},
		{"an escalation that only woke", `SELECT value||':'||updated_at FROM gateway_state WHERE key='caller_escalation:s_solo'`, "0:5678"},
		{"the notices of the open task", `SELECT json_extract(notices,'$.s_one')||json_extract(notices,'$.s_two') FROM delivery_step_tasks WHERE step='review'`, "m_onem_two"},
		{"the delegates of the open task", `SELECT delegates FROM delivery_step_tasks WHERE step='review'`, `["agent:lead","other"]`},
		{"only open work stays", `SELECT group_concat(step) FROM delivery_step_tasks`, "review"},
		{"a notice that only done tasks pointed at is expired", `SELECT state FROM messages WHERE id='m_done'`, "expired"},
		{"a notice shared by two done tasks is expired", `SELECT state FROM messages WHERE id='m_shared'`, "expired"},
		{"a delivered notice stays delivered", `SELECT state FROM messages WHERE id='m_two'`, "delivered"},
		{"an undelivered notice of the open task stays", `SELECT state FROM messages WHERE id='m_one'`, "stored"},
	} {
		if got := str(f.query); got != f.want {
			t.Errorf("%s: got %q, want %q", f.name, got, f.want)
		}
	}
	for _, table := range []string{"delivery_step_messages", "delivery_step_delegates", "caller_step_escalations", "project_bar_next", "merge_train_fails", "project_mergers"} {
		if n := str(`SELECT CAST(COUNT(*) AS TEXT) FROM sqlite_master WHERE type='table' AND name=?`, table); n != "0" {
			t.Errorf("table %s is still there", table)
		}
	}
	if n := str(`SELECT CAST(COUNT(*) AS TEXT) FROM pragma_table_info('delivery_step_tasks') WHERE name='completed'`); n != "0" {
		t.Error("delivery_step_tasks still has the completed column")
	}
	// The merger's triggers now follow the repo: deleting its session ends the
	// registration and returns the repo's merger work to the caller.
	if _, err := s.db.Exec(`UPDATE delivery_step_tasks SET owner_node='/merger' WHERE step='review'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE id='s_merger'`); err != nil {
		t.Fatal(err)
	}
	if got := str(`SELECT merger_session||'|'||owner_node||'|'||notices FROM repos, delivery_step_tasks WHERE repos.id='demo'`); got != "|/caller|{}" {
		t.Errorf("after the merger's session was deleted: %q", got)
	}
}

// A step that is done leaves no task and no notice behind: its evidence is the
// record. Before the step tables rework the task stayed with a done flag and
// its notices stayed in a table for ever. A notice that another open task of
// the same message still points at stays until that task ends too.
func TestDoneStepLeavesNoTaskOrNoticeBehind(t *testing.T) {
	r := newDeliveryRig(t)
	callerNeedsSharedPipeline(t, r)
	id := r.submit(r.a, r.workerSubmit("feature/done", commitA)).Change.Change.ID
	r.g.dispatchStepTasks(context.Background())
	var notice, state string
	if err := r.s.db.QueryRow(`SELECT n.message_id,m.state FROM `+stepNoticesSQL+` n JOIN messages m ON m.id=n.message_id WHERE n.change_id=? AND n.step='accept'`, id).Scan(&notice, &state); err != nil || state != "stored" {
		t.Fatalf("accept notice before the report = %q %q, %v; want one stored notice", notice, state, err)
	}
	count := func(name, query string, want int) {
		t.Helper()
		var n int
		if err := r.s.db.QueryRow(query, id).Scan(&n); err != nil || n != want {
			t.Errorf("%s = %d, %v; want %d", name, n, err, want)
		}
	}
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepAccept}, Result: protocol.StepResultPass, Commit: commitA})
	count("tasks of the accepted step", `SELECT count(*) FROM delivery_step_tasks WHERE change_id=? AND step='accept'`, 0)
	count("notices of the accepted step", `SELECT count(*) FROM `+stepNoticesSQL+` WHERE change_id=? AND step='accept'`, 0)
	count("evidence of the accepted step", `SELECT count(*) FROM delivery_evidence WHERE change_id=? AND step='accept' AND result='pass'`, 1)
	// The notice named the next step too, so the task of that step still holds it.
	count("notice held by the next step", `SELECT count(*) FROM `+stepNoticesSQL+` WHERE change_id=? AND message_id='`+notice+`'`, 1)

	// Closing the change ends the last tasks, and the notice expires with them.
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepMerge}, Result: protocol.StepResultClose, Commit: commitA, Note: "not needed"})
	count("tasks after the close", `SELECT count(*) FROM delivery_step_tasks WHERE change_id=?`, 0)
	count("undelivered copies of the notice", `SELECT count(*) FROM messages WHERE id='`+notice+`' AND state IN ('stored','dispatched')`, 0)
}
