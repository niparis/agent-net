package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestWorkerEarlyQuitEvidence(t *testing.T) {
	for _, tc := range []struct {
		name             string
		lines            int
		commit, complete bool
		age              time.Duration
		early            bool
	}{
		{"empty", 0, false, false, 79 * time.Second, true},
		{"tiny", 6, false, false, 79 * time.Second, true},
		{"real", 20, false, false, 79 * time.Second, false},
		{"committed", 6, true, false, 79 * time.Second, false},
		{"long", 0, false, false, 121 * time.Second, false},
		{"complete", 0, false, true, 79 * time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := commitTestDaemon(t)
			w, dir := commitTestWorker(t, d)
			w.startedAt = time.Now().Add(-tc.age)
			if tc.lines > 0 {
				if err := os.WriteFile(filepath.Join(dir, "new\tfile\nname"), []byte(strings.Repeat("line\n", tc.lines)), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.commit {
				gitWithEnv(t, dir, nil, "add", ".")
				gitWithEnv(t, dir, nil, "commit", "-m", "work")
			}
			e := d.measureWorkerEvidence(context.Background(), w, &tc.complete)
			if e == nil || e.EarlyQuit != tc.early || e.LinesChanged != tc.lines {
				t.Fatalf("evidence: %+v", e)
			}
			wantFiles := 0
			if tc.lines > 0 {
				wantFiles = 1
			}
			if e.FilesChanged != wantFiles {
				t.Fatalf("files: %+v", e)
			}
			n := protocol.WorkerNotice{Evidence: e}
			out := protocol.FormatWorkerNotice(n, "")
			if strings.Contains(out, "early quit:") != tc.early {
				t.Fatalf("notice: %s", out)
			}
		})
	}
}

func TestWorkerEarlyQuitHandbackAndExit(t *testing.T) {
	for _, task := range []string{"FAKE:handback FAKE:partial", "FAKE:ignore"} {
		t.Run(task, func(t *testing.T) {
			g := startGateway(t, t.TempDir(), "")
			set := shortSet
			set.StartGap = -1
			w := startWorkerNode(t, g, set, nil)
			repo := newRepo(t, "shop")
			w.register(t, repo)
			res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: task, Isolation: protocol.IsolationWorktree, AutoContinue: autoContinueOff()})
			if task == "FAKE:ignore" {
				w.d.endWorker(w.d.workerBySession(res.SessionID), protocol.StopExited)
			}
			notice := w.waitNotice(t, 1)[0]
			prefix := "early quit: 1 file, 1 lines after "
			if task == "FAKE:ignore" {
				prefix = "early quit: 0 files, 0 lines after "
			}
			if !strings.Contains(notice.Body, prefix) {
				t.Fatalf("notice: %s", notice.Body)
			}
			eventually(t, "early quit reaches gateway", func() bool {
				tr, ok, err := g.gw.Store().TrackRecord(w.d.NodeID(), "opencode", "fake/m1", 0)
				return err == nil && ok && tr.EarlyQuits == 1
			})
			rows, err := w.d.st.workers()
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rows {
				if r.SessionID == res.SessionID && (r.Evidence == nil || !r.Evidence.EarlyQuit) {
					t.Fatalf("persisted evidence: %+v", r.Evidence)
				}
			}
		})
	}
}

func TestWorkerEarlyQuitLostEvidence(t *testing.T) {
	d := commitTestDaemon(t)
	w, dir := commitTestWorker(t, d)
	st, err := openState(d.cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	d.st = st
	t.Cleanup(func() { st.db.Close() })
	w.startedAt = time.Now().Add(-79 * time.Second)
	if err := os.WriteFile(filepath.Join(dir, "unfinished"), []byte("one\ntwo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := st.saveWorker(d.rowLocked(w, "")); err != nil {
		t.Fatal(err)
	}
	d.adoptWorkers()
	row, ok, err := st.workerBySession(w.sessionID)
	if err != nil || !ok || row.Evidence == nil || row.Evidence.FilesChanged != 1 || row.Evidence.LinesChanged != 2 || !row.Evidence.EarlyQuit {
		t.Fatalf("lost evidence: %+v %v", row.Evidence, err)
	}
}
