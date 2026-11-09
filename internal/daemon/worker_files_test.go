package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestWorkerHandbackFilesAcrossNodes(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	a := startWorkerNode(t, g, shortSet, func(c *Config) { c.Files.Home = t.TempDir() })
	remote := &fakeAdapter{harness: "fake"}
	b := startNode(t, g, nodeOpts{adapter: remote, cfg: func(c *Config) {
		c.Files.Home = t.TempDir()
		c.Files.Poll, c.Files.PollMax = 20*time.Millisecond, 50*time.Millisecond
	}})
	remote.setSessions(protocol.SessionInfo{Harness: "fake", NativeID: "caller", PID: 1, Cwd: t.TempDir(), Status: "idle"})
	var caller string
	eventually(t, "remote caller", func() bool { caller = remote.host.SessionID("fake", "caller"); return caller != "" })
	repo := newRepo(t, "shop")
	a.register(t, repo)
	ctx, cancel := context.WithTimeout(context.Background(), scaled(30*time.Second))
	defer cancel()
	res, err := b.d.workerStartCall(ctx, caller, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "FAKE:ignore wait", Isolation: protocol.IsolationWorktree}, false, "")
	if err != nil {
		t.Fatalf("remote worker start: %v", err)
	}
	eventually(t, "remote worker ready", func() bool {
		w := a.d.workerBySession(res.SessionID)
		if w == nil {
			return false
		}
		a.d.wmu.Lock()
		defer a.d.wmu.Unlock()
		return w.owner == caller && w.taskMsg != "" && w.startDone != nil && isClosed(w.startDone)
	})
	inside := writeTestFile(t, filepath.Join(res.Worktree, "result.txt"), []byte("worker result\n"))
	// Ordinary Go code with a key= in its SQL is sent, not refused as a secret.
	code := writeTestFile(t, filepath.Join(res.Worktree, "lead_route.go"), []byte("package gateway\n\n"+leadRouteLine+"\n"))
	outside := writeTestFile(t, filepath.Join(t.TempDir(), "outside.txt"), []byte("outside\n"))
	link := filepath.Join(res.Worktree, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	secret := writeTestFile(t, filepath.Join(res.Worktree, "secret.txt"), []byte("OPENAI=sk-"+strings.Repeat("a1", 12)+"\n"))
	for _, p := range []string{outside, link, secret} {
		if _, err := a.d.handback(ctx, res.SessionID, protocol.WorkerHandbackRequest{Report: "done", Files: []string{p}}); err == nil {
			t.Errorf("handback accepted outside path %s", p)
		}
	}
	out, err := a.d.handback(ctx, res.SessionID, protocol.WorkerHandbackRequest{Report: "the report", Status: protocol.StatusBlocked, Files: []string{inside, code}})
	if err != nil {
		t.Fatalf("handback: %v", err)
	}
	if out.Sent == nil || len(out.Sent.Files) != 2 {
		t.Fatalf("handback result: %+v", out)
	}
	msgID := out.Sent.ID
	dest := filepath.Join(b.cfg.StateDir, "inbox", caller, msgID, "result.txt")
	codeDest := filepath.Join(b.cfg.StateDir, "inbox", caller, msgID, "lead_route.go")
	eventually(t, "caller receives notice with file", func() bool {
		for _, m := range remote.delivered() {
			if m.ID == msgID && strings.Contains(m.Body, "the report") && strings.Contains(m.Body, filepath.Dir(dest)) {
				return true
			}
		}
		return false
	})
	if got := string(mustReadFile(t, dest)); got != "worker result\n" {
		t.Fatalf("file contents = %q", got)
	}
	if got := string(mustReadFile(t, codeDest)); got != "package gateway\n\n"+leadRouteLine+"\n" {
		t.Fatalf("code file contents = %q", got)
	}
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestWorkerHandbackFilesRequireWorktree(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	a := startWorkerNode(t, g, shortSet, func(c *Config) { c.Files.Home = t.TempDir() })
	repo := newRepo(t, "shop")
	a.register(t, repo)
	res := a.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "FAKE:ignore wait", Isolation: protocol.IsolationNone})
	file := filepath.Join(repo, "README.md")
	_, err := a.d.handback(context.Background(), res.SessionID, protocol.WorkerHandbackRequest{Report: "done", Files: []string{file}})
	if err == nil || !strings.Contains(err.Error(), "worktree") {
		t.Fatalf("handback without worktree: %v", err)
	}
}

func TestWorkerNoticeMissingFilesDeliversReport(t *testing.T) {
	n := startFilesNet(t, filesNetOpts{layout: "darwin", wait: 200 * time.Millisecond})
	paths := []string{
		writeTestFile(t, filepath.Join(n.project, "arrived.txt"), []byte("present")),
		writeTestFile(t, filepath.Join(n.project, "missing.txt"), []byte("missing")),
	}
	out, err := PrepareFiles(paths, FileRules{Roots: []string{n.project}, Home: n.senderHome}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer out.Remove()
	m := protocol.Message{ID: "m_timeout", From: n.alice, To: n.bob, Type: protocol.TypeReply,
		FromWorker: true, Body: "[agent-net worker notice]\nstatus: done\nreport text", Files: out.Manifest(),
		CreatedAt: time.Now().Add(-3 * time.Minute), ExpiresAt: time.Now().Add(time.Hour)}
	dir := n.b.d.fileInboxDir(m.To, m.ID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "arrived.txt"), []byte("present"), 0600); err != nil {
		t.Fatal(err)
	}
	n.b.d.collectThenDeliver(context.Background(), m)
	eventually(t, "worker notice delivered with missing files", func() bool {
		for _, got := range n.fb.delivered() {
			if got.ID == m.ID {
				if !strings.Contains(got.Body, "files missing: missing.txt, transfer did not finish") || !strings.Contains(got.Body, "report text") || !strings.Contains(got.Body, "arrived.txt") {
					t.Errorf("incomplete notice: %s", got.Body)
				}
				return true
			}
		}
		return false
	})
}

func TestWorkerNoticeNotPushedInStatus(t *testing.T) {
	n, f := startDeliveryNode(t)
	f.answer("msg.send", func(raw json.RawMessage) (any, error) {
		return map[string]any{"message": protocol.Message{ID: "m_notice"}}, nil
	})
	repo, wt := deliveryWorktree(t)
	if err := os.WriteFile(filepath.Join(wt, "uncommitted.txt"), []byte("work"), 0600); err != nil {
		t.Fatal(err)
	}
	f.answer(protocol.FrameDeliveryNeedsPush, func(json.RawMessage) (any, error) {
		return protocol.DeliveryNeedsPushResult{NeedsPush: true}, nil
	})
	f.answer(protocol.FrameChangeSubmit, func(json.RawMessage) (any, error) {
		return nil, errors.New("record unavailable")
	})
	w := &worker{id: "w_dirty", sessionID: "s_worker", owner: "s_owner", worktree: wt, branch: "feat/x", base: gitHead(t, repo, "main"), project: "demo"}
	_, problem := n.d.recordHandbackChange(context.Background(), w)
	if !strings.Contains(problem, handbackUncommittedPushProblem) || !strings.Contains(problem, "record unavailable") {
		t.Fatalf("record problem does not preserve both errors: %q", problem)
	}
	_, err := n.d.sendNoticeToKey(context.Background(), w, protocol.WorkerNotice{Status: protocol.StatusDone, Worktree: "/worktree", DeliveryError: problem}, "report", "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	frames := f.frames("msg.send")
	if len(frames) != 1 {
		t.Fatalf("messages: %d", len(frames))
	}
	var msg protocol.MsgSend
	if err := json.Unmarshal(frames[0], &msg); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(msg.Body, "[agent-net worker notice]\nstatus: done, NOT PUSHED: uncommitted changes in the worktree\n") {
		t.Errorf("status line: %s", msg.Body)
	}
}

func TestWorkerNoticeFileDeadline(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	m := protocol.Message{FromWorker: true, Body: "[agent-net worker notice]\nstatus: done\n", CreatedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}
	if got, want := fileWaitDeadline(m, FileConfig{}, now), now.Add(time.Minute); !got.Equal(want) {
		t.Fatalf("deadline = %s, want %s", got, want)
	}
	m.CreatedAt = now.Add(-3 * time.Minute)
	if got := fileWaitDeadline(m, FileConfig{}, now); !got.Before(now) {
		t.Fatalf("replayed notice should already have timed out: %s", got)
	}
	m.FromWorker = false
	m.FromHarness, m.FromName = "fake", "peer"
	if got := fileWaitDeadline(m, FileConfig{}, now); !got.Equal(m.ExpiresAt) {
		t.Fatalf("ordinary message deadline = %s, want expiry %s", got, m.ExpiresAt)
	}
	m.FromWorker = true
	m.CreatedAt = now
	if got, want := fileWaitDeadline(m, FileConfig{Wait: time.Second}, now), now.Add(time.Second); !got.Equal(want) {
		t.Fatalf("configured shorter wait = %s, want %s", got, want)
	}
}

func TestLocalFileOfferCanBeRetried(t *testing.T) {
	root := t.TempDir()
	file := writeTestFile(t, filepath.Join(root, "file.txt"), []byte("file data"))
	out, err := PrepareFiles([]string{file}, FileRules{Roots: []string{root}, Home: t.TempDir()}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer out.Remove()
	d := &Daemon{cfg: Config{StateDir: t.TempDir()}}
	for i := 0; i < 2; i++ {
		if _, err := d.pushFile(context.Background(), "m_test", out.Files[0], FilePathLocal, "", "", ""); err != nil {
			t.Fatalf("offer %d: %v", i+1, err)
		}
	}
	if err := os.Remove(filepath.Join(d.cfg.StateDir, fileLocalFolder, protocol.TaildropName("m_test", out.Files[0].Entry))); err != nil {
		t.Fatal(err)
	}
	if _, err := d.pushFile(context.Background(), "m_test", out.Files[0], FilePathLocal, "", "", ""); err != nil {
		t.Fatalf("offer after collection: %v", err)
	}
}

func TestWorkerNoticeMissingFilesGatewayDelivery(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	a := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.testGatewayCall = func(_ context.Context, typ string, _ any) (json.RawMessage, bool, error) {
			if typ == "xfer.begin" {
				return nil, true, errors.New("test transfer unavailable")
			}
			return nil, false, nil
		}
	})
	remote := &fakeAdapter{harness: "fake"}
	b := startNode(t, g, nodeOpts{adapter: remote, cfg: func(c *Config) {
		c.Files.Wait = 200 * time.Millisecond
		c.Files.Poll, c.Files.PollMax = 20*time.Millisecond, 50*time.Millisecond
	}})
	remote.setSessions(protocol.SessionInfo{Harness: "fake", NativeID: "caller", PID: 1, Cwd: t.TempDir(), Status: "idle"})
	var caller string
	eventually(t, "remote caller", func() bool { caller = remote.host.SessionID("fake", "caller"); return caller != "" })
	a.register(t, newRepo(t, "shop"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res, err := b.d.workerStartCall(ctx, caller, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "FAKE:ignore wait", Isolation: protocol.IsolationWorktree}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "worker ready", func() bool {
		w := a.d.workerBySession(res.SessionID)
		if w == nil {
			return false
		}
		a.d.wmu.Lock()
		defer a.d.wmu.Unlock()
		return w.taskMsg != "" && w.startDone != nil && isClosed(w.startDone)
	})
	file := writeTestFile(t, filepath.Join(res.Worktree, "missing.txt"), []byte("file data"))
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = a.d.handback(ctx, res.SessionID, protocol.WorkerHandbackRequest{Report: "the report", Status: protocol.StatusBlocked, Files: []string{file}})
	}()
	var notice protocol.Message
	eventually(t, "worker report without missing file", func() bool {
		for _, m := range remote.delivered() {
			if m.From == res.SessionID && strings.Contains(m.Body, "the report") {
				notice = m
				return true
			}
		}
		return false
	})
	if !strings.Contains(notice.Body, "files missing: missing.txt, transfer did not finish") {
		t.Fatalf("notice: %s", notice.Body)
	}
	eventually(t, "gateway records notice delivery", func() bool {
		m, ok, err := g.gw.Store().Message(notice.ID)
		return err == nil && ok && m.State == protocol.StateDelivered
	})
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handback did not stop after cancellation")
	}
}

func TestOrphanedWorkerNoticeFileDeadline(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	// The gateway deletes old worker sessions while keeping their messages.
	// On replay, the left join no longer marks the sender as a worker.
	m := protocol.Message{Body: "[agent-net worker notice]\nstatus: done\nreport", CreatedAt: now.Add(-3 * time.Minute), ExpiresAt: now.Add(time.Hour)}
	if got := fileWaitDeadline(m, FileConfig{}, now); !got.Before(now) {
		t.Fatalf("orphaned worker notice deadline = %s, should have expired", got)
	}
}
