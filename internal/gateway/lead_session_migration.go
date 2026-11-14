package gateway

// leadSessionRepairMigration pins a lead session on every lead Agent that has
// none (the owner's decision on lead routing). An Agent with one session gets
// it. An Agent with several gets its only online session. An Agent with several
// and no single online session gets one question for the CTO, which answers
// with a lead_set naming the session.
//
// The migration is append-only and idempotent: an Agent that already has a pin
// is left alone, and a CTO question that is still open is not repeated.
const leadSessionRepairMarker = "Lead session repair"

const leadSessionRepairMigration = `
-- ` + leadSessionRepairMarker + `: pin one lead session per lead Agent.
WITH cand AS (
 SELECT s.agent_id AS agent, s.id AS session, s.online AS online
 FROM sessions s
 WHERE COALESCE(s.agent_id,'')<>'' AND s.worker='' AND s.archived_at=0
  AND s.harness NOT IN ('owner','cto','bot')
  AND NOT EXISTS(SELECT 1 FROM project_mergers pm WHERE pm.session_id=s.id)
  AND NOT EXISTS(SELECT 1 FROM agents ma WHERE ma.id=s.agent_id AND ma.role='merger')
)
UPDATE agents SET lead_session=(SELECT c.session FROM cand c WHERE c.agent=agents.id)
WHERE COALESCE(agents.lead_session,'')='' AND agents.tombstone=0 AND agents.role<>'merger'
 AND (EXISTS(SELECT 1 FROM repos r WHERE NOT EXISTS(SELECT 1 FROM archived_repos ar WHERE ar.repo_id=r.id)
   AND COALESCE((SELECT rl.agent_id FROM repo_leads rl WHERE rl.repo_id=r.id),r.owner_agent_id)=agents.id)
  OR EXISTS(SELECT 1 FROM teams t WHERE t.active=1 AND t.owner_agent_id=agents.id)
  OR EXISTS(SELECT 1 FROM agents lv WHERE lv.id=agents.id AND lv.lead_level<>0))
 AND (SELECT COUNT(*) FROM cand c WHERE c.agent=agents.id)=1;

WITH cand AS (
 SELECT s.agent_id AS agent, s.id AS session, s.online AS online
 FROM sessions s
 WHERE COALESCE(s.agent_id,'')<>'' AND s.worker='' AND s.archived_at=0
  AND s.harness NOT IN ('owner','cto','bot')
  AND NOT EXISTS(SELECT 1 FROM project_mergers pm WHERE pm.session_id=s.id)
  AND NOT EXISTS(SELECT 1 FROM agents ma WHERE ma.id=s.agent_id AND ma.role='merger')
)
UPDATE agents SET lead_session=(SELECT c.session FROM cand c WHERE c.agent=agents.id AND c.online=1)
WHERE COALESCE(agents.lead_session,'')='' AND agents.tombstone=0 AND agents.role<>'merger'
 AND (EXISTS(SELECT 1 FROM repos r WHERE NOT EXISTS(SELECT 1 FROM archived_repos ar WHERE ar.repo_id=r.id)
   AND COALESCE((SELECT rl.agent_id FROM repo_leads rl WHERE rl.repo_id=r.id),r.owner_agent_id)=agents.id)
  OR EXISTS(SELECT 1 FROM teams t WHERE t.active=1 AND t.owner_agent_id=agents.id)
  OR EXISTS(SELECT 1 FROM agents lv WHERE lv.id=agents.id AND lv.lead_level<>0))
 AND (SELECT COUNT(*) FROM cand c WHERE c.agent=agents.id)>1
 AND (SELECT COUNT(*) FROM cand c WHERE c.agent=agents.id AND c.online=1)=1;

WITH cand AS (
 SELECT s.agent_id AS agent, s.id AS session, s.online AS online,
  COALESCE(NULLIF(p.name,''),NULLIF(s.name,''),NULLIF(s.title,''),NULLIF(s.repo_project,''),s.id) AS label
 FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id
 WHERE COALESCE(s.agent_id,'')<>'' AND s.worker='' AND s.archived_at=0
  AND s.harness NOT IN ('owner','cto','bot')
  AND NOT EXISTS(SELECT 1 FROM project_mergers pm WHERE pm.session_id=s.id)
  AND NOT EXISTS(SELECT 1 FROM agents ma WHERE ma.id=s.agent_id AND ma.role='merger')
)
INSERT INTO inbox_items (id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status,options,asker_name,change_title,
 from_kind,source_ref,about_kind,about_id,next_who,why_you,detail,route_fixed,category)
SELECT 'i_'||lower(hex(randomblob(8))),'question',cto.value,'',
 'Which session leads '||a.display_name||'?'||char(10)||
 (SELECT group_concat('- '||x.label,char(10)) FROM (SELECT c.label FROM cand c WHERE c.agent=a.id ORDER BY c.label) x),
 '','','',CAST(strftime('%s','now') AS INTEGER)*1000000000,'open','[]','agent-net','','gateway',
 'lead_session:'||a.id,'','',cto.value,'','',1,'normal'
FROM agents a, (SELECT value FROM owner_settings WHERE key='cto_session') cto
WHERE COALESCE(a.lead_session,'')='' AND a.tombstone=0 AND a.role<>'merger'
 AND (EXISTS(SELECT 1 FROM repos r WHERE NOT EXISTS(SELECT 1 FROM archived_repos ar WHERE ar.repo_id=r.id)
   AND COALESCE((SELECT rl.agent_id FROM repo_leads rl WHERE rl.repo_id=r.id),r.owner_agent_id)=a.id)
  OR EXISTS(SELECT 1 FROM teams t WHERE t.active=1 AND t.owner_agent_id=a.id)
  OR EXISTS(SELECT 1 FROM agents lv WHERE lv.id=a.id AND lv.lead_level<>0))
 AND (SELECT COUNT(*) FROM cand c WHERE c.agent=a.id)>1
 AND (SELECT COUNT(*) FROM cand c WHERE c.agent=a.id AND c.online=1)<>1
 AND NOT EXISTS(SELECT 1 FROM inbox_items i WHERE i.source_ref='lead_session:'||a.id AND i.status='open');
`
