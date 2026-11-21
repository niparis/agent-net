package gateway

// settingsFoldMarker names the migration; the migrate loop adds the columns first.
const settingsFoldMarker = "settings fold: one settings system"

var settingsFoldColumns = []migrationColumn{
	{"repos", "settings_token", `TEXT NOT NULL DEFAULT ''`},
	{"agents", "settings_token", `TEXT NOT NULL DEFAULT ''`},
	{"teams", "settings_token", `TEXT NOT NULL DEFAULT ''`},
}

// settingsFoldMigration leaves one settings system (decision 0182): see the
// decision for where each table's facts go.
const settingsFoldMigration = `
-- settings fold: one settings system.
INSERT INTO setting_history(revision,seq,key,level,scope,item,old_value,new_value,actor,reason,cause,at)
 SELECT revision,0,'revision','network','network','',NULL,'"bump"','agent-net','settings revision','cutover',0
 FROM setting_revision WHERE revision > (SELECT COALESCE(MAX(revision),0) FROM setting_history);
CREATE TEMP TABLE moved_settings (seq INTEGER PRIMARY KEY AUTOINCREMENT, key TEXT NOT NULL, value TEXT NOT NULL);
INSERT INTO moved_settings(key,value)
 SELECT 'owner.'||key, CASE WHEN key IN ('cto_title','time_zone') THEN json_quote(value) ELSE json(value) END
 FROM owner_settings
 WHERE key IN ('integrations','cto_settings','cto_title','protected_providers','worker_floors','model_scores','public','worker_profiles','worker_providers','difficulty_minimums','time_zone')
 AND (key IN ('cto_title','time_zone') OR (json_valid(value) AND value<>'null'))
 ORDER BY key;
INSERT OR IGNORE INTO setting_values(key,level,scope,item,value,revision)
 SELECT key,'network','network','',value,(SELECT COALESCE(MAX(revision),0)+1 FROM setting_history) FROM moved_settings;
INSERT INTO setting_history(revision,seq,key,level,scope,item,old_value,new_value,actor,reason,cause,at)
 SELECT (SELECT COALESCE(MAX(revision),0)+1 FROM setting_history),seq,key,'network','network','',NULL,value,'agent-net cutover','moved from owner_settings','cutover',CAST(strftime('%s','now') AS INTEGER)*1000000000
 FROM moved_settings;
DROP TABLE moved_settings;
INSERT OR IGNORE INTO gateway_state(key,value,updated_at)
 SELECT key,value,updated_at FROM owner_settings
 WHERE key IN ('cto_session','chief_session','chief_cleared','chief_silent_at','worker_rotation','onboarding','hidden_items') OR key LIKE 'chief_stuck_ping:%';
UPDATE repos SET settings_token=COALESCE((SELECT token FROM settings_target_identity t WHERE t.kind='repo' AND t.id=repos.id),'');
UPDATE agents SET settings_token=COALESCE((SELECT token FROM settings_target_identity t WHERE t.kind='agent' AND t.id=agents.id),'');
UPDATE teams SET settings_token=COALESCE((SELECT token FROM settings_target_identity t WHERE t.kind='team' AND t.id=teams.id),'');
INSERT OR IGNORE INTO gateway_state(key,value,updated_at) SELECT 'settings_network_token',token,0 FROM settings_target_identity WHERE kind='network' AND id='network';
DROP TRIGGER IF EXISTS settings_target_identity_repo_delete;
DROP TRIGGER IF EXISTS settings_target_identity_agent_delete;
DROP TRIGGER IF EXISTS settings_target_identity_agent_tombstone;
DROP TRIGGER IF EXISTS settings_token_agent_tombstone;
CREATE TRIGGER settings_token_agent_tombstone AFTER UPDATE OF tombstone ON agents WHEN NEW.tombstone<>0 BEGIN
 UPDATE agents SET settings_token='' WHERE id=NEW.id;
END;
DELETE FROM gateway_state WHERE key='graph_ref_backup';
DROP TRIGGER IF EXISTS merger_removed;
CREATE TRIGGER merger_removed AFTER DELETE ON project_mergers BEGIN
UPDATE messages SET state='expired' WHERE state IN ('stored','dispatched','failed') AND id IN (
SELECT d.message_id FROM delivery_step_messages d JOIN delivery_step_tasks t
USING(change_id,revision,step,for_step,after_merge)
JOIN delivery_changes c ON c.id=t.change_id
WHERE t.owner_node='/merger' AND t.completed=0 AND c.project=OLD.project);
DELETE FROM delivery_step_messages WHERE message_id IN (
SELECT d.message_id FROM delivery_step_messages d JOIN delivery_step_tasks t
USING(change_id,revision,step,for_step,after_merge)
JOIN delivery_changes c ON c.id=t.change_id
WHERE t.owner_node='/merger' AND t.completed=0 AND c.project=OLD.project);
UPDATE delivery_step_tasks SET owner_node='/caller',claimed_by='',urgent=1
WHERE owner_node='/merger' AND completed=0 AND change_id IN
(SELECT c.id FROM delivery_changes c WHERE c.project=OLD.project);
INSERT OR IGNORE INTO graph_edges(source,target,kind,mode,rule,source_kind,target_kind)
SELECT '/delivery/caller',target,kind,mode,rule,'special','repo' FROM graph_edges
WHERE source='/delivery/merger' AND target=OLD.project AND kind<>'accepts';
DELETE FROM graph_edges WHERE source='/delivery/merger' AND target=OLD.project AND kind<>'accepts';
DELETE FROM project_mergers WHERE project=OLD.project;
INSERT INTO setting_history(revision,seq,key,level,scope,item,old_value,new_value,actor,reason,cause,at)
 SELECT COALESCE(MAX(revision),0)+1,0,'revision','network','network','',NULL,'"bump"','agent-net','settings revision','cutover',0 FROM setting_history;
END;
DROP TABLE owner_settings;
DROP TABLE setting_revision;
DROP TABLE settings_target_identity;
`
