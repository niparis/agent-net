package gateway

import "database/sql"

const deskTablesMarker = "desk tables rework"

// deskTablesColumns are the CTO intake columns added to inbox_items by the
// migrate loop before migrateDeskTablesTx runs.
var deskTablesColumns = []migrationColumn{
	{"inbox_items", "dedupe_key", `TEXT NOT NULL DEFAULT ''`},
	{"inbox_items", "count", `INTEGER NOT NULL DEFAULT 0`},
	{"inbox_items", "first_seen", `INTEGER NOT NULL DEFAULT 0`},
	{"inbox_items", "last_seen", `INTEGER NOT NULL DEFAULT 0`},
	{"inbox_items", "severity", `INTEGER NOT NULL DEFAULT 0`},
}

const deskTablesMigration = `
-- desk tables rework: fold cto_intake, delivery_step_inbox and ask_sort_hide_receipts
-- into inbox_items and desk_log, keep one status field, drop dup_of and back_note.
DROP TRIGGER IF EXISTS inbox_items_desk_insert;
DROP TRIGGER IF EXISTS inbox_items_desk_update;
DROP TRIGGER IF EXISTS inbox_items_joined_settle;
DROP INDEX IF EXISTS inbox_items_dup_of;
UPDATE inbox_items SET status='closed' WHERE status='joined';
CREATE TABLE IF NOT EXISTS cto_intake (id TEXT PRIMARY KEY, kind TEXT NOT NULL, subject TEXT NOT NULL, severity INTEGER NOT NULL, text TEXT NOT NULL DEFAULT '', detail TEXT NOT NULL DEFAULT '', first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL, count INTEGER NOT NULL DEFAULT 1, state TEXT NOT NULL DEFAULT 'open', alert_ask TEXT NOT NULL DEFAULT '', stuck_ask TEXT NOT NULL DEFAULT '', lead TEXT NOT NULL DEFAULT '', closed_at INTEGER NOT NULL DEFAULT 0, close_reason TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS delivery_step_inbox (change_id TEXT NOT NULL, revision INTEGER NOT NULL, step TEXT NOT NULL, for_step TEXT NOT NULL DEFAULT '', after_merge INTEGER NOT NULL DEFAULT 0, commit_hash TEXT NOT NULL, item_id TEXT NOT NULL UNIQUE, PRIMARY KEY(change_id, revision, step, for_step, after_merge));
CREATE TABLE IF NOT EXISTS ask_sort_hide_receipts (id TEXT PRIMARY KEY, ids TEXT NOT NULL, undone INTEGER NOT NULL DEFAULT 0);
UPDATE inbox_items SET owner='cto', dedupe_key=c.kind||':'||c.subject, count=c.count, first_seen=c.first_seen, last_seen=c.last_seen, severity=c.severity
 FROM cto_intake c WHERE inbox_items.id=c.alert_ask AND c.alert_ask<>'' AND (c.state<>'open' OR inbox_items.status='open')
 AND NOT EXISTS (SELECT 1 FROM inbox_items x WHERE x.dedupe_key=c.kind||':'||c.subject AND x.status='open' AND x.id<>inbox_items.id);
UPDATE inbox_items SET owner='cto', dedupe_key=c.kind||':'||c.subject, count=c.count, first_seen=c.first_seen, last_seen=c.last_seen, severity=c.severity
 FROM cto_intake c WHERE inbox_items.id=c.stuck_ask AND c.stuck_ask<>'' AND (c.state<>'open' OR inbox_items.status='open')
 AND (c.alert_ask='' OR NOT EXISTS (SELECT 1 FROM inbox_items a WHERE a.id=c.alert_ask AND a.dedupe_key=c.kind||':'||c.subject))
 AND NOT EXISTS (SELECT 1 FROM inbox_items x WHERE x.dedupe_key=c.kind||':'||c.subject AND x.status='open' AND x.id<>inbox_items.id);
INSERT INTO inbox_items (id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status,answer,answered_at,options,asker_name,change_title,from_kind,source_ref,about_kind,about_id,next_who,why_you,detail,priority,chief_sent_at,chief_at,route_fixed,owner,category,dedupe_key,count,first_seen,last_seen,severity)
 SELECT c.id,'cto_intake','agent-net',CASE WHEN c.kind='merge_queue_failing' THEN c.subject ELSE '' END,c.text,'','','',c.first_seen,c.state,c.close_reason,c.closed_at,'[]','agent-net','','gateway','',
  CASE WHEN c.kind='machine_silent' THEN 'machine' WHEN c.kind='merge_queue_failing' THEN 'project' ELSE '' END,
  CASE WHEN c.kind IN ('machine_silent','merge_queue_failing') THEN c.subject ELSE '' END,
  'cto','',c.detail,0,0,0,1,'cto',CASE WHEN c.kind='machine_silent' THEN 'outage' ELSE 'normal' END,
  c.kind||':'||c.subject,c.count,c.first_seen,c.last_seen,c.severity
 FROM cto_intake c
 WHERE NOT EXISTS (SELECT 1 FROM inbox_items i WHERE i.id=c.id)
  AND NOT EXISTS (SELECT 1 FROM inbox_items i WHERE i.id=c.alert_ask AND c.alert_ask<>'' AND i.dedupe_key=c.kind||':'||c.subject)
  AND NOT EXISTS (SELECT 1 FROM inbox_items i WHERE i.id=c.stuck_ask AND c.stuck_ask<>'' AND i.dedupe_key=c.kind||':'||c.subject);
UPDATE inbox_items SET about_kind='step', about_id=d.change_id||':'||d.revision||':'||d.step||':'||d.for_step||':'||d.after_merge||':'||d.commit_hash
 FROM delivery_step_inbox d WHERE inbox_items.id=d.item_id;
INSERT INTO desk_log(at,source,about_kind,about_id,text,detail,options)
 SELECT 0,'chief','hide_receipt',r.id,CASE WHEN r.undone<>0 THEN 'Undone: Hidden ask sorts' ELSE 'Hidden ask sorts' END,r.ids,CASE WHEN r.undone<>0 THEN '[]' ELSE '["hide_undo"]' END
 FROM ask_sort_hide_receipts r WHERE NOT EXISTS (SELECT 1 FROM desk_log l WHERE l.about_kind='hide_receipt' AND l.about_id=r.id);
DROP TABLE IF EXISTS cto_intake;
DROP TABLE IF EXISTS delivery_step_inbox;
DROP TABLE IF EXISTS ask_sort_hide_receipts;
`

const deskTablesPostSQL = `
CREATE TRIGGER inbox_items_desk_insert AFTER INSERT ON inbox_items WHEN NEW.owner<>'cto' BEGIN
 UPDATE inbox_items SET owner=` + deskOwnerSQL + ` WHERE id=NEW.id;
END;
CREATE TRIGGER inbox_items_desk_update AFTER UPDATE OF next_who ON inbox_items WHEN NEW.dedupe_key='' BEGIN
 UPDATE inbox_items SET owner=` + deskOwnerSQL + ` WHERE id=NEW.id;
END;
CREATE UNIQUE INDEX IF NOT EXISTS inbox_items_open_dedupe ON inbox_items(dedupe_key) WHERE dedupe_key<>'' AND status='open';
CREATE INDEX IF NOT EXISTS inbox_items_cto_seen ON inbox_items(status, last_seen) WHERE dedupe_key<>'';
`

func migrateDeskTablesTx(tx *sql.Tx) error {
	// dropDeskTablesMigration drops inbox_items and desk_log, so a replay
	// after it has nothing to migrate.
	if ok, err := hasTable(tx, "inbox_items"); err != nil || !ok {
		return err
	}
	if ok, err := hasTable(tx, "desk_log"); err != nil || !ok {
		return err
	}
	hasCol := func(name string) (bool, error) {
		var n int
		err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('inbox_items') WHERE name=?`, name).Scan(&n)
		return n > 0, err
	}
	for _, step := range [][2]string{
		{"state", `UPDATE inbox_items SET status='carried_out' WHERE state='carried_out'`},
		{"back_note", `UPDATE inbox_items SET detail=CASE WHEN detail='' THEN back_note ELSE detail||char(10)||back_note END WHERE back_note<>'' AND instr(detail,back_note)=0`},
	} {
		if ok, err := hasCol(step[0]); err != nil {
			return err
		} else if ok {
			if _, err := tx.Exec(step[1]); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(deskTablesMigration); err != nil {
		return err
	}
	for _, col := range []string{"state", "dup_of", "back_note"} {
		if ok, err := hasCol(col); err != nil {
			return err
		} else if ok {
			if _, err := tx.Exec(`ALTER TABLE inbox_items DROP COLUMN ` + col); err != nil {
				return err
			}
		}
	}
	_, err := tx.Exec(deskTablesPostSQL)
	return err
}
