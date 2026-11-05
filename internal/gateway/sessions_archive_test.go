package gateway

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestSessionsArchivePatternRestore(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	id := shopSession(t, s, node, "junk")
	if _, err := s.RenameSession(id, "judge-junk"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionOffline(node, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET graph_excluded=2 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	before, err := s.GraphView("", 10)
	if err != nil || len(before.Nodes) != 1 || before.Nodes[0].Name != "shop" {
		t.Fatalf("graph fixture: %+v %v", before, err)
	}
	q := protocol.SessionsArchiveRequest{Name: "judge-*", DryRun: true}
	preview, err := s.ArchiveSessions(q)
	if err != nil || len(preview.Sessions) != 1 || preview.Sessions[0].SessionID != id {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	if _, ok, _ := s.Agent(id); !ok {
		t.Fatal("dry run changed session")
	}
	q.DryRun = false
	if _, err = s.ArchiveSessions(q); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Agent(id); ok {
		t.Fatal("archived session still discoverable")
	}
	agents, err := s.Agents(protocol.QueryAgents{})
	if err != nil || len(agents) != 0 {
		t.Fatalf("list: %+v %v", agents, err)
	}
	graph, err := s.GraphView("", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range graph.Nodes {
		if n.Name == "shop" {
			t.Fatal("archived project remains in graph")
		}
	}
	all, err := s.Graph()
	if err != nil || len(all.Nodes) != 0 {
		t.Fatalf("raw graph includes archived project: %+v %v", all, err)
	}
	projects, err := s.Projects("")
	if err != nil || len(projects) != 0 {
		t.Fatalf("project observations: %+v %v", projects, err)
	}
	counts, err := s.SessionCounts(24 * time.Hour)
	if err != nil || counts.OfflineTotal != 0 {
		t.Fatalf("counts include archive: %+v %v", counts, err)
	}
	var archived int64
	if err = s.db.QueryRow(`SELECT archived_at FROM sessions WHERE id=?`, id).Scan(&archived); err != nil || archived == 0 {
		t.Fatalf("row lost: %d %v", archived, err)
	}
	if name := s.displayName(id); name != "judge-junk" {
		t.Fatalf("history name: %q", name)
	}
	if _, err = s.CleanWorkers(node, time.Nanosecond, nil); err != nil {
		t.Fatal(err)
	}
	listed, err := s.ArchivedSessions()
	if err != nil || len(listed.Sessions) != 1 {
		t.Fatalf("archived: %+v %v", listed, err)
	}
	if _, err = s.ArchiveSessions(protocol.SessionsArchiveRequest{IDs: []string{id}, Restore: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Agent(id); !ok {
		t.Fatal("restore did not return session")
	}
}

func TestSessionsArchiveRefusesOnlineAndPinsPreview(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	id := shopSession(t, s, node, "live")
	if _, err := s.ArchiveSessions(protocol.SessionsArchiveRequest{IDs: []string{id}}); err == nil {
		t.Fatal("online session archived")
	}
	if err := s.SetSessionOffline(node, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ArchiveSessions(protocol.SessionsArchiveRequest{IDs: []string{id}, Expected: []string{"different"}}); err == nil {
		t.Fatal("stale preview accepted")
	}
	if _, ok, _ := s.Agent(id); !ok {
		t.Fatal("rejected operation changed session")
	}
}

func TestSessionsArchiveSelectorsAndHistory(t *testing.T) {
	s, clock := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	worker, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "worker", Name: "Review junk", Cwd: "/tmp/judge/run", Repo: protocol.RepoInfo{Root: "/code/junk", Project: "junk"}, Worker: &protocol.WorkerInfo{ID: "w_junk", StartedAt: clock.t.Unix() - 60, Title: "Review junk", Project: "junk"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RenameSession(worker, "junk-worker"); err != nil {
		t.Fatal(err)
	}
	ordinary := shopSession(t, s, node, "ordinary")
	if err = s.SetSessionOffline(node, worker); err != nil {
		t.Fatal(err)
	}
	if err = s.SetSessionOffline(node, ordinary); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: worker, Harness: "codex", Outcome: protocol.OutcomeComplete, StartedAt: clock.t.Unix() - 60, EndedAt: clock.t.Unix(), CallerSessionID: worker}); err != nil {
		t.Fatal(err)
	}
	insertConversationMessage(t, s, "m_archive", "c_archive", worker, ordinary, "retained", clock.t)
	project := "junk"
	tests := []protocol.SessionsArchiveRequest{
		{IDs: []string{worker}}, {Name: "junk-*"}, {Folder: "/tmp/judge/*"}, {Project: &project}, {Harness: "codex"}, {EndedWorkers: true},
		{Harness: "codex", CreatedBefore: clock.t.Add(24 * time.Hour).Format("2006-01-02")},
	}
	for _, q := range tests {
		q.DryRun = true
		got, err := s.ArchiveSessions(q)
		if err != nil || got.Count != 1 || got.Sessions[0].SessionID != worker {
			t.Fatalf("selector %+v: %+v %v", q, got, err)
		}
	}
	// A prior completed run does not make a continuing offline worker ended.
	if _, err = s.db.Exec(`UPDATE sessions SET worker_run_active=1 WHERE id=?`, worker); err != nil {
		t.Fatal(err)
	}
	got, err := s.ArchiveSessions(protocol.SessionsArchiveRequest{EndedWorkers: true, DryRun: true})
	if err != nil || got.Count != 0 {
		t.Fatalf("active run matched ended workers: %+v %v", got, err)
	}
	if _, err = s.db.Exec(`UPDATE sessions SET worker_run_active=0 WHERE id=?`, worker); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ArchiveSessions(protocol.SessionsArchiveRequest{EndedWorkers: true}); err != nil {
		t.Fatal(err)
	}
	msg, ok, err := s.Message("m_archive")
	if err != nil || !ok || msg.FromName != "junk-worker" {
		t.Fatalf("message lost name: %+v %v", msg, err)
	}
	runs, err := s.WorkerLog(10)
	if err != nil || len(runs) != 1 || runs[0].Title != "Review junk" || runs[0].Caller != "junk-worker" {
		t.Fatalf("run lost names: %+v %v", runs, err)
	}
	// All cleanup routes must leave the archive and its profile restorable.
	if _, err = s.CleanWorkers(node, time.Nanosecond, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CleanWorktreeSessions(node, "/tmp"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Prune(Retention{Sessions: time.Nanosecond}); err != nil {
		t.Fatal(err)
	}
	if got := s.displayName(worker); got != "junk-worker" {
		t.Fatalf("cleanup lost archived name: %q", got)
	}
}

func TestSessionsArchiveNoProjectAndLiveRegistration(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	in := protocol.SessionInfo{Harness: "pi", NativeID: "unassigned", Cwd: "/tmp/run", NoProject: true, Repo: protocol.RepoInfo{Project: "ignored"}}
	id, err := s.UpsertSession(node, in)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetSessionOffline(node, id); err != nil {
		t.Fatal(err)
	}
	empty := ""
	got, err := s.ArchiveSessions(protocol.SessionsArchiveRequest{Project: &empty, DryRun: true})
	if err != nil || got.Count != 1 {
		t.Fatalf("no project: %+v %v", got, err)
	}
	if _, err = s.ArchiveSessions(protocol.SessionsArchiveRequest{IDs: []string{id}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpsertSession(node, in); err != nil {
		t.Fatal(err)
	}
	if a, ok, err := s.Agent(id); err != nil || !ok || !a.Online {
		t.Fatalf("live report did not restore: %+v %v", a, err)
	}
}

func TestSessionsArchiveCTOSyncRestoresVisibility(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	id, err := s.RegisterCTOSession(node)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetSessionOffline(node, id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ArchiveSessions(protocol.SessionsArchiveRequest{IDs: []string{id}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SyncSessions(node, nil); err != nil {
		t.Fatal(err)
	}
	if a, ok, err := s.Agent(id); err != nil || !ok || !a.Online {
		t.Fatalf("host sync left CTO archived: %+v %v", a, err)
	}
}

func TestSessionsArchiveRevokedBot(t *testing.T) {
	s, _ := newTestStore(t)
	app, err := s.CreateApp("test bot", "bot", "test", "Test", nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.touchBot(app)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ArchiveSessions(protocol.SessionsArchiveRequest{IDs: []string{id}}); err == nil {
		t.Fatal("archived active bot")
	}
	if err = s.RevokeApp(app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ArchiveSessions(protocol.SessionsArchiveRequest{IDs: []string{id}}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Agent(id); ok {
		t.Fatal("archive reported success without hiding revoked bot")
	}
}
