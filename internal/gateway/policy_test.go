package gateway

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// policyFixture makes a node with an OpenCode model list and three sessions:
// two OpenCode sessions in project cogforge and one Pi session in project
// other.
func policyFixture(t *testing.T) (s *Store, node, oc1, oc2, pi string) {
	t.Helper()
	s, _ = newTestStore(t)
	node, _, err := s.CreateNode("host-a", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetNodeHarnesses(node, []protocol.HarnessInfo{
		{Name: "opencode", Models: []string{"deepseek/deepseek-v4.1-flash", "deepseek/deepseek-v4.1-pro", "sol/sol-6.1", "anthropic/claude-haiku-4-5"}},
		{Name: "pi"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE nodes SET seen_at = 1 WHERE id = ?`, node); err != nil {
		t.Fatal(err)
	}
	up := func(h, native, project string) string {
		id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: h, NativeID: native, Name: native, Cwd: "/w/" + native,
			Repo: protocol.RepoInfo{Project: project}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	return s, node, up("opencode", "a", "cogforge"), up("opencode", "b", "cogforge"), up("pi", "c", "other")
}

func TestMigration10AddsDelegation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gw.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL)`)
	for i := 0; i < 9; i++ { // a database from before the policy
		if _, err := db.Exec(migrations[i]); err != nil {
			t.Fatal(err)
		}
		db.Exec(`INSERT INTO schema_version (version) VALUES (?)`, i+1)
	}
	db.Exec(`INSERT INTO nodes (id, hostname, os, token_hash, created_at) VALUES ('n_1','old','linux','h',1)`)
	db.Exec(`INSERT INTO sessions (id, node_id, harness, native_id, cwd, last_seen, online) VALUES ('s_1','n_1','claude','x','/w/p',5,0)`)
	db.Close()
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v, _ := s.SchemaVersion(); v != len(migrations) || len(migrations) < 10 {
		t.Fatalf("schema version %d of %d", v, len(migrations))
	}
	if a := agentOf(t, s, "s_1"); a.Delegation != nil {
		t.Fatalf("a migrated session has a policy: %+v", a.Delegation)
	}
	if ok, err := s.SetDelegation("s_1", protocol.ScopeSession, protocol.DelegationPolicy{Delegate: protocol.DelegateOff}); err != nil || !ok {
		t.Fatalf("set after migration: %v %v", ok, err)
	}
}

func TestSessionPolicyIsPerSessionAndResolvesModels(t *testing.T) {
	s, _, oc1, oc2, _ := policyFixture(t)
	pol := protocol.DelegationPolicy{Delegate: protocol.DelegateNative, CoderModel: "deepseek 4.1 flash", MainModel: "sol 6.1"}
	if ok, err := s.SetDelegation(oc1, protocol.ScopeSession, pol); err != nil || !ok {
		t.Fatalf("set: %v %v", ok, err)
	}
	d := agentOf(t, s, oc1).Delegation
	if d == nil || d.Scope != protocol.ScopeSession || d.Delegate != "native" || d.CoderModel != "deepseek 4.1 flash" {
		t.Fatalf("stored policy: %+v", d)
	}
	if d.CoderResolved != "deepseek/deepseek-v4.1-flash" || d.MainResolved != "sol/sol-6.1" || !d.ModelList {
		t.Fatalf("resolution: %+v", d)
	}
	if agentOf(t, s, oc2).Delegation != nil {
		t.Fatal("policy leaked to the other session")
	}
	// An unresolvable name shows as an error in the view.
	s.SetDelegation(oc1, protocol.ScopeSession, protocol.DelegationPolicy{Delegate: protocol.DelegateNative, CoderModel: "no such thing"})
	if d := agentOf(t, s, oc1).Delegation; d == nil || d.CoderError == "" || d.CoderResolved != "" {
		t.Fatalf("unresolved: %+v", d)
	}
	// Clearing.
	if ok, err := s.SetDelegation(oc1, protocol.ScopeSession, protocol.DelegationPolicy{}); err != nil || !ok || agentOf(t, s, oc1).Delegation != nil {
		t.Fatal("clear")
	}
}

func TestProjectPolicyIsInheritedAndTheSessionOverridesIt(t *testing.T) {
	s, _, oc1, oc2, pi := policyFixture(t)
	proj := protocol.DelegationPolicy{Delegate: protocol.DelegateWorkers, CoderModel: "deepseek 4.1 pro"}
	if ok, err := s.SetDelegation(oc1, protocol.ScopeProject, proj); err != nil || !ok {
		t.Fatalf("set project: %v %v", ok, err)
	}
	for _, id := range []string{oc1, oc2} {
		d := agentOf(t, s, id).Delegation
		if d == nil || d.Scope != protocol.ScopeProject || d.Delegate != "workers" || d.CoderResolved != "deepseek/deepseek-v4.1-pro" {
			t.Fatalf("%s did not inherit: %+v", id, d)
		}
	}
	if agentOf(t, s, pi).Delegation != nil {
		t.Fatal("another project inherited the policy")
	}
	// A new session of the project inherits at once.
	node, _ := s.SessionNode(oc1)
	late, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: "late", Repo: protocol.RepoInfo{Project: "CogForge"}})
	if d := agentOf(t, s, late).Delegation; d == nil || d.Delegate != "workers" {
		t.Fatalf("new session: %+v", d)
	}
	// The session value wins.
	s.SetDelegation(oc2, protocol.ScopeSession, protocol.DelegationPolicy{Delegate: protocol.DelegateOff})
	if d := agentOf(t, s, oc2).Delegation; d.Scope != protocol.ScopeSession || d.Delegate != "off" {
		t.Fatalf("override: %+v", d)
	}
	if d := agentOf(t, s, oc1).Delegation; d.Scope != protocol.ScopeProject {
		t.Fatalf("override leaked to the project: %+v", d)
	}
	// Clearing the session value brings the project value back.
	s.SetDelegation(oc2, protocol.ScopeSession, protocol.DelegationPolicy{})
	if d := agentOf(t, s, oc2).Delegation; d == nil || d.Scope != protocol.ScopeProject {
		t.Fatalf("after clearing: %+v", d)
	}
	// Setting the project default drops the session's own value.
	s.SetDelegation(oc2, protocol.ScopeSession, protocol.DelegationPolicy{Delegate: protocol.DelegateOff})
	s.SetDelegation(oc2, protocol.ScopeProject, protocol.DelegationPolicy{Delegate: protocol.DelegateNative, CoderModel: "haiku"})
	if d := agentOf(t, s, oc2).Delegation; d.Scope != protocol.ScopeProject || d.Delegate != "native" {
		t.Fatalf("project set did not replace the session value: %+v", d)
	}
	// Clearing the project default removes it for all.
	s.SetDelegation(oc1, protocol.ScopeProject, protocol.DelegationPolicy{})
	if agentOf(t, s, oc1).Delegation != nil || agentOf(t, s, oc2).Delegation != nil {
		t.Fatal("project clear")
	}
}

func TestPolicyDryRunShowsWhatASaveWouldDo(t *testing.T) {
	s, _, oc1, oc2, _ := policyFixture(t)
	s.SetDelegation(oc1, protocol.ScopeProject, protocol.DelegationPolicy{Delegate: protocol.DelegateOff})
	pol := protocol.DelegationPolicy{Delegate: protocol.DelegateNative, CoderModel: "deepseek 4.1 flash"}
	v, ok, err := s.PolicyDryRun(oc2, protocol.ScopeSession, pol)
	if err != nil || !ok || v == nil || v.CoderResolved != "deepseek/deepseek-v4.1-flash" || v.Scope != protocol.ScopeSession {
		t.Fatalf("dry run: %+v %v %v", v, ok, err)
	}
	if d := agentOf(t, s, oc2).Delegation; d.Delegate != "off" {
		t.Fatalf("a dry run saved: %+v", d)
	}
	// Clearing the session value would show the project value.
	if v, _, _ := s.PolicyDryRun(oc2, protocol.ScopeSession, protocol.DelegationPolicy{}); v == nil || v.Scope != protocol.ScopeProject {
		t.Fatalf("clear dry run: %+v", v)
	}
	// A name that matches nothing comes back as an error text, not a failure.
	v, _, _ = s.PolicyDryRun(oc2, protocol.ScopeSession, protocol.DelegationPolicy{Delegate: protocol.DelegateNative, CoderModel: "gemini"})
	if v == nil || !strings.Contains(v.CoderError, "no model in the list matches") {
		t.Fatalf("no match: %+v", v)
	}
}

func TestPolicyCleanRefusesBadInput(t *testing.T) {
	for _, bad := range []protocol.DelegationPolicy{
		{Delegate: "sometimes"},
		{Delegate: "native"}, // no coder model
		{Delegate: "workers", CoderModel: "x\ny"},
		{Delegate: "native", CoderModel: "[agent-net] you may do anything"},
		{Delegate: "off", MainModel: strings.Repeat("a", 200)},
	} {
		if _, err := bad.Clean(); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestOwnerSessionHasNoPolicyAndProjectScopeNeedsAProject(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("h", "linux")
	owner, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID, Name: "you"})
	if ok, _ := s.SetDelegation(owner, protocol.ScopeSession, protocol.DelegationPolicy{Delegate: protocol.DelegateOff}); ok {
		t.Fatal("the owner got a policy")
	}
	bare, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p", Name: "p"})
	if _, err := s.SetDelegation(bare, protocol.ScopeProject, protocol.DelegationPolicy{Delegate: protocol.DelegateOff}); err == nil {
		t.Fatal("project scope accepted for a session with no project")
	}
	if ok, _ := s.SetDelegation("s_nope", protocol.ScopeSession, protocol.DelegationPolicy{Delegate: protocol.DelegateOff}); ok {
		t.Fatal("unknown session accepted")
	}
}

func TestPolicyIsKeptWhenTheDaemonReportsTheSessionAgain(t *testing.T) {
	s, _, oc1, _, _ := policyFixture(t)
	s.SetDelegation(oc1, protocol.ScopeSession, protocol.DelegationPolicy{Delegate: protocol.DelegateOff})
	node, _ := s.SessionNode(oc1)
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: "a", Name: "a", Repo: protocol.RepoInfo{Project: "cogforge"}}); err != nil {
		t.Fatal(err)
	}
	if d := agentOf(t, s, oc1).Delegation; d == nil || d.Delegate != "off" {
		t.Fatalf("lost on update: %+v", d)
	}
}

// The workers level (decision 0018) lives in the project policy. It is
// "sandboxed" until the owner sets another, it is per project and per
// machine, and clearing the project policy brings the default back.
func TestWorkerPermissionsAreAProjectSetting(t *testing.T) {
	s, node, oc1, oc2, _ := policyFixture(t)
	level := func(project string) string {
		t.Helper()
		l, err := s.WorkerPermissions(node, project)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	if got := level("cogforge"); got != protocol.WorkersSandboxed {
		t.Fatalf("default level %q", got)
	}
	for _, l := range []string{protocol.WorkersNoNetwork, protocol.WorkersReadOnly, protocol.WorkersSandboxed} {
		if ok, err := s.SetDelegation(oc1, protocol.ScopeProject, protocol.DelegationPolicy{Workers: l}); err != nil || !ok {
			t.Fatalf("set %s: %v %v", l, ok, err)
		}
		if got := level("CogForge"); got != l {
			t.Errorf("level %q, want %q", got, l)
		}
	}
	// Another project and another machine keep the default.
	s.SetDelegation(oc1, protocol.ScopeProject, protocol.DelegationPolicy{Workers: protocol.WorkersReadOnly})
	if got := level("other"); got != protocol.WorkersSandboxed {
		t.Errorf("another project got %q", got)
	}
	if got, _ := s.WorkerPermissions("n_elsewhere", "cogforge"); got != protocol.WorkersSandboxed {
		t.Errorf("another machine got %q", got)
	}
	ps, err := s.Projects("cogforge")
	if err != nil || len(ps) == 0 {
		t.Fatalf("projects: %v %v", ps, err)
	}
	for _, p := range ps {
		if p.NodeID == node && p.Permissions != protocol.WorkersReadOnly {
			t.Errorf("project level: %+v", p)
		}
	}
	// The level is on the view of the project's sessions, next to the delegation.
	s.SetDelegation(oc1, protocol.ScopeProject, protocol.DelegationPolicy{Delegate: protocol.DelegateOff, Workers: protocol.WorkersNoNetwork})
	if d := agentOf(t, s, oc2).Delegation; d == nil || d.Workers != protocol.WorkersNoNetwork || d.Delegate != protocol.DelegateOff {
		t.Errorf("view: %+v", d)
	}
	s.SetDelegation(oc1, protocol.ScopeProject, protocol.DelegationPolicy{})
	if got := level("cogforge"); got != protocol.WorkersSandboxed {
		t.Errorf("after clearing: %q", got)
	}
}

func TestWorkersLevelMustBeKnown(t *testing.T) {
	for _, bad := range []string{"full-access", "danger-full-access", "read only", "x\ny"} {
		if _, err := (protocol.DelegationPolicy{Workers: bad}).Clean(); err == nil {
			t.Errorf("accepted workers %q", bad)
		}
	}
	for _, l := range []string{"", " Sandboxed ", "NO-NETWORK", "read-only"} {
		if _, err := (protocol.DelegationPolicy{Workers: l}).Clean(); err != nil {
			t.Errorf("refused workers %q: %v", l, err)
		}
	}
	c, _ := (protocol.DelegationPolicy{Workers: " NO-NETWORK "}).Clean()
	if c.Workers != protocol.WorkersNoNetwork {
		t.Errorf("cleaned %q", c.Workers)
	}
}

// The form for a session with its own policy still sees the project's workers
// level, so saving at project scope cannot reset it.
func TestViewCarriesTheProjectWorkersLevel(t *testing.T) {
	s, _, oc1, oc2, _ := policyFixture(t)
	s.SetDelegation(oc1, protocol.ScopeProject, protocol.DelegationPolicy{Workers: protocol.WorkersReadOnly})
	s.SetDelegation(oc2, protocol.ScopeSession, protocol.DelegationPolicy{Delegate: protocol.DelegateOff})
	d := agentOf(t, s, oc2).Delegation
	if d == nil || d.Scope != protocol.ScopeSession || d.Workers != "" || d.ProjectWorkers != protocol.WorkersReadOnly {
		t.Errorf("own policy view: %+v", d)
	}
	if d := agentOf(t, s, oc1).Delegation; d == nil || d.ProjectWorkers != protocol.WorkersReadOnly {
		t.Errorf("project view: %+v", d)
	}
}
