package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// seedArchiveSkill stores one version with known tokens, so a test can tell a
// pin from the latest version.
func seedArchiveSkill(t *testing.T, s *Store, name string, version, tokens int, description, body string) {
	t.Helper()
	dir := t.TempDir()
	text := "---\ndescription: " + description + "\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := inventory.SkillHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := inventory.PackSkill(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO skill_archive(name,version,hash,content,description,tokens,context_tokens,source_kind,source_node,source_path,added_by,added_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, name, version, hash, bundle, description, tokens, skillContextTokens(name, description), "machine", "n", "/skills/"+name, "test", 0); err != nil {
		t.Fatal(err)
	}
}

func seedRepo(t *testing.T, s *Store, repo, maturity string) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO repos(id,display_name,created_at,updated_at) VALUES(?,?,0,0)`, repo, repo); err != nil {
		t.Fatal(err)
	}
	if maturity != "" {
		if _, err := s.db.Exec(`INSERT INTO graph_charter(node,attr,value,updated_at,updated_by) VALUES(?,'maturity',?,0,'owner')`, repo, maturity); err != nil {
			t.Fatal(err)
		}
	}
}

func setSkills(t *testing.T, s *Store, level, scope string, add, remove []string, pins map[string]int) protocol.SkillSetResult {
	t.Helper()
	res, err := s.SkillListSet(protocol.SkillSetRequest{Level: level, Scope: scope, Add: add, Remove: remove, Pins: pins, By: "cto"})
	if err != nil {
		t.Fatalf("set %s %s: %v", level, scope, err)
	}
	return res
}

func effective(t *testing.T, s *Store, level, scope string) []protocol.EffectiveSkill {
	t.Helper()
	nodes, err := s.SkillListsEffective(level, scope)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("effective %s %s: %+v %v", level, scope, nodes, err)
	}
	return nodes[0].Skills
}

func TestSkillListsInheritGlobalTeamRepoAgent(t *testing.T) {
	s, _ := newTestStore(t)
	seedArchiveSkill(t, s, "alpha", 1, 10, "Alpha skill.", "a")
	seedArchiveSkill(t, s, "beta", 1, 20, "Beta skill.", "b")
	seedArchiveSkill(t, s, "gamma", 1, 30, "Gamma skill.", "c")
	seedRepo(t, s, "alpha", "growing")
	if _, err := s.db.Exec(`INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES('ag','Ag','',0,0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE repos SET owner_agent_id='ag' WHERE id='alpha'`); err != nil {
		t.Fatal(err)
	}
	setSkills(t, s, "global", "", []string{"alpha", "beta"}, nil, nil)
	setSkills(t, s, "repo", "alpha", nil, []string{"beta"}, nil)
	setSkills(t, s, "agent", "ag", []string{"gamma"}, nil, nil)

	agent := effective(t, s, "agent", "ag")
	if len(agent) != 2 || agent[0].Name != "alpha" || agent[0].Level != "global" || agent[1].Name != "gamma" || agent[1].Level != "agent" {
		t.Fatalf("agent list: %+v", agent)
	}
	repo := effective(t, s, "repo", "alpha")
	if len(repo) != 1 || repo[0].Name != "alpha" || repo[0].SizeTokens != 10 {
		t.Fatalf("repo list: %+v", repo)
	}
}

func TestSkillListPinHoldsAndLatestFollows(t *testing.T) {
	s, _ := newTestStore(t)
	seedRepo(t, s, "alpha", "growing")
	seedArchiveSkill(t, s, "alpha", 1, 10, "First.", "one")
	seedArchiveSkill(t, s, "alpha", 2, 25, "Second.", "two")
	setSkills(t, s, "repo", "alpha", []string{"alpha"}, nil, map[string]int{"alpha": 1})
	if got := effective(t, s, "repo", "alpha"); got[0].Version != 1 || got[0].SizeTokens != 10 || got[0].ContextTokens != skillContextTokens("alpha", "First.") || got[0].Pin != 1 {
		t.Fatalf("pinned: %+v", got)
	}
	seedArchiveSkill(t, s, "alpha", 3, 40, "Third.", "three")
	if got := effective(t, s, "repo", "alpha"); got[0].Version != 1 {
		t.Fatalf("pin moved to %d", got[0].Version)
	}
	setSkills(t, s, "repo", "alpha", []string{"alpha"}, nil, nil)
	if got := effective(t, s, "repo", "alpha"); got[0].Version != 3 || got[0].SizeTokens != 40 || got[0].Pin != 0 {
		t.Fatalf("latest: %+v", got)
	}
}

func TestSkillListSetPinsMatureRepo(t *testing.T) {
	s, _ := newTestStore(t)
	seedRepo(t, s, "alpha", protocol.MaturityMature)
	seedRepo(t, s, "beta", protocol.MaturityGrowing)
	seedArchiveSkill(t, s, "alpha", 1, 10, "First.", "one")
	mature := setSkills(t, s, "repo", "alpha", []string{"alpha"}, nil, nil)
	if !mature.PinnedByMaturity || len(mature.Pinned) != 1 || mature.Pinned[0] != "alpha" {
		t.Fatalf("mature set: %+v", mature)
	}
	seedArchiveSkill(t, s, "alpha", 2, 20, "Second.", "two")
	if got := effective(t, s, "repo", "alpha"); got[0].Version != 1 {
		t.Fatalf("mature repo moved to %d", got[0].Version)
	}
	growing := setSkills(t, s, "repo", "beta", []string{"alpha"}, nil, nil)
	if growing.PinnedByMaturity || len(growing.Pinned) != 0 {
		t.Fatalf("growing repo pinned: %+v", growing)
	}
	if got := effective(t, s, "repo", "beta"); got[0].Version != 2 {
		t.Fatalf("growing repo version %d", got[0].Version)
	}
}

func TestSkillListSetBySuggestion(t *testing.T) {
	g := archiveGateway(t)
	s := g.store
	seedRepo(t, s, "alpha", "growing")
	seedArchiveSkill(t, s, "grill-me", 1, 12, "Ask hard questions about a design and its tradeoffs.", "q")
	seedArchiveSkill(t, s, "bailian", 1, 90, "Run Alibaba cloud models.", "b")
	if _, err := s.db.Exec(`INSERT INTO sessions(id,node_id,harness,native_id,repo_project,repo_description,last_seen,online)
		VALUES('s_x','n','claude','x','alpha','A design review tool for architecture tradeoffs',0,1)`); err != nil {
		t.Fatal(err)
	}
	suggestion, err := g.skillSuggest(protocol.SkillSuggestRequest{Level: "repo", Scope: "alpha", By: "cto"})
	if err != nil {
		t.Fatal(err)
	}
	if suggestion.ID == "" || len(suggestion.Skills) != 1 || suggestion.Skills[0].Name != "grill-me" || suggestion.Skills[0].Reason == "" {
		t.Fatalf("suggestion: %+v", suggestion)
	}
	// The suggestion shows both figures and totals the one a session loads.
	grillContext := skillContextTokens("grill-me", "Ask hard questions about a design and its tradeoffs.")
	if got := suggestion.Skills[0]; got.SizeTokens != 12 || got.ContextTokens != grillContext || suggestion.ContextTokens != grillContext {
		t.Fatalf("suggestion tokens: %+v", suggestion)
	}
	res, err := s.SkillListSet(protocol.SkillSetRequest{Level: "repo", Scope: "alpha", Suggestion: suggestion.ID, By: "cto"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Change == "" || len(res.Added) != 1 || res.Added[0] != "grill-me" || res.AfterTokens != grillContext {
		t.Fatalf("set by suggestion: %+v", res)
	}
	// The suggestion belongs to its node: another repo is refused.
	seedRepo(t, s, "beta", "growing")
	if _, err = s.SkillListSet(protocol.SkillSetRequest{Level: "repo", Scope: "beta", Suggestion: suggestion.ID, By: "cto"}); err == nil {
		t.Fatal("a suggestion was applied to another repo")
	}
}

func TestSkillListUndoRestoresPreviousList(t *testing.T) {
	s, _ := newTestStore(t)
	seedRepo(t, s, "alpha", "growing")
	seedArchiveSkill(t, s, "alpha", 1, 10, "Alpha.", "a")
	seedArchiveSkill(t, s, "beta", 1, 20, "Beta.", "b")
	setSkills(t, s, "repo", "alpha", []string{"alpha"}, nil, nil)
	second := setSkills(t, s, "repo", "alpha", []string{"beta"}, []string{"alpha"}, nil)
	if len(second.Added) != 1 || second.Added[0] != "beta" || len(second.Removed) != 1 || second.Removed[0] != "alpha" {
		t.Fatalf("second set: %+v", second)
	}
	if _, err := s.SkillListUndo(second.Change); err != nil {
		t.Fatal(err)
	}
	got := effective(t, s, "repo", "alpha")
	if len(got) != 1 || got[0].Name != "alpha" {
		t.Fatalf("after undo: %+v", got)
	}
	if _, err := s.SkillListUndo(second.Change); err == nil {
		t.Fatal("undo was accepted twice")
	}
}

func TestSkillListsEffectiveCarriesSource(t *testing.T) {
	s, _ := newTestStore(t)
	seedRepo(t, s, "alpha", "growing")
	seedArchiveSkill(t, s, "alpha", 1, 10, "Alpha.", "a")
	seedArchiveSkill(t, s, "beta", 1, 20, "Beta.", "b")
	setSkills(t, s, "global", "", []string{"alpha"}, nil, nil)
	setSkills(t, s, "repo", "alpha", []string{"beta"}, nil, nil)
	nodes, err := s.SkillListsEffective("", "")
	if err != nil {
		t.Fatal(err)
	}
	var repo *protocol.EffectiveSkills
	for i := range nodes {
		if nodes[i].Level == "repo" && nodes[i].Scope == "alpha" {
			repo = &nodes[i]
		}
	}
	if repo == nil || len(repo.Skills) != 2 || repo.Skills[0].Name != "alpha" || repo.Skills[0].Level != "global" || repo.Skills[1].Level != "repo" || repo.ContextTokens != skillContextTokens("alpha", "Alpha.")+skillContextTokens("beta", "Beta.") {
		t.Fatalf("repo effective: %+v", repo)
	}
}

func TestSkillListRestartOfferListsNodeSessions(t *testing.T) {
	s, _ := newTestStore(t)
	seedRepo(t, s, "alpha", "growing")
	seedArchiveSkill(t, s, "alpha", 1, 10, "Alpha.", "a")
	node, _, err := s.CreateNode("h1", "linux")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, id := range []string{"a", "b"} {
		session, e := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: id, Name: "name-" + id, Repo: protocol.RepoInfo{Project: "alpha"}})
		if e != nil {
			t.Fatal(e)
		}
		want[session] = true
	}
	if _, err = s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "other", Name: "other", Repo: protocol.RepoInfo{Project: "beta"}}); err != nil {
		t.Fatal(err)
	}
	res := setSkills(t, s, "repo", "alpha", []string{"alpha"}, nil, nil)
	if len(res.Restart.Sessions) != 2 || res.Restart.Call == "" {
		t.Fatalf("restart: %+v", res.Restart)
	}
	for _, session := range res.Restart.Sessions {
		if !want[session.SessionID] {
			t.Errorf("unrelated session in the list: %+v", session)
		}
	}
}

func TestSkillSuggestFlagsMissingMCP(t *testing.T) {
	g := archiveGateway(t)
	s := g.store
	seedRepo(t, s, "alpha", "growing")
	seedArchiveSkill(t, s, "jira-helper", 1, 12, "Work with Jira issues.", "Call mcp__jira__create_issue for a new issue.")
	if _, err := s.db.Exec(`INSERT INTO sessions(id,node_id,harness,native_id,repo_project,repo_description,last_seen,online)
		VALUES('s_x','n','claude','x','alpha','Track Jira issues for the team',0,1)`); err != nil {
		t.Fatal(err)
	}
	got, err := g.skillSuggest(protocol.SkillSuggestRequest{Level: "repo", Scope: "alpha", By: "cto"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Skills) != 1 || len(got.Skills[0].MissingMcp) != 1 || got.Skills[0].MissingMcp[0] != "jira" {
		t.Fatalf("missing MCP: %+v", got.Skills)
	}
	node, _, err := s.CreateNode("h1", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AddMCPServer(protocol.MCPServer{Name: "jira", NodeID: node, Transport: "stdio", Command: "jira", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	got, err = g.skillSuggest(protocol.SkillSuggestRequest{Level: "repo", Scope: "alpha", By: "cto"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Skills) != 1 || len(got.Skills[0].MissingMcp) != 0 {
		t.Fatalf("present MCP still flagged: %+v", got.Skills)
	}
}

func TestNewProjectSendsOneSkillNoticeToCTO(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.db.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES('cto_session','s_cto',0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddProjects("n1", protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Path: "/repo/x", Name: "x"}}}); err != nil {
		t.Fatal(err)
	}
	var text, next string
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE source_ref='skills:new-project:x' AND status='open'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("notices: %d", n)
	}
	if err := s.db.QueryRow(`SELECT text, next_who FROM inbox_items WHERE source_ref='skills:new-project:x'`).Scan(&text, &next); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "run skills suggest and set") || next != "s_cto" {
		t.Fatalf("notice: %q next %q", text, next)
	}
	if err := s.newProjectSkillNotice("x"); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE source_ref='skills:new-project:x'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("a second notice was added: %d", n)
	}
}
