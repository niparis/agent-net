package gateway

import (
	"context"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestWorkerEarlyQuitTrackAndPicker(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("test", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeHello(node, "test", "test"); err != nil {
		t.Fatal(err)
	}
	saidComplete := false
	e := &protocol.WorkerEvidence{Complete: &saidComplete, FilesChanged: 1, LinesChanged: 6, RunSeconds: 79, EarlyQuit: true}
	if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: "worker", Harness: "codex", Model: "model", Outcome: protocol.OutcomePartial, Difficulty: "hard", Evidence: e}); err != nil {
		t.Fatal(err)
	}
	tr, ok, err := s.TrackRecord(node, "codex", "model", 0)
	if err != nil || !ok || tr.EarlyQuits != 1 || tr.Partial != 1 {
		t.Fatalf("track: %+v %v", tr, err)
	}
	sub, ok, err := s.TrackSubject(protocol.SubjectModel, "model", 0)
	if err != nil || !ok || sub.EarlyQuits != 1 {
		t.Fatalf("subject: %+v %v", sub, err)
	}
	levels, err := s.recentEarlyQuitLevels(node)
	if err != nil || !levels["codex\x00model"]["hard"] || levels["codex\x00model"]["simple"] {
		t.Fatalf("picker levels: %v %v", levels, err)
	}
	g := &Gateway{store: s, conns: map[string]*nodeConn{}}
	page, err := g.MachineGet(context.Background(), protocol.MachineGetRequest{Machine: node, Include: []string{"track_record"}})
	if err != nil || page.TrackRecord == nil || len(page.TrackRecord.Items) != 1 || page.TrackRecord.Items[0].EarlyQuits != 1 {
		t.Fatalf("machine track: %+v %v", page, err)
	}
	checkOutput(t, "agent_machine_get", page)
	var files, lines int
	var early, complete bool
	var seconds int64
	if err := s.db.QueryRow(`SELECT files_changed,lines_changed,worker_complete,run_seconds,early_quit FROM worker_runs WHERE session_id='worker'`).Scan(&files, &lines, &complete, &seconds, &early); err != nil || files != 1 || lines != 6 || complete || seconds != 79 || !early {
		t.Fatalf("stored %d %d complete=%t seconds=%d early=%t %v", files, lines, complete, seconds, early, err)
	}
}

func TestMigrationWorkerEarlyQuitReplay(t *testing.T) {
	s, _ := newTestStore(t)
	version := migrationVersion(t, "worker early quit evidence")
	t.Logf("early quit migration: %d", version)
	if version != 127 {
		t.Fatalf("migration %d, want 127 on this base", version)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, version); err != nil {
		t.Fatal(err)
	}
	if err := migrateOldFixture(t, s); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('worker_runs') WHERE name IN ('files_changed','lines_changed','worker_complete','early_quit')`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("columns %d %v", n, err)
	}
}
