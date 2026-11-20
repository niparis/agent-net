package gateway

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// repoFoldTriggers are the triggers the repo fold rewrites. Their definitions
// name the repos columns, so a store shaped as before the fold drops them first.
var repoFoldTriggers = []string{
	"repo_agent_project_insert", "repo_agent_project_update",
	"repo_agent_session_insert", "repo_agent_session_update",
	"repo_lead_delete", "repo_lead_tombstone",
}

// beforeRepoFold shapes a store as the gateway before the repo fold left it: the
// three side tables exist again and are empty, the repo columns are gone, and
// the schema version is one short, so the fold is the next migration to run.
func beforeRepoFold(t *testing.T, s *Store) {
	t.Helper()
	// The store remembers that it is folded. This shape is not, so say so.
	s.repoFolded.Store(false)
	statements := make([]string, 0, len(repoFoldTriggers)+8)
	for _, trigger := range repoFoldTriggers {
		statements = append(statements, `DROP TRIGGER IF EXISTS `+trigger)
	}
	for _, col := range []string{"archived", "held_back", "lead_agent_id"} {
		statements = append(statements, `ALTER TABLE repos DROP COLUMN `+col)
	}
	statements = append(statements,
		`CREATE TABLE archived_repos (repo_id TEXT PRIMARY KEY REFERENCES repos(id))`,
		`CREATE TABLE repo_held_back (repo_id TEXT PRIMARY KEY REFERENCES repos(id), held_back TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE repo_leads (repo_id TEXT PRIMARY KEY REFERENCES repos(id), agent_id TEXT REFERENCES agents(id))`,
		`DELETE FROM schema_version WHERE version>=`+strconv.Itoa(migrationVersion(t, repoLeadFoldMarker)),
	)
	for _, q := range statements {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// foldRepos are the Repos of the fold test before the migration. Each one
// covers one case of the copy: an override that differs from the owner, no
// override, a cleared lead, and no owner with an archive.
func foldRepos(t *testing.T, s *Store) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES('demo','demo','',0,0,0),('other','other','',0,0,0),('fresh','fresh','',0,0,0)`,
		`INSERT INTO repos(id,display_name,created_at,updated_at,owner_agent_id) VALUES('demo','demo',0,0,'demo'),('plain','plain',0,0,'demo'),('cleared','cleared',0,0,'demo'),('old','old',0,0,NULL)`,
		`INSERT INTO repo_leads(repo_id,agent_id) VALUES('demo','other'),('cleared',NULL)`,
		`INSERT INTO archived_repos(repo_id) VALUES('old')`,
		`INSERT INTO repo_held_back(repo_id,held_back) VALUES('demo','force pushes')`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// TestRepoFoldKeepsTheLeadArchiveAndHeldBack migrates a store that has an
// override, a cleared lead, an archived Repo and held back text. Every reader
// must see the same values afterwards, and the lead, archive and held back
// writes must still work.
func TestRepoFoldKeepsTheLeadArchiveAndHeldBack(t *testing.T) {
	s, _ := newTestStore(t)
	beforeRepoFold(t, s)
	foldRepos(t, s)
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	for repo, want := range map[string]string{"demo": "other", "plain": "demo", "cleared": "", "old": ""} {
		out, err := s.StoredLead(protocol.GraphRef{Kind: "repo", ID: repo})
		if err != nil || out.Agent != want {
			t.Fatalf("lead of %s = %q (%v), want %q", repo, out.Agent, err, want)
		}
	}
	var owner string
	if err := s.db.QueryRow(`SELECT owner_agent_id FROM repos WHERE id='demo'`).Scan(&owner); err != nil || owner != "demo" {
		t.Fatalf("owner of demo = %q (%v), want demo: the fold must not move the owner", owner, err)
	}
	var archived int
	if err := s.db.QueryRow(`SELECT archived FROM repos WHERE id='old'`).Scan(&archived); err != nil || archived != 1 {
		t.Fatalf("archived of old = %d (%v), want 1", archived, err)
	}
	if held, err := s.RepoHeldBack("demo"); err != nil || held != "force pushes" {
		t.Fatalf("held back of demo = %q (%v)", held, err)
	}
	for _, table := range []string{"archived_repos", "repo_held_back", "repo_leads"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s still exists after the fold (%d, %v)", table, n, err)
		}
	}

	// The writes that used the three tables, on the migrated store.
	if _, err := s.db.Exec(`UPDATE graph_ref_gate SET enabled=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.loadGraphRefs(); err != nil {
		t.Fatal(err)
	}
	demo := protocol.GraphRef{Kind: "repo", ID: "demo"}
	if err := s.SetLead(protocol.LeadSetRequest{Ref: demo}, "owner", false); err != nil {
		t.Fatalf("clear lead: %v", err)
	}
	if out, err := s.StoredLead(demo); err != nil || out.Agent != "" {
		t.Fatalf("lead after the clear = %q (%v), want none", out.Agent, err)
	}
	owned := protocol.GraphRef{Kind: "agent", ID: "demo"}
	if err := s.SetLead(protocol.LeadSetRequest{Ref: demo, Lead: &owned}, "owner", false); err != nil {
		t.Fatalf("set lead: %v", err)
	}
	if out, err := s.StoredLead(demo); err != nil || out.Agent != "demo" {
		t.Fatalf("lead after the set = %q (%v), want demo", out.Agent, err)
	}
	if err := s.GraphLifecycle("repo.archive", protocol.GraphLifecycleRequest{Ref: &protocol.GraphRef{Kind: "repo", ID: "plain"}}, "owner", false); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if err := s.db.QueryRow(`SELECT archived FROM repos WHERE id='plain'`).Scan(&archived); err != nil || archived != 1 {
		t.Fatalf("archived of plain = %d (%v), want 1 after the archive", archived, err)
	}
	if err := s.WriteTeam(protocol.TeamWrite{Action: "repo_held_back", ID: "cleared", HeldBack: "no force pushes"}, "owner"); err != nil {
		t.Fatalf("held back write: %v", err)
	}
	if held, err := s.RepoHeldBack("cleared"); err != nil || held != "no force pushes" {
		t.Fatalf("held back of cleared = %q (%v)", held, err)
	}
}

// The fold rewrites the triggers that inserted Repos by position. A Repo that
// discovery creates must still get its owner as its lead, and no trigger may
// keep the positional insert.
func TestRepoFoldDiscoveredRepoLeadsAsItsOwner(t *testing.T) {
	s, _ := newTestStore(t)
	beforeRepoFold(t, s)
	foldRepos(t, s)
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	var positional int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND sql LIKE '%INSERT INTO repos VALUES(%'`).Scan(&positional); err != nil || positional != 0 {
		t.Fatalf("%d trigger(s) still insert into repos by position (%v)", positional, err)
	}
	node, _, err := s.CreateNode("fold-host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "fresh", Repo: protocol.RepoInfo{Root: "/w/fresh", Project: "fresh"}}); err != nil {
		t.Fatal(err)
	}
	var owner string
	if err := s.db.QueryRow(`SELECT COALESCE(owner_agent_id,'') FROM repos WHERE id='fresh'`).Scan(&owner); err != nil || owner != "fresh" {
		t.Fatalf("fresh repo owner=%q (%v), want fresh", owner, err)
	}
	if out, err := s.StoredLead(protocol.GraphRef{Kind: "repo", ID: "fresh"}); err != nil || out.Agent != "fresh" {
		t.Fatalf("lead of the fresh repo = %q (%v), want its owner fresh", out.Agent, err)
	}
}

// A store that still has Agents runs the org-tree steps before the fold. Those
// steps place Agents with the lead of each Repo, so they must read the lead the
// store has at that point.
func TestRepoFoldLetsTheOrgTreeStepsRunFirst(t *testing.T) {
	s, _ := newTestStore(t)
	beforeRepoFold(t, s)
	foldRepos(t, s)
	if err := rewindOrgTreeErr(t, s); err != nil {
		t.Fatalf("replaying the org tree steps before the fold: %v", err)
	}
	if out, err := s.StoredLead(protocol.GraphRef{Kind: "repo", ID: "demo"}); err != nil || out.Agent != "other" {
		t.Fatalf("lead of demo after the replay = %q (%v), want other", out.Agent, err)
	}
}

// The agents and projects screens read the lead of a Repo through its topic.
// A lead write must move both topics, or a screen keeps the old lead.
func TestRepoLeadWriteRefreshesTheAgentsAndProjectsTopics(t *testing.T) {
	s := teamStore(t)
	before := s.topicVersions()
	if _, err := s.db.Exec(`UPDATE sessions SET agent_id='other' WHERE repo_id='demo'`); err != nil {
		t.Fatal(err)
	}
	other := protocol.GraphRef{Kind: "agent", ID: "other"}
	if err := s.SetLead(protocol.LeadSetRequest{Ref: protocol.GraphRef{Kind: "repo", ID: "demo"}, Lead: &other}, "owner", false); err != nil {
		t.Fatal(err)
	}
	after := s.topicVersions()
	if after[protocol.TopicAgents] == before[protocol.TopicAgents] {
		t.Fatal("the agents topic did not move on a repo lead write")
	}
	if after[protocol.TopicProjects] == before[protocol.TopicProjects] {
		t.Fatal("the projects topic did not move on a repo lead write")
	}
}

// replayMigrations runs the migrations again after a test rewound the schema
// version. The older steps name the repo side tables, so they come back first.
func replayMigrations(t *testing.T, s *Store) error {
	t.Helper()
	if behind(t, s.db) {
		restoreRepoSideTables(t, s.db)
	}
	return s.migrate()
}

// restoreRepoSideTablesAt does what replayMigrations does for a database file
// that a test is about to open, so the gateway migrates it itself.
func restoreRepoSideTablesAt(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if behind(t, db) {
		restoreRepoSideTables(t, db)
	}
}

// behind says whether the schema version of db is older than this binary's
// migrations. A database with no schema_version table has never migrated.
func behind(t *testing.T, db repoDB) bool {
	t.Helper()
	var table int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_version'`).Scan(&table); err != nil {
		t.Fatal(err)
	}
	if table == 0 {
		return false
	}
	var cur int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_version`).Scan(&cur); err != nil {
		t.Fatal(err)
	}
	return cur < len(migrations)
}

// repoDB is the part of a database handle the restore needs. The gateway's
// handle and a plain *sql.DB both have it.
type repoDB interface {
	Exec(string, ...any) (sql.Result, error)
	QueryRow(string, ...any) *sql.Row
}

// restoreRepoSideTables puts back each repo side table that the fold dropped,
// with the rows the fold moved into repos, so the replayed steps read the data
// the old schema had. A table that exists already keeps its rows, which a test
// may have put there on purpose. Before the fold the tables exist, and nothing
// is restored.
func restoreRepoSideTables(t *testing.T, db repoDB) {
	t.Helper()
	var folded int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('repos') WHERE name='lead_agent_id'`).Scan(&folded); err != nil {
		t.Fatal(err)
	}
	if folded == 0 {
		return
	}
	for _, rebuild := range []struct{ table, create, fill string }{
		{"archived_repos",
			`CREATE TABLE archived_repos (repo_id TEXT PRIMARY KEY REFERENCES repos(id))`,
			`INSERT INTO archived_repos(repo_id) SELECT id FROM repos WHERE archived<>0`},
		{"repo_held_back",
			`CREATE TABLE repo_held_back (repo_id TEXT PRIMARY KEY REFERENCES repos(id), held_back TEXT NOT NULL DEFAULT '')`,
			`INSERT INTO repo_held_back(repo_id,held_back) SELECT id,held_back FROM repos WHERE held_back<>''`},
		{"repo_leads",
			`CREATE TABLE repo_leads (repo_id TEXT PRIMARY KEY REFERENCES repos(id), agent_id TEXT REFERENCES agents(id))`,
			`INSERT INTO repo_leads(repo_id,agent_id) SELECT id, NULLIF(lead_agent_id,'') FROM repos WHERE lead_agent_id IS NOT NULL`},
	} {
		var exists int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, rebuild.table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists > 0 {
			continue
		}
		for _, q := range []string{rebuild.create, rebuild.fill} {
			if _, err := db.Exec(q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
	}
}

// A scope over an Agent reads the Repos the Agent leads. The worker sandbox
// cutover reads scopes while it runs, on a store from before the fold, where the
// explicit leads are still in repo_leads.
func TestRepoFoldScopeReadsTheLeadOfAStoreBeforeTheFold(t *testing.T) {
	s, _ := newTestStore(t)
	for _, q := range []string{
		`INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES('demo','demo','',0,0,0),('other','other','',0,0,0)`,
		`INSERT INTO repos(id,display_name,created_at,updated_at,owner_agent_id) VALUES('demo','demo',0,0,'demo')`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := s.db.Exec(`UPDATE graph_ref_gate SET enabled=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.loadGraphRefs(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetScope(protocol.ScopeSetRequest{Node: "demo", Kind: protocol.ScopeMerge, Over: "agent:other"}, "owner", false); err != nil {
		t.Fatal(err)
	}
	beforeRepoFold(t, s)
	if _, err := s.db.Exec(`INSERT INTO repo_leads(repo_id,agent_id) VALUES('demo','other')`); err != nil {
		t.Fatal(err)
	}
	_, repos, err := s.ScopesWithRepos("demo")
	if err != nil {
		t.Fatalf("reading scopes before the fold: %v", err)
	}
	if got := strings.Join(repos["agent:other"], ","); got != "demo" {
		t.Fatalf("repos of agent:other = %q, want demo: its lead is led by it", got)
	}
}
