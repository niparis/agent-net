package gateway

// stepTablesMarker names the migration in the migrate loop, which adds the
// columns below before the statement runs.
const stepTablesMarker = "delivery step tables rework"

// stepTablesColumns are the columns the rework adds. The loop adds each one
// only when it is missing, so a rewound database keeps the values it has.
var stepTablesColumns = []migrationColumn{
	{"repos", "bar_next", `INTEGER NOT NULL DEFAULT 0`},
	{"repos", "train_fails", `INTEGER NOT NULL DEFAULT 0`},
	{"repos", "train_last_run", `TEXT NOT NULL DEFAULT ''`},
	{"repos", "merger_node", `TEXT NOT NULL DEFAULT ''`},
	{"repos", "merger_session", `TEXT NOT NULL DEFAULT ''`},
	// notices maps a session id to the id of the notice that went to it.
	{"delivery_step_tasks", "notices", `TEXT NOT NULL DEFAULT '{}'`},
	// delegates lists the nodes that may report a delegated approval.
	{"delivery_step_tasks", "delegates", `TEXT NOT NULL DEFAULT '[]'`},
}

// stepTablesMigration moves the facts of six tables into columns, deletes the
// tasks that are done and drops the tables (owner review of 2026-11-21). A done
// task has its delivery_evidence row, so the task table keeps open work only.
// The merger registration moves to the repo: a repo has one merger, and the
// triggers that return its work when it ends follow it.
const stepTablesMigration = `
-- delivery step tables rework: copy each fact into its column, then drop the six tables.
-- A replay after a rewind can find a source table gone. An empty table copies
-- nothing, so the copy then does nothing.
CREATE TABLE IF NOT EXISTS delivery_step_messages (change_id TEXT NOT NULL, revision INTEGER NOT NULL, step TEXT NOT NULL, for_step TEXT NOT NULL, after_merge INTEGER NOT NULL, session_id TEXT NOT NULL, message_id TEXT NOT NULL, PRIMARY KEY(change_id, revision, step, for_step, after_merge, session_id));
CREATE TABLE IF NOT EXISTS delivery_step_delegates (change_id TEXT NOT NULL, revision INTEGER NOT NULL, step TEXT NOT NULL, for_step TEXT NOT NULL DEFAULT '', after_merge INTEGER NOT NULL DEFAULT 0, node TEXT NOT NULL, PRIMARY KEY(change_id, revision, step, for_step, after_merge, node));
CREATE TABLE IF NOT EXISTS caller_step_escalations (owner_key TEXT PRIMARY KEY, woken_at INTEGER NOT NULL, lead_offered INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS project_bar_next (project TEXT PRIMARY KEY, next INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS merge_train_fails (project TEXT PRIMARY KEY, fails INTEGER NOT NULL DEFAULT 0, last_run TEXT NOT NULL DEFAULT '', updated_at INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS project_mergers (project TEXT NOT NULL, node_id TEXT NOT NULL, session_id TEXT NOT NULL UNIQUE, PRIMARY KEY(project,node_id));
DROP TRIGGER IF EXISTS remove_project_merger;
INSERT OR IGNORE INTO repos(id,display_name,created_at,updated_at)
 SELECT project,project,0,0 FROM project_bar_next UNION SELECT project,project,0,0 FROM merge_train_fails UNION SELECT project,project,0,0 FROM project_mergers;
UPDATE repos SET bar_next=(SELECT next FROM project_bar_next WHERE project=repos.id) WHERE id IN (SELECT project FROM project_bar_next);
UPDATE repos SET train_fails=(SELECT fails FROM merge_train_fails WHERE project=repos.id), train_last_run=(SELECT last_run FROM merge_train_fails WHERE project=repos.id) WHERE id IN (SELECT project FROM merge_train_fails);
UPDATE repos SET merger_node=(SELECT node_id FROM project_mergers WHERE project=repos.id ORDER BY node_id LIMIT 1), merger_session=(SELECT session_id FROM project_mergers WHERE project=repos.id ORDER BY node_id LIMIT 1) WHERE id IN (SELECT project FROM project_mergers);
INSERT OR IGNORE INTO gateway_state(key,value,updated_at) SELECT 'caller_escalation:'||owner_key,CAST(lead_offered AS TEXT),woken_at FROM caller_step_escalations;
UPDATE delivery_step_tasks SET notices=(SELECT json_group_object(d.session_id,d.message_id) FROM delivery_step_messages d
  WHERE d.change_id=delivery_step_tasks.change_id AND d.revision=delivery_step_tasks.revision AND d.step=delivery_step_tasks.step AND d.for_step=delivery_step_tasks.for_step AND d.after_merge=delivery_step_tasks.after_merge)
 WHERE completed=0 AND EXISTS (SELECT 1 FROM delivery_step_messages d
  WHERE d.change_id=delivery_step_tasks.change_id AND d.revision=delivery_step_tasks.revision AND d.step=delivery_step_tasks.step AND d.for_step=delivery_step_tasks.for_step AND d.after_merge=delivery_step_tasks.after_merge);
UPDATE delivery_step_tasks SET delegates=(SELECT json_group_array(d.node) FROM delivery_step_delegates d
  WHERE d.change_id=delivery_step_tasks.change_id AND d.revision=delivery_step_tasks.revision AND d.step=delivery_step_tasks.step AND d.for_step=delivery_step_tasks.for_step AND d.after_merge=delivery_step_tasks.after_merge)
 WHERE completed=0 AND EXISTS (SELECT 1 FROM delivery_step_delegates d
  WHERE d.change_id=delivery_step_tasks.change_id AND d.revision=delivery_step_tasks.revision AND d.step=delivery_step_tasks.step AND d.for_step=delivery_step_tasks.for_step AND d.after_merge=delivery_step_tasks.after_merge);
-- A notice that only done tasks pointed at is stale: expire it before its link goes.
UPDATE messages SET state='expired' WHERE state IN ('stored','dispatched')
 AND id IN (SELECT d.message_id FROM delivery_step_messages d JOIN delivery_step_tasks t USING(change_id,revision,step,for_step,after_merge) WHERE t.completed=1)
 AND id NOT IN (SELECT d.message_id FROM delivery_step_messages d JOIN delivery_step_tasks t USING(change_id,revision,step,for_step,after_merge) WHERE t.completed=0);
-- A node that finished a step of a live change stays barred from its caller steps.
INSERT OR IGNORE INTO gateway_state(key,value,updated_at)
 SELECT DISTINCT 'step_node:'||t.change_id||'/'||t.owner_node,'1',0 FROM delivery_step_tasks t JOIN delivery_changes c ON c.id=t.change_id
 WHERE t.completed=1 AND t.owner_node NOT LIKE '/%' AND c.state IN ('open','merged');
DELETE FROM delivery_step_tasks WHERE completed=1;
DROP TABLE delivery_step_messages;
DROP TABLE delivery_step_delegates;
DROP TABLE caller_step_escalations;
DROP TABLE project_bar_next;
DROP TABLE merge_train_fails;
DROP TABLE project_mergers;
ALTER TABLE delivery_step_tasks DROP COLUMN completed;
CREATE UNIQUE INDEX IF NOT EXISTS repos_merger_session ON repos(merger_session) WHERE merger_session<>'';
-- A session that is deleted ends its merger registration the way an explicit stop does.
CREATE TRIGGER remove_project_merger AFTER DELETE ON sessions BEGIN
 UPDATE repos SET merger_session='',merger_node='' WHERE merger_session=OLD.id;
END;
-- Ending the registration returns the repo's merger work to the caller, and
-- replacing the session keeps the work and queues it for the new merger.
DROP TRIGGER IF EXISTS merger_removed;
DROP TRIGGER IF EXISTS merger_replaced;
CREATE TRIGGER merger_removed AFTER UPDATE OF merger_session ON repos
WHEN OLD.merger_session<>'' AND NEW.merger_session='' BEGIN
UPDATE messages SET state='expired' WHERE state IN ('stored','dispatched','failed') AND id IN (
SELECT d.value FROM delivery_step_tasks t JOIN delivery_changes c ON c.id=t.change_id JOIN json_each(t.notices) d
WHERE t.owner_node='/merger' AND c.project=OLD.id);
UPDATE delivery_step_tasks SET notices='{}',owner_node='/caller',claimed_by='',urgent=1
WHERE owner_node='/merger' AND change_id IN (SELECT c.id FROM delivery_changes c WHERE c.project=OLD.id);
INSERT OR IGNORE INTO graph_edges(source,target,kind,mode,rule,source_kind,target_kind)
SELECT '/delivery/caller',target,kind,mode,rule,'special','repo' FROM graph_edges
WHERE source='/delivery/merger' AND target=OLD.id AND kind<>'accepts';
DELETE FROM graph_edges WHERE source='/delivery/merger' AND target=OLD.id AND kind<>'accepts';
UPDATE setting_revision SET revision=revision+1 WHERE id=1;
END;
CREATE TRIGGER merger_replaced AFTER UPDATE OF merger_session ON repos
WHEN OLD.merger_session<>'' AND NEW.merger_session<>'' AND OLD.merger_session<>NEW.merger_session BEGIN
UPDATE messages SET state='expired' WHERE state IN ('stored','dispatched','failed') AND id IN (
SELECT d.value FROM delivery_step_tasks t JOIN delivery_changes c ON c.id=t.change_id JOIN json_each(t.notices) d
WHERE t.owner_node='/merger' AND c.project=OLD.id AND c.machine=OLD.merger_node);
UPDATE delivery_step_tasks SET notices='{}',owner_node='/merger',claimed_by='',urgent=1
WHERE owner_node='/merger' AND change_id IN (SELECT c.id FROM delivery_changes c WHERE c.project=OLD.id AND c.machine=OLD.merger_node);
END;
`
