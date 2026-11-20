package gateway

// repoLeadFoldMarker finds the migration that folds the repo side tables into
// repos. The runner uses it, and it appears only in that migration's text.
const repoLeadFoldMarker = "Repo lead, archive and held back columns"

// The repo lead, the archive flag and the held back text were three tables
// keyed by repo id. They are columns of repos now.
//
// lead_agent_id stores only what an owner decided:
//   - NULL: no lead was set, so the lead follows repos.owner_agent_id;
//   - an empty string: the lead was cleared, so the Repo has no lead;
//   - an Agent id: that Agent leads the Repo, whether or not it is the owner.
//
// The owner stays the only copy of the default. Storing it here as well would
// give the lead two sources to keep in step, and an owner change would have to
// rewrite the lead. repoTopAgentOfSQL resolves the three cases for every reader.
//
// lead_agent_id has no REFERENCES clause. owner_agent_id has one, but the
// gateway does not turn foreign keys on, so neither clause is enforced. A
// column with a REFERENCES clause cannot be dropped, and a test that rewinds
// the schema drops it to rebuild the old shape.
const repoLeadFoldAddColumns = `
-- ` + repoLeadFoldMarker + `: archived_repos, repo_held_back and repo_leads
-- move into repos. The copy below runs only in the transaction that adds
-- these columns, so a replay never resets a lead set later.
ALTER TABLE repos ADD COLUMN archived INTEGER NOT NULL DEFAULT 0;
ALTER TABLE repos ADD COLUMN held_back TEXT NOT NULL DEFAULT '';
ALTER TABLE repos ADD COLUMN lead_agent_id TEXT;
`

// repoLeadFoldCopy copies the three tables. A Repo with no override keeps NULL
// and follows its owner. A cleared override (a row with no Agent) becomes an
// empty string.
const repoLeadFoldCopy = `
UPDATE repos SET archived=1 WHERE id IN (SELECT repo_id FROM archived_repos);
UPDATE repos SET held_back=(SELECT held_back FROM repo_held_back WHERE repo_id=repos.id) WHERE id IN (SELECT repo_id FROM repo_held_back);
UPDATE repos SET lead_agent_id=(SELECT CASE WHEN rl.agent_id IS NULL THEN '' ELSE rl.agent_id END FROM repo_leads rl WHERE rl.repo_id=repos.id) WHERE id IN (SELECT repo_id FROM repo_leads);
`

// repoLeadFoldRewrite replaces the triggers that name the old tables or insert
// into repos by position, then drops the tables. Every statement is idempotent,
// so a replay can run it on a database that already has the change.
const repoLeadFoldRewrite = `
DROP TRIGGER IF EXISTS repo_agent_project_insert;
CREATE TRIGGER repo_agent_project_insert AFTER INSERT ON projects WHEN NEW.name<>'' AND NOT EXISTS (SELECT 1 FROM graph_nodes WHERE name=NEW.name AND kind<>'component') BEGIN
 INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES(NEW.name,NEW.name,'',NEW.added_at,NEW.added_at,0) ON CONFLICT(id) DO NOTHING;
 INSERT INTO repos(id,display_name,created_at,updated_at,owner_agent_id) VALUES(NEW.name,NEW.name,NEW.added_at,NEW.added_at,CASE WHEN EXISTS (SELECT 1 FROM agents WHERE id=NEW.name AND tombstone=0) THEN NEW.name ELSE NULL END) ON CONFLICT(id) DO NOTHING;
END;
DROP TRIGGER IF EXISTS repo_agent_project_update;
CREATE TRIGGER repo_agent_project_update AFTER UPDATE OF name ON projects WHEN NEW.name<>'' AND NOT EXISTS (SELECT 1 FROM graph_nodes WHERE name=NEW.name AND kind<>'component') BEGIN
 INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES(NEW.name,NEW.name,'',NEW.checked_at,NEW.checked_at,0) ON CONFLICT(id) DO NOTHING;
 INSERT INTO repos(id,display_name,created_at,updated_at,owner_agent_id) VALUES(NEW.name,NEW.name,NEW.checked_at,NEW.checked_at,CASE WHEN EXISTS (SELECT 1 FROM agents WHERE id=NEW.name AND tombstone=0) THEN NEW.name ELSE NULL END) ON CONFLICT(id) DO NOTHING;
END;
DROP TRIGGER IF EXISTS repo_agent_session_insert;
CREATE TRIGGER repo_agent_session_insert AFTER INSERT ON sessions WHEN NEW.repo_project<>'' AND NOT EXISTS (SELECT 1 FROM graph_nodes WHERE name=NEW.repo_project AND kind<>'component') BEGIN
 INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES(NEW.repo_project,NEW.repo_project,'',NEW.created_at,NEW.created_at,0) ON CONFLICT(id) DO NOTHING;
 INSERT INTO repos(id,display_name,created_at,updated_at,owner_agent_id) VALUES(NEW.repo_project,NEW.repo_project,NEW.created_at,NEW.created_at,CASE WHEN EXISTS (SELECT 1 FROM agents WHERE id=NEW.repo_project AND tombstone=0) THEN NEW.repo_project ELSE NULL END) ON CONFLICT(id) DO NOTHING;
END;
DROP TRIGGER IF EXISTS repo_agent_session_update;
CREATE TRIGGER repo_agent_session_update AFTER UPDATE OF repo_project ON sessions WHEN NEW.repo_project<>'' AND NOT EXISTS (SELECT 1 FROM graph_nodes WHERE name=NEW.repo_project AND kind<>'component') BEGIN
 INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES(NEW.repo_project,NEW.repo_project,'',NEW.last_seen,NEW.last_seen,0) ON CONFLICT(id) DO NOTHING;
 INSERT INTO repos(id,display_name,created_at,updated_at,owner_agent_id) VALUES(NEW.repo_project,NEW.repo_project,NEW.last_seen,NEW.last_seen,CASE WHEN EXISTS (SELECT 1 FROM agents WHERE id=NEW.repo_project AND tombstone=0) THEN NEW.repo_project ELSE NULL END) ON CONFLICT(id) DO NOTHING;
END;
-- An Agent that owns a Repo is guarded by agent_owned_tombstone. These two
-- guard an Agent that leads a Repo, as the repo_leads triggers did.
DROP TRIGGER IF EXISTS repo_lead_delete;
CREATE TRIGGER repo_lead_delete BEFORE DELETE ON agents
 WHEN EXISTS(SELECT 1 FROM repos WHERE lead_agent_id=OLD.id)
 BEGIN SELECT RAISE(ABORT,'clear or transfer Repo lead first'); END;
DROP TRIGGER IF EXISTS repo_lead_tombstone;
CREATE TRIGGER repo_lead_tombstone BEFORE UPDATE OF tombstone ON agents
 WHEN NEW.tombstone<>0 AND EXISTS(SELECT 1 FROM repos WHERE lead_agent_id=OLD.id)
 BEGIN SELECT RAISE(ABORT,'clear or transfer Repo lead first'); END;
DROP TABLE IF EXISTS archived_repos;
DROP TABLE IF EXISTS repo_held_back;
DROP TABLE IF EXISTS repo_leads;
`

// repoLeadFoldMigration is the migration as the list holds it. A database that
// has the columns runs only repoLeadFoldRewrite (see the runner).
const repoLeadFoldMigration = repoLeadFoldAddColumns + repoLeadFoldCopy + repoLeadFoldRewrite
