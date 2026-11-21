package gateway

import (
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// createGraphNodesSQL is the table the fold drops, as the shipped migration
// made it. A test of a step that names it makes it first.
const createGraphNodesSQL = `CREATE TABLE graph_nodes (name TEXT PRIMARY KEY, kind TEXT NOT NULL, created_at INTEGER NOT NULL)`

// legacyArchitectTriggers are the two triggers on graph_nodes that the fold
// drops, as the shipped migrations made them.
const legacyArchitectTriggers = `
CREATE TRIGGER repo_agent_architect_insert AFTER INSERT ON graph_nodes WHEN NEW.kind='architect' BEGIN
 INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES(NEW.name,NEW.name,'architect',NEW.created_at,NEW.created_at,0)
 ON CONFLICT(id) DO UPDATE SET role='architect', tombstone=0, updated_at=NEW.created_at;
END;
CREATE TRIGGER repo_agent_architect_delete AFTER DELETE ON graph_nodes WHEN OLD.kind='architect' BEGIN
 UPDATE repos SET owner_agent_id=NULL WHERE owner_agent_id=OLD.name;
 UPDATE agents SET tombstone=1, updated_at=CAST(strftime('%s','now') AS INTEGER)*1000000000 WHERE id=OLD.name;
END;
`

// legacyGraphNodesSQL is the node list query as it read graph_nodes, kept here
// so a test can ask the old layout the same question the new code answers.
const legacyGraphNodesSQL = `SELECT name,kind,created_at,1,'',2 FROM graph_nodes
 UNION ALL SELECT name,'component',0,0,path,graph_excluded FROM projects WHERE graph_excluded<>1 AND name NOT IN (SELECT name FROM graph_nodes)
 UNION ALL SELECT repo_project,'component',0,0,repo_root,graph_excluded FROM sessions WHERE archived_at=0 AND graph_excluded<>1 AND repo_project<>'' AND repo_project NOT IN (SELECT name FROM graph_nodes)
 UNION ALL SELECT source,'component',0,1,'',2 FROM graph_edges WHERE kind<>'reports_to' AND source<>'*' AND source NOT LIKE '/delivery/%' AND source NOT IN (SELECT name FROM graph_nodes)
 UNION ALL SELECT target,'component',0,1,'',2 FROM graph_edges WHERE kind<>'reports_to' AND target NOT IN (SELECT name FROM graph_nodes) ORDER BY 1`

// beforeGraphNodesFold shapes a store as the gateway before the fold left it:
// graph_nodes holds one explicit row per repo charter scope and per architect,
// the six triggers name it, and the schema version is one short, so the fold is
// the next migration to run.
func beforeGraphNodesFold(t *testing.T, s *Store) {
	t.Helper()
	for _, q := range []string{
		createGraphNodesSQL,
		`INSERT OR IGNORE INTO graph_nodes SELECT id,'architect',created_at FROM agents WHERE role='architect' AND tombstone=0`,
		`INSERT OR IGNORE INTO graph_nodes SELECT DISTINCT c.scope,'component',COALESCE((SELECT created_at FROM repos r WHERE r.id=c.scope),0) FROM charter_values c WHERE c.level='repo' AND c.scope NOT IN (SELECT name FROM graph_nodes)`,
		repoLeadFoldRewrite,
		legacyArchitectTriggers,
		`DELETE FROM schema_version WHERE version>=` + strconv.Itoa(migrationVersion(t, graphNodesFoldMarker)),
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// restoreGraphNodesForReplay brings graph_nodes back when a test rewinds a
// folded store to a step between the creation of graph_nodes and the fold.
// Each repo charter scope becomes a component row and each architect agent an
// architect row, so steps like the worker sandbox cutover see the project
// names the old layout gave, and the fold at the end drops the table again.
func restoreGraphNodesForReplay(t testing.TB, db repoDB) {
	t.Helper()
	var folded bool
	if err := db.QueryRow(`SELECT NOT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='graph_nodes')
 AND EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='repos')
 AND EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='agents')
 AND EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='charter_values' AND sql LIKE '%''repo''%')`).Scan(&folded); err != nil {
		t.Fatal(err)
	}
	if !folded {
		return
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS graph_nodes (name TEXT PRIMARY KEY, kind TEXT NOT NULL, created_at INTEGER NOT NULL);
INSERT OR IGNORE INTO graph_nodes(name,kind,created_at) SELECT id,'architect',updated_at FROM agents WHERE role='architect' AND tombstone=0;
INSERT OR IGNORE INTO graph_nodes(name,kind,created_at) SELECT c.scope,'component',COALESCE((SELECT created_at FROM repos r WHERE r.id=c.scope),0)
 FROM (SELECT DISTINCT scope FROM charter_values WHERE level='repo') c WHERE c.scope NOT IN (SELECT id FROM agents WHERE role='architect' AND tombstone=0)`); err != nil {
		t.Fatal(err)
	}
}

// legacyNodeKinds is the answer of the old node list query, name to kind.
func legacyNodeKinds(t testing.TB, s *Store) map[string]string {
	t.Helper()
	rows, err := s.db.Query(legacyGraphNodesSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, kind, root string
		var created int64
		var explicit, checked int
		if err := rows.Scan(&name, &kind, &created, &explicit, &root, &checked); err != nil {
			t.Fatal(err)
		}
		if explicit == 0 && (root == "" || (checked == 0 && !graphProjectRoot(root))) {
			continue
		}
		out[name] = kind
	}
	return out
}

func nodeKinds(t testing.TB, s *Store) map[string]string {
	t.Helper()
	nodes, err := s.graphNodes()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, n := range nodes {
		out[n.Name] = n.Kind
	}
	return out
}

// foldStore builds a store with the three kinds of explicit node the fold must
// keep, then puts it in the layout before the fold. It returns the node kinds
// the old query gave.
//
//   - alpha: a project with a charter and a session.
//   - ghost: a charter and nothing else. The live store has one of these.
//   - bare: an explicit row with an edge and no charter.
//   - arch: a legacy architect with a charter.
//   - lost: an explicit row with no charter, no edge and no project. The fold
//     drops its flag, and only its flag.
func foldStore(t *testing.T) (*Store, map[string]string) {
	t.Helper()
	s, _ := newTestStore(t)
	ids, _ := graphSessions(t, s, "alpha", "ghost")
	for _, c := range [][3]string{{"alpha", "purpose", "builds things"}, {"ghost", "purpose", "outlives its folder"}} {
		if err := s.SetCharter(c[0], c[1], c[2], "owner"); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(t, s, `DELETE FROM sessions WHERE id=?`, ids["ghost"])
	mustExec(t, s, `DELETE FROM projects WHERE name='ghost'`)
	if err := s.AddArchitect("arch", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCharter("arch", "purpose", "designs things", "owner"); err != nil {
		t.Fatal(err)
	}
	beforeGraphNodesFold(t, s)
	mustExec(t, s, `INSERT INTO graph_nodes VALUES('bare','component',7),('lost','component',8)`)
	mustExec(t, s, `INSERT INTO graph_edges(source,target,kind,mode,rule) VALUES('bare','alpha','depends','','normal')`)
	return s, legacyNodeKinds(t, s)
}

func reopen(t *testing.T, s *Store) *Store {
	t.Helper()
	path := s.path
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	restoreRepoSideTablesAt(t, path)
	s2, err := OpenStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { s2.Close() })
	return s2
}

func TestGraphNodesFoldKeepsTheNodeList(t *testing.T) {
	s, before := foldStore(t)
	want := map[string]string{"alpha": "component", "ghost": "component", "bare": "component", "arch": "architect", "lost": "component"}
	if !reflect.DeepEqual(before, want) {
		t.Fatalf("the old layout lists %v, want %v", before, want)
	}
	s = reopen(t, s)
	delete(want, "lost")
	if got := nodeKinds(t, s); !reflect.DeepEqual(got, want) {
		t.Fatalf("after the fold\n got %v\nwant %v", got, want)
	}
	if tableExists(t, s, "graph_nodes") {
		t.Fatal("graph_nodes is still there")
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE sql LIKE '%graph_nodes%'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d schema objects still name graph_nodes: %v", n, err)
	}
	g, err := s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range g.Nodes {
		switch node.Name {
		case "ghost":
			if node.Charter.Purpose != "outlives its folder" {
				t.Errorf("ghost lost its charter: %+v", node.Charter)
			}
		case "arch":
			if node.Kind != "architect" || node.Charter.Purpose != "designs things" {
				t.Errorf("arch: %+v", node)
			}
		}
	}
}

func TestGraphNodesFoldLogsTheNodesThatLoseTheirFlag(t *testing.T) {
	s, _ := foldStore(t)
	s = reopen(t, s)
	var detail string
	if err := s.db.QueryRow(`SELECT detail FROM events WHERE kind='graph.nodes_fold'`).Scan(&detail); err != nil {
		t.Fatalf("no event: %v", err)
	}
	if !strings.Contains(detail, "lost") || strings.Contains(detail, "ghost") || strings.Contains(detail, "bare") {
		t.Fatalf("the event names the wrong nodes: %q", detail)
	}
}

// The old trigger made an agent for an architect. A row whose agent was missing
// or still had its old role is repaired by the fold, so the guard in the new
// triggers reads the truth.
func TestGraphNodesFoldRepairsTheAgentOfAnArchitect(t *testing.T) {
	s, _ := newTestStore(t)
	beforeGraphNodesFold(t, s)
	mustExec(t, s, `INSERT INTO graph_nodes VALUES('old','architect',3)`)
	mustExec(t, s, `DROP TRIGGER repo_agent_architect_insert`)
	mustExec(t, s, `INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES('tomb','tomb','',1,1,1)`)
	mustExec(t, s, `INSERT INTO graph_nodes VALUES('tomb','architect',4)`)
	s = reopen(t, s)
	for _, name := range []string{"old", "tomb"} {
		var role string
		var tombstone int
		if err := s.db.QueryRow(`SELECT role,tombstone FROM agents WHERE id=?`, name).Scan(&role, &tombstone); err != nil || role != "architect" || tombstone != 0 {
			t.Fatalf("agent %s: role %q tombstone %d %v", name, role, tombstone, err)
		}
	}
}

func TestGraphNodesFoldKeepsTheTriggerGuard(t *testing.T) {
	s, _ := foldStore(t)
	s = reopen(t, s)
	node, _, err := s.CreateNode("h2", "linux")
	if err != nil {
		t.Fatal(err)
	}
	for _, project := range []string{"arch", "fresh"} {
		if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "n-" + project, Cwd: "/w/" + project,
			Repo: protocol.RepoInfo{Root: "/w/" + project, Project: project}}); err != nil {
			t.Fatal(err)
		}
	}
	var repos int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM repos WHERE id='arch'`).Scan(&repos); err != nil || repos != 0 {
		t.Errorf("a project named like an architect got a repo: %d %v", repos, err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM repos WHERE id='fresh'`).Scan(&repos); err != nil || repos != 1 {
		t.Errorf("a new project got no repo: %d %v", repos, err)
	}
}

// node_add and node_remove give the same answers after the fold, in the layout
// that has not cut over to typed refs and in the one that has.
func TestGraphNodesFoldKeepsNodeAddAndRemove(t *testing.T) {
	s, _ := foldStore(t)
	s = reopen(t, s)
	if err := s.AddArchitect("newcomer", "owner"); err != nil {
		t.Fatal(err)
	}
	if got := nodeKinds(t, s)["newcomer"]; got != "architect" {
		t.Fatalf("a new architect is %q", got)
	}
	for _, name := range []string{"newcomer", "alpha", "ghost"} {
		if err := s.AddArchitect(name, "owner"); err == nil || !strings.Contains(err.Error(), "node already exists") {
			t.Errorf("adding %s again: %v", name, err)
		}
	}
	if err := s.RemoveArchitect("alpha", "owner"); err == nil || !strings.Contains(err.Error(), "not an architect") {
		t.Errorf("removing a component: %v", err)
	}
	mustExec(t, s, `INSERT INTO graph_edges(source,target,kind,mode,rule) VALUES('arch','alpha','depends','','normal')`)
	if err := s.RemoveArchitect("arch", "owner"); err == nil || !strings.Contains(err.Error(), "node has edges") {
		t.Errorf("removing an architect with an edge: %v", err)
	}
	mustExec(t, s, `DELETE FROM graph_edges WHERE source='arch'`)
	if err := s.RemoveArchitect("arch", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, ok := nodeKinds(t, s)["arch"]; ok {
		t.Error("a removed architect is still a node")
	}
	var tombstone int
	if err := s.db.QueryRow(`SELECT tombstone FROM agents WHERE id='arch'`).Scan(&tombstone); err != nil || tombstone != 1 {
		t.Errorf("the agent of a removed architect: tombstone %d %v", tombstone, err)
	}
	// The undo of that removal brings the architect back, and the undo of the
	// add of a new architect takes it away.
	for _, c := range []struct {
		name string
		want bool
	}{{"arch", true}, {"newcomer", false}} {
		var id int64
		if err := s.db.QueryRow(`SELECT id FROM graph_history WHERE type='node' AND node=? ORDER BY id DESC LIMIT 1`, c.name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if err := s.GraphUndo(id, "owner"); err != nil {
			t.Fatalf("undo %s: %v", c.name, err)
		}
		if _, ok := nodeKinds(t, s)[c.name]; ok != c.want {
			t.Errorf("after the undo %s present=%v, want %v", c.name, ok, c.want)
		}
	}
}

func TestGraphNodesFoldKeepsNodeRemoveThroughTypedRefs(t *testing.T) {
	s, _ := foldStore(t)
	s = reopen(t, s)
	mustExec(t, s, `INSERT OR REPLACE INTO gateway_state(key,value,updated_at) VALUES('graph_ref_gate','1',0)`)
	if err := s.loadGraphRefs(); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCharter("arch", "accepts", "design questions", "owner"); err != nil {
		t.Fatal(err)
	}
	var agentValue int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM charter_values WHERE level='agent' AND scope='arch' AND attr='accepts'`).Scan(&agentValue); err != nil || agentValue != 1 {
		t.Fatalf("an architect charter goes to the agent: %d %v", agentValue, err)
	}
	if err := s.RemoveArchitect("arch", "owner"); err != nil {
		t.Fatal(err)
	}
	// The agent is tombstoned, so the name is no architect any more. The old
	// layout kept the graph_nodes row and listed it for good.
	if got := nodeKinds(t, s)["arch"]; got == "architect" {
		t.Error("a tombstoned architect is still an architect node")
	}
}

// A repo with a charter value or an edge still shows, and a repo with neither
// and no folder does not.
func TestGraphNodesAreTheReposWithACharterOrAnEdge(t *testing.T) {
	s, _ := newTestStore(t)
	mustExec(t, s, `INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES('a','a','',1,1,0),('b','b','',1,1,0),('c','c','',1,1,0)`)
	mustExec(t, s, `INSERT INTO repos(id,display_name,created_at,updated_at,lead_agent_id) VALUES('a','a',11,1,'a'),('b','b',12,1,'b'),('c','c',13,1,'c')`)
	mustExec(t, s, `INSERT INTO charter_values VALUES('repo','a','purpose','x',1,'owner')`)
	mustExec(t, s, `INSERT INTO graph_edges(source,target,kind,mode,rule) VALUES('b','a','depends','','normal')`)
	nodes, err := s.graphNodes()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, n := range nodes {
		got[n.Name] = n.CreatedAt
	}
	want := map[string]int64{"a": 11, "b": 0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("nodes %v, want %v (a: charter, creation time from repos; b: edge, implicit)", got, want)
	}
}

// A repo is a node because it has a session, not because the store wrote a
// charter value for it. A written value would be hidden data in the owner's
// charter (decision 0163), and the owner's own first value would collide.
func TestRegisteringASessionInANewRepoWritesNoCharterValue(t *testing.T) {
	s, _ := newTestStore(t)
	graphSessions(t, s, "fresh")
	var n int
	// The CTO's default charter is an agent-level value that a migration seeds.
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM charter_values WHERE level='repo'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("registering a session wrote %d repo charter values", n)
	}
	nodes, err := s.graphNodes()
	if err != nil || len(nodes) != 1 || nodes[0].Name != "fresh" || nodes[0].Kind != "component" {
		t.Fatalf("nodes = %+v, %v; want the repo as a component", nodes, err)
	}
	if err := s.SetNodeSetting("fresh", protocol.SettingLoopEvery, "4h", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM charter_values WHERE level='repo'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a node setting wrote %d repo charter values, %v", n, err)
	}
}

// An agent made with the role architect after the cutover is a legacy
// architect: it is a node, and its name guards a project of the same name.
func TestAnAgentWithTheRoleArchitectIsAnArchitectNode(t *testing.T) {
	s := teamStore(t)
	if err := s.GraphLifecycle("agent.create", protocol.GraphLifecycleRequest{Name: "planner", Role: "architect"}, "owner", false); err != nil {
		t.Fatal(err)
	}
	if got := nodeKinds(t, s)["planner"]; got != "architect" {
		t.Fatalf("kind %q", got)
	}
	if err := s.GraphLifecycle("repo.create", protocol.GraphLifecycleRequest{Name: "planner"}, "owner", false); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := s.db.QueryRow(`SELECT id FROM repos WHERE id LIKE 'planner%'`).Scan(&id); err != nil || id != "planner~repo" {
		t.Fatalf("a new repo named like an architect keeps its own id: %q %v", id, err)
	}
}

// A rewind replays the shipped steps, and some of them read graph_nodes. The
// replay gets the table again, filled from the new sources, so the sandbox
// cutover sees the project names the old layout gave, and the fold at the end
// drops it again.
func TestGraphNodesFoldReplaysAfterARewind(t *testing.T) {
	// The second marker is the oldest step the other rewind tests replay. It
	// runs the typed-graph triggers and the project identity step on the way.
	for _, marker := range []string{"worker sandbox cutover", "ALTER TABLE inbox_items ADD COLUMN options"} {
		t.Run(marker, func(t *testing.T) { replayFrom(t, marker) })
	}
}

func replayFrom(t *testing.T, marker string) {
	s, _ := foldStore(t)
	s = reopen(t, s)
	before := workerNames(t, s)
	if len(before) == 0 || before["ghost"] == nil || before["arch"] == nil {
		t.Fatalf("names before the rewind: %v", before)
	}
	mustExec(t, s, `DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, marker))
	if err := migrateOldFixture(t, s); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if tableExists(t, s, "graph_nodes") {
		t.Fatal("the replay left graph_nodes")
	}
	after := workerNames(t, s)
	if !reflect.DeepEqual(namesOf(after), namesOf(before)) {
		t.Fatalf("project names\n got %v\nwant %v", namesOf(after), namesOf(before))
	}
}

func workerNames(t testing.TB, s *Store) map[string][]string {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	names, err := workerProjectNamesTx(tx)
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func namesOf(m map[string][]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
