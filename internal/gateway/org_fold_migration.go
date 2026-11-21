package gateway

import (
	"database/sql"
	"strings"
)

// orgFoldMarker names the migration that unifies repo leadership on
// repos.lead_agent_id, removes stored reports_to edges, and folds
// team_members, outcome_overrules, charter_draft_asks, and
// project_display_names into their parent tables.
const orgFoldMarker = "Org graph leadership and four table folds"

const orgFoldMigration = `
-- ` + orgFoldMarker + `: one source per leadership fact, computed reports_to, and four folds.
SELECT 1;
`

func hasTable(tx *sql.Tx, name string) (bool, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
	return n > 0, err
}

func hasColumn(tx *sql.Tx, table, col string) (bool, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, col).Scan(&n)
	return n > 0, err
}

func execAll(tx *sql.Tx, stmts ...string) error {
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// foldOrgGraphTx runs the schema reduction for the org graph and the four
// folded tables. Every step checks the current schema so a replay on a
// rewound database is safe.
func foldOrgGraphTx(tx *sql.Tx) error {
	hasOwner, err := hasColumn(tx, "repos", "owner_agent_id")
	if err != nil {
		return err
	}
	hasRepoTeam, err := hasColumn(tx, "repos", "team_id")
	if err != nil {
		return err
	}
	hasTeamMembers, err := hasTable(tx, "team_members")
	if err != nil {
		return err
	}
	hasDisplayNames, err := hasTable(tx, "project_display_names")
	if err != nil {
		return err
	}
	if hasOwner || !hasRepoTeam {
		displayExpr := "r.display_name"
		if hasDisplayNames {
			displayExpr = "COALESCE(NULLIF((SELECT name FROM project_display_names WHERE project=r.id),''),r.display_name)"
		}
		leadExpr := "r.lead_agent_id"
		if hasOwner {
			leadExpr = "CASE WHEN r.lead_agent_id IS NULL THEN r.owner_agent_id WHEN r.lead_agent_id='' THEN NULL ELSE r.lead_agent_id END"
		}
		teamExpr := "''"
		if hasRepoTeam {
			teamExpr = "r.team_id"
		} else if hasTeamMembers {
			teamExpr = "COALESCE((SELECT team_id FROM team_members WHERE member_kind='repo' AND member_id=r.id),'')"
		}
		// Later schema changes add columns, indexes and triggers to repos. The
		// rebuild keeps every column it does not own, and gives back each index
		// and trigger it does not recreate below.
		extraDefs, extraNames, err := repoExtraColumnsTx(tx)
		if err != nil {
			return err
		}
		kept, err := repoDependentSchemaTx(tx)
		if err != nil {
			return err
		}
		stmts := []string{
			`DROP TRIGGER IF EXISTS repo_live_owner_insert`,
			`DROP TRIGGER IF EXISTS repo_live_owner_update`,
			`DROP TRIGGER IF EXISTS agent_owned_tombstone`,
			`DROP TRIGGER IF EXISTS repo_lead_delete`,
			`DROP TRIGGER IF EXISTS repo_lead_tombstone`,
			`DROP TRIGGER IF EXISTS repo_agent_project_insert`,
			`DROP TRIGGER IF EXISTS repo_agent_project_update`,
			`DROP TRIGGER IF EXISTS repo_agent_session_insert`,
			`DROP TRIGGER IF EXISTS repo_agent_session_update`,
			`DROP TRIGGER IF EXISTS settings_target_identity_repo_delete`,
			`DROP TRIGGER IF EXISTS session_identity_repo_update`,
			`CREATE TABLE repos_new (
 id TEXT PRIMARY KEY,
 display_name TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 archived INTEGER NOT NULL DEFAULT 0,
 held_back TEXT NOT NULL DEFAULT '',
 lead_agent_id TEXT,
 team_id TEXT NOT NULL DEFAULT ''` + extraDefs + `
)`,
			`INSERT INTO repos_new(id,display_name,created_at,updated_at,archived,held_back,lead_agent_id,team_id` + extraNames + `)
SELECT r.id,` + displayExpr + `,r.created_at,r.updated_at,r.archived,r.held_back,` + leadExpr + `,` + teamExpr + extraSelect(extraNames) + ` FROM repos r`,
			`DROP TABLE repos`,
			`ALTER TABLE repos_new RENAME TO repos`,
		}
		// The kept triggers and indexes go first and come back last.
		drops := make([]string, 0, len(kept))
		for _, o := range kept {
			drops = append(drops, `DROP `+o.kind+` IF EXISTS `+o.name)
		}
		if err := execAll(tx, append(drops, stmts...)...); err != nil {
			return err
		}
		for _, o := range kept {
			if _, err := tx.Exec(o.sql); err != nil {
				return err
			}
		}
	}
	// The settings fold dropped settings_target_identity and its triggers. A
	// store from before that fold still has the table, so it still needs this one.
	hasTokenTable, err := hasTable(tx, "settings_target_identity")
	if err != nil {
		return err
	}
	tokenTrigger := []string{}
	if hasTokenTable {
		tokenTrigger = []string{
			`DROP TRIGGER IF EXISTS settings_target_identity_repo_delete`,
			`CREATE TRIGGER settings_target_identity_repo_delete AFTER DELETE ON repos BEGIN
 DELETE FROM settings_target_identity WHERE kind='repo' AND id=OLD.id;
END`,
		}
	}
	if err := execAll(tx, tokenTrigger...); err != nil {
		return err
	}
	if err := execAll(tx,
		`CREATE INDEX IF NOT EXISTS repos_team ON repos(team_id)`,
		`DROP TRIGGER IF EXISTS session_identity_repo_update`,
		`CREATE TRIGGER session_identity_repo_update AFTER UPDATE OF repo_project ON sessions BEGIN
 UPDATE sessions SET repo_id=(SELECT id FROM repos WHERE id=NEW.repo_project) WHERE id=NEW.id AND NEW.harness NOT IN ('cto','owner');
END`,
		`DROP TRIGGER IF EXISTS repo_agent_project_insert`,
		`CREATE TRIGGER repo_agent_project_insert AFTER INSERT ON projects WHEN NEW.name<>'' AND NOT EXISTS (SELECT 1 FROM agents WHERE id=NEW.name AND role='architect' AND tombstone=0) BEGIN
 INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES(NEW.name,NEW.name,'',NEW.added_at,NEW.added_at,0) ON CONFLICT(id) DO NOTHING;
 INSERT INTO repos(id,display_name,created_at,updated_at,lead_agent_id) VALUES(NEW.name,NEW.name,NEW.added_at,NEW.added_at,CASE WHEN EXISTS (SELECT 1 FROM agents WHERE id=NEW.name AND tombstone=0) THEN NEW.name ELSE NULL END) ON CONFLICT(id) DO NOTHING;
END`,
		`DROP TRIGGER IF EXISTS repo_agent_project_update`,
		`CREATE TRIGGER repo_agent_project_update AFTER UPDATE OF name ON projects WHEN NEW.name<>'' AND NOT EXISTS (SELECT 1 FROM agents WHERE id=NEW.name AND role='architect' AND tombstone=0) BEGIN
 INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES(NEW.name,NEW.name,'',NEW.checked_at,NEW.checked_at,0) ON CONFLICT(id) DO NOTHING;
 INSERT INTO repos(id,display_name,created_at,updated_at,lead_agent_id) VALUES(NEW.name,NEW.name,NEW.checked_at,NEW.checked_at,CASE WHEN EXISTS (SELECT 1 FROM agents WHERE id=NEW.name AND tombstone=0) THEN NEW.name ELSE NULL END) ON CONFLICT(id) DO NOTHING;
END`,
		`DROP TRIGGER IF EXISTS repo_agent_session_insert`,
		`CREATE TRIGGER repo_agent_session_insert AFTER INSERT ON sessions WHEN NEW.repo_project<>'' AND NOT EXISTS (SELECT 1 FROM agents WHERE id=NEW.repo_project AND role='architect' AND tombstone=0) BEGIN
 INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES(NEW.repo_project,NEW.repo_project,'',NEW.created_at,NEW.created_at,0) ON CONFLICT(id) DO NOTHING;
 INSERT INTO repos(id,display_name,created_at,updated_at,lead_agent_id) VALUES(NEW.repo_project,NEW.repo_project,NEW.created_at,NEW.created_at,CASE WHEN EXISTS (SELECT 1 FROM agents WHERE id=NEW.repo_project AND tombstone=0) THEN NEW.repo_project ELSE NULL END) ON CONFLICT(id) DO NOTHING;
END`,
		`DROP TRIGGER IF EXISTS repo_agent_session_update`,
		`CREATE TRIGGER repo_agent_session_update AFTER UPDATE OF repo_project ON sessions WHEN NEW.repo_project<>'' AND NOT EXISTS (SELECT 1 FROM agents WHERE id=NEW.repo_project AND role='architect' AND tombstone=0) BEGIN
 INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES(NEW.repo_project,NEW.repo_project,'',NEW.last_seen,NEW.last_seen,0) ON CONFLICT(id) DO NOTHING;
 INSERT INTO repos(id,display_name,created_at,updated_at,lead_agent_id) VALUES(NEW.repo_project,NEW.repo_project,NEW.last_seen,NEW.last_seen,CASE WHEN EXISTS (SELECT 1 FROM agents WHERE id=NEW.repo_project AND tombstone=0) THEN NEW.repo_project ELSE NULL END) ON CONFLICT(id) DO NOTHING;
END`,
		`DROP TRIGGER IF EXISTS repo_live_lead_insert`,
		`CREATE TRIGGER repo_live_lead_insert BEFORE INSERT ON repos
 WHEN NEW.lead_agent_id IS NOT NULL AND NEW.lead_agent_id<>'' AND EXISTS (SELECT 1 FROM agents WHERE id=NEW.lead_agent_id AND tombstone<>0)
 BEGIN SELECT RAISE(ABORT,'tombstoned agent cannot lead a repo'); END`,
		`DROP TRIGGER IF EXISTS repo_live_lead_update`,
		`CREATE TRIGGER repo_live_lead_update BEFORE UPDATE OF lead_agent_id ON repos
 WHEN NEW.lead_agent_id IS NOT NULL AND NEW.lead_agent_id<>'' AND EXISTS (SELECT 1 FROM agents WHERE id=NEW.lead_agent_id AND tombstone<>0)
 BEGIN SELECT RAISE(ABORT,'tombstoned agent cannot lead a repo'); END`,
		`DROP TRIGGER IF EXISTS repo_lead_delete`,
		`CREATE TRIGGER repo_lead_delete BEFORE DELETE ON agents
 WHEN EXISTS(SELECT 1 FROM repos WHERE lead_agent_id=OLD.id)
 BEGIN SELECT RAISE(ABORT,'clear or transfer Repo lead first'); END`,
		`DROP TRIGGER IF EXISTS repo_lead_tombstone`,
		`CREATE TRIGGER repo_lead_tombstone BEFORE UPDATE OF tombstone ON agents
 WHEN NEW.tombstone<>0 AND EXISTS(SELECT 1 FROM repos WHERE lead_agent_id=OLD.id)
 BEGIN SELECT RAISE(ABORT,'clear or transfer Repo lead first'); END`,
	); err != nil {
		return err
	}

	// Fold team_members into agents.team_id (repos.team_id was folded above).
	hasAgentTeam, err := hasColumn(tx, "agents", "team_id")
	if err != nil {
		return err
	}
	if !hasAgentTeam {
		if _, err := tx.Exec(`ALTER TABLE agents ADD COLUMN team_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if hasTeamMembers {
		if _, err := tx.Exec(`UPDATE agents SET team_id=(SELECT team_id FROM team_members WHERE member_kind='agent' AND member_id=agents.id) WHERE id IN (SELECT member_id FROM team_members WHERE member_kind='agent')`); err != nil {
			return err
		}
	}
	if err := execAll(tx,
		`CREATE INDEX IF NOT EXISTS agents_team ON agents(team_id)`,
		`DROP TRIGGER IF EXISTS bot_agent_revoke`,
		`CREATE TRIGGER bot_agent_revoke AFTER UPDATE OF revoked_at ON apps WHEN NEW.kind='bot' AND OLD.revoked_at IS NULL AND NEW.revoked_at IS NOT NULL BEGIN
 INSERT INTO graph_history(type,node,attr,old_value,new_value,at,actor,target_kind)
  SELECT 'team',team_id,'membership:agent:'||NEW.id,team_id,'',NEW.revoked_at,'apps','' FROM agents WHERE id=NEW.id AND team_id<>'';
 UPDATE agents SET team_id='', tombstone=CASE WHEN role='bot' THEN 1 ELSE tombstone END, updated_at=CASE WHEN role='bot' THEN NEW.revoked_at ELSE updated_at END WHERE id=NEW.id;
END`,
		`DROP TRIGGER IF EXISTS bot_names_revoke`,
		`CREATE TRIGGER bot_names_revoke AFTER UPDATE OF revoked_at ON apps WHEN NEW.kind='bot' AND OLD.revoked_at IS NULL AND NEW.revoked_at IS NOT NULL BEGIN
 INSERT INTO graph_history(type,node,attr,old_value,new_value,at,actor,target_kind)
  SELECT 'team',team_id,'membership:agent:'||id,team_id,'',NEW.revoked_at,'apps','' FROM agents WHERE team_id<>'' AND substr(id,1,length(NEW.id)+1)=NEW.id||'/';
 UPDATE agents SET team_id='', tombstone=1, updated_at=NEW.revoked_at WHERE role='bot' AND substr(id,1,length(NEW.id)+1)=NEW.id||'/';
END`,
		`DROP TABLE IF EXISTS team_members`,
		`DELETE FROM graph_edges WHERE kind='reports_to'`,
		`DROP TABLE IF EXISTS project_display_names`,
	); err != nil {
		return err
	}

	// Fold outcome_overrules into graph_outcomes.
	for _, col := range []struct{ name, spec string }{
		{"overruled", "INTEGER NOT NULL DEFAULT 0"},
		{"overrule_message_id", "TEXT NOT NULL DEFAULT ''"},
	} {
		has, err := hasColumn(tx, "graph_outcomes", col.name)
		if err != nil {
			return err
		}
		if !has {
			if _, err := tx.Exec(`ALTER TABLE graph_outcomes ADD COLUMN ` + col.name + ` ` + col.spec); err != nil {
				return err
			}
		}
	}
	hasOverrules, err := hasTable(tx, "outcome_overrules")
	if err != nil {
		return err
	}
	if hasOverrules {
		if _, err := tx.Exec(`UPDATE graph_outcomes SET overruled=1, overrule_message_id=COALESCE((SELECT oo.message_id FROM outcome_overrules oo WHERE oo.reply_id=graph_outcomes.reply_id),'') WHERE reply_id IN (SELECT reply_id FROM outcome_overrules)`); err != nil {
			return err
		}
	}
	if err := execAll(tx,
		`CREATE INDEX IF NOT EXISTS graph_outcomes_overrule_msg ON graph_outcomes(overrule_message_id) WHERE overrule_message_id<>''`,
		`DROP TABLE IF EXISTS outcome_overrules`,
	); err != nil {
		return err
	}

	// Fold charter_draft_asks into charter_drafts as status='asked'.
	hasAskedAt, err := hasColumn(tx, "charter_drafts", "asked_at")
	if err != nil {
		return err
	}
	hasDraftAsks, err := hasTable(tx, "charter_draft_asks")
	if err != nil {
		return err
	}
	if !hasAskedAt {
		if err := execAll(tx,
			`CREATE TABLE charter_drafts_new (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 level TEXT NOT NULL CHECK(level IN ('repo','agent','team','bar')),
 scope TEXT NOT NULL, attr TEXT NOT NULL, value TEXT NOT NULL,
 source TEXT NOT NULL, source_hash TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('asked','pending','accepted','rejected','superseded')),
 drafted_by TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 decided_at INTEGER NOT NULL DEFAULT 0, decided_by TEXT NOT NULL DEFAULT '',
 asked_at INTEGER NOT NULL DEFAULT 0
)`,
			`INSERT INTO charter_drafts_new(id,level,scope,attr,value,source,source_hash,status,drafted_by,created_at,decided_at,decided_by,asked_at)
SELECT id,level,scope,attr,value,source,source_hash,status,drafted_by,created_at,decided_at,decided_by,0 FROM charter_drafts`,
			`DROP TABLE charter_drafts`,
			`ALTER TABLE charter_drafts_new RENAME TO charter_drafts`,
			`CREATE INDEX IF NOT EXISTS charter_drafts_key ON charter_drafts(level,scope,attr,status)`,
		); err != nil {
			return err
		}
	}
	if hasDraftAsks {
		if _, err := tx.Exec(`INSERT INTO charter_drafts(level,scope,attr,value,source,source_hash,status,drafted_by,created_at,asked_at)
SELECT level,scope,'','','',source_hash,'asked',session_id,asked_at,asked_at FROM charter_draft_asks`); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DROP TABLE IF EXISTS charter_draft_asks`); err != nil {
		return err
	}
	return nil
}

// repoOwnColumns are the columns the repos rebuild writes itself.
var repoOwnColumns = map[string]bool{"id": true, "display_name": true, "created_at": true, "updated_at": true,
	"archived": true, "held_back": true, "lead_agent_id": true, "owner_agent_id": true, "team_id": true}

// repoExtraColumnsTx returns the definitions (for CREATE TABLE) and the names
// (for the column lists) of the columns of repos that the rebuild does not own,
// each with a leading comma.
func repoExtraColumnsTx(tx *sql.Tx) (defs, names string, err error) {
	rows, err := tx.Query(`SELECT name,type,"notnull",dflt_value FROM pragma_table_info('repos') ORDER BY cid`)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	for rows.Next() {
		var name, typ string
		var notNull int
		var dflt sql.NullString
		if err := rows.Scan(&name, &typ, &notNull, &dflt); err != nil {
			return "", "", err
		}
		if repoOwnColumns[name] {
			continue
		}
		def := ", " + name + " " + typ
		if notNull != 0 {
			def += " NOT NULL"
		}
		if dflt.Valid {
			def += " DEFAULT " + dflt.String
		}
		defs += def
		names += ", " + name
	}
	return defs, names, rows.Err()
}

// extraSelect turns a leading-comma name list into the same list on r.
func extraSelect(names string) string {
	out := ""
	for _, n := range strings.Split(names, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out += ", r." + n
		}
	}
	return out
}

type savedSchema struct{ kind, name, sql string }

// repoRebuildOwns are the triggers the repos rebuild drops and recreates itself.
var repoRebuildOwns = map[string]bool{
	"repo_live_owner_insert": true, "repo_live_owner_update": true, "agent_owned_tombstone": true,
	"repo_lead_delete": true, "repo_lead_tombstone": true, "repo_agent_project_insert": true,
	"repo_agent_project_update": true, "repo_agent_session_insert": true, "repo_agent_session_update": true,
	"settings_target_identity_repo_delete": true, "session_identity_repo_update": true,
	"repo_live_lead_insert": true, "repo_live_lead_update": true,
}

// repoDependentSchemaTx saves the indexes of repos and the triggers that name
// repos, except the ones the rebuild owns. SQLite refuses to drop repos while
// a trigger on another table names it, and drops the others with the table.
func repoDependentSchemaTx(tx *sql.Tx) ([]savedSchema, error) {
	rows, err := tx.Query(`SELECT type,name,sql FROM sqlite_master WHERE sql IS NOT NULL
 AND ((type='trigger' AND (tbl_name='repos' OR sql LIKE '%repos%')) OR (type='index' AND tbl_name='repos')) ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []savedSchema
	for rows.Next() {
		var o savedSchema
		if err := rows.Scan(&o.kind, &o.name, &o.sql); err != nil {
			return nil, err
		}
		if !repoRebuildOwns[o.name] {
			out = append(out, o)
		}
	}
	return out, rows.Err()
}
