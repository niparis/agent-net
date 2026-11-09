package daemon

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// setGatewayNodeOS changes the OS the gateway knows for a machine, so a test
// can make a receiver look like a Mac.
func setGatewayNodeOS(t *testing.T, g *testGateway, nodeID, name string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(g.dir, "gateway.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE nodes SET os = ? WHERE id = ?`, name, nodeID); err != nil {
		t.Fatal(err)
	}
}

// A file open can block in the kernel forever: a macOS privacy check or an
// iCloud-backed file holds the syscall, which ignores the context. The
// collector must still deliver at its deadline, and a daemon stop must not
// wait for the helper that is stuck in the open.
func TestBlockedFileOpenDoesNotBlockDeliveryOrStop(t *testing.T) {
	n := startFilesNet(t, filesNetOpts{layout: "linux", tailnet: true, wait: 300 * time.Millisecond})
	started := make(chan struct{}, 8)
	release := make(chan struct{})
	oldOpen, oldLstat := takeFileOpen, takeFileLstat
	takeFileOpen = func(string) (*os.File, error) {
		started <- struct{}{}
		<-release
		return nil, os.ErrClosed
	}
	t.Cleanup(func() {
		takeFileOpen, takeFileLstat = oldOpen, oldLstat
		close(release)
	})

	waitStarted := func() {
		t.Helper()
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("the receiver never tried to open the file")
		}
	}
	waitFailed := func(id string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for n.message(id).State != protocol.StateFailed {
			if time.Now().After(deadline) {
				t.Fatal("delivery stayed blocked past the file wait deadline")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	src := writeTestFile(t, filepath.Join(n.project, "result.txt"), []byte("result"))
	res, err := n.send(src)
	if err != nil {
		t.Fatal(err)
	}
	waitStarted()
	waitFailed(res.Message.ID)

	// A second collector is blocked in the open when the daemon stops.
	if _, err := n.send(src); err != nil {
		t.Fatal(err)
	}
	waitStarted()
	stopped := make(chan struct{})
	go func() {
		n.b.d.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("daemon stop waited for the blocked file open")
	}
}

// On macOS the daemon must never stat or open a file in Downloads: that is
// where the blocked open came from. Files from senders that still use
// Taildrop stay there and the message goes without them.
func TestDarwinReceiverDoesNotStatOrOpenDownloads(t *testing.T) {
	n := startFilesNet(t, filesNetOpts{layout: "darwin", tailnet: true, wait: 300 * time.Millisecond})
	var touched atomic.Bool
	oldOpen, oldLstat := takeFileOpen, takeFileLstat
	takeFileOpen = func(name string) (*os.File, error) {
		if within(name, n.downloads) {
			touched.Store(true)
		}
		return nil, os.ErrClosed
	}
	takeFileLstat = func(name string) (os.FileInfo, error) {
		if within(name, n.downloads) {
			touched.Store(true)
		}
		return nil, os.ErrNotExist
	}
	t.Cleanup(func() { takeFileOpen, takeFileLstat = oldOpen, oldLstat })

	src := writeTestFile(t, filepath.Join(n.project, "result.txt"), []byte("result"))
	res, err := n.send(src)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for n.message(res.Message.ID).State != protocol.StateFailed {
		if time.Now().After(deadline) {
			t.Fatal("the Mac receiver did not finish the wait")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if touched.Load() {
		t.Fatal("the Mac daemon stat'd or opened a file in Downloads")
	}
	assertExists(t, filepath.Join(n.downloads, protocol.TaildropName(res.Message.ID, res.Files[0].Entry)))
}

// A receiver whose daemon reports darwin gets every message's files through
// the gateway: a request, a reply and a worker notice. Taildrop is never
// tried, because the Mac daemon does not read Downloads any more.
func TestDarwinReceiverFileRoutesUseGateway(t *testing.T) {
	n := startFilesNet(t, filesNetOpts{layout: "darwin", tailnet: true})
	setGatewayNodeOS(t, n.g, n.b.cfg.NodeID, "darwin")
	src := writeTestFile(t, filepath.Join(n.project, "result.txt"), []byte("result"))
	cases := []struct {
		name string
		req  FileSend
	}{
		{"request", FileSend{Body: "a request", Type: protocol.TypeRequest}},
		{"reply", FileSend{Body: "a reply", Type: protocol.TypeReply}},
		{"worker notice", FileSend{Body: "[agent-net worker notice]\nstatus: done\nreport", Type: protocol.TypeReply, WorkerNotice: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req
			req.From, req.To, req.Paths = n.alice, n.bob, []string{src}
			ctx, cancel := context.WithTimeout(context.Background(), scaled(20*time.Second))
			defer cancel()
			res, err := n.a.d.SendFiles(ctx, req)
			if err != nil {
				t.Fatalf("SendFiles: %v", err)
			}
			if len(res.Files) != 1 || res.Files[0].Path != FilePathGateway {
				t.Fatalf("file route = %+v, want the gateway", res.Files)
			}
			n.waitDelivered(res.Message.ID)
			if got := string(mustReadFile(t, filepath.Join(n.inbox(res.Message.ID), "result.txt"))); got != "result" {
				t.Fatalf("file = %q", got)
			}
		})
	}
	if strings.Contains(n.calls(), "file cp") {
		t.Fatal("a file went over Taildrop to a Mac receiver")
	}
}
