package gateway

// teamHistoryMigration moves the Team history into graph_history, the one
// history of the org tree. Each Team change is one graph_history row:
//   - a membership row names its two Teams in its values (new_value is the Team
//     it joined, old_value the Team it left), and node is the Team it is about;
//   - an undo of a Team change has type team_undo;
//   - a lead row is the lead row it always was.
//
// The rows that graph_history does not hold yet are copied, in their order,
// and team_history is dropped. The bot revoke triggers now write their
// membership row to graph_history too. A rewind can run this migration again
// after team_history was dropped, so the table is created first when missing.
const teamHistoryMigration = `
-- Team history moves into graph_history (schema reduction, part 9).
CREATE TABLE IF NOT EXISTS team_history (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 action TEXT NOT NULL,
 team_id TEXT NOT NULL,
 old_team_id TEXT NOT NULL DEFAULT '',
 member_kind TEXT NOT NULL DEFAULT '',
 member_id TEXT NOT NULL DEFAULT '',
 old_value TEXT NOT NULL,
 new_value TEXT NOT NULL,
 at INTEGER NOT NULL,
 actor TEXT NOT NULL
);
DROP TRIGGER IF EXISTS bot_agent_revoke;
CREATE TRIGGER bot_agent_revoke AFTER UPDATE OF revoked_at ON apps WHEN NEW.kind='bot' AND OLD.revoked_at IS NULL AND NEW.revoked_at IS NOT NULL BEGIN
 INSERT INTO graph_history(type,node,attr,old_value,new_value,at,actor,target_kind)
  SELECT 'team',team_id,'membership:agent:'||NEW.id,team_id,'',NEW.revoked_at,'apps','' FROM team_members WHERE member_kind='agent' AND member_id=NEW.id;
 DELETE FROM team_members WHERE member_kind='agent' AND member_id=NEW.id;
 UPDATE agents SET tombstone=1, updated_at=NEW.revoked_at WHERE id=NEW.id AND role='bot';
END;
DROP TRIGGER IF EXISTS bot_names_revoke;
CREATE TRIGGER bot_names_revoke AFTER UPDATE OF revoked_at ON apps WHEN NEW.kind='bot' AND OLD.revoked_at IS NULL AND NEW.revoked_at IS NOT NULL BEGIN
 INSERT INTO graph_history(type,node,attr,old_value,new_value,at,actor,target_kind)
  SELECT 'team',team_id,'membership:agent:'||member_id,team_id,'',NEW.revoked_at,'apps','' FROM team_members WHERE member_kind='agent' AND substr(member_id,1,length(NEW.id)+1)=NEW.id||'/';
 DELETE FROM team_members WHERE member_kind='agent' AND substr(member_id,1,length(NEW.id)+1)=NEW.id||'/';
 UPDATE agents SET tombstone=1, updated_at=NEW.revoked_at WHERE role='bot' AND substr(id,1,length(NEW.id)+1)=NEW.id||'/';
END;
-- A Team undo was written as a team row with the same time, actor and values.
UPDATE graph_history SET type='team_undo'
 WHERE type='team' AND EXISTS(SELECT 1 FROM team_history t WHERE t.action='undo' AND t.at=graph_history.at AND t.actor=graph_history.actor AND t.old_value=graph_history.old_value AND t.new_value=graph_history.new_value);
-- The rows graph_history does not hold, found before any insert so that the
-- copies cannot match each other.
DROP TABLE IF EXISTS temp.team_history_missing;
CREATE TEMP TABLE team_history_missing AS SELECT t.* FROM team_history t
 WHERE NOT EXISTS(SELECT 1 FROM graph_history h WHERE h.type IN ('team','team_undo','lead') AND h.at=t.at AND h.actor=t.actor AND h.old_value=t.old_value AND h.new_value=t.new_value);
INSERT INTO graph_history(type,node,attr,old_value,new_value,at,actor,target_kind)
 SELECT CASE WHEN action='undo' THEN 'team_undo' WHEN action='lead' THEN 'lead' ELSE 'team' END,
  CASE WHEN team_id<>'' THEN team_id ELSE old_team_id END,
  CASE WHEN action='lead' THEN 'lead'
   WHEN action='membership' OR (action='undo' AND member_kind<>'') THEN 'membership:'||member_kind||':'||member_id
   WHEN action='undo' THEN 'undo'
   ELSE action END,
  old_value,new_value,at,actor,CASE WHEN action='lead' THEN 'team' ELSE '' END
 FROM temp.team_history_missing ORDER BY id;
DROP TABLE temp.team_history_missing;
DROP TABLE IF EXISTS team_history;
`
