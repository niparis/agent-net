package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestOpenCodeTimelineDoesNotBlockPresence(t *testing.T) {
	f := startCTOCompactionFixture(t)
	d := f.n.d
	d.cto.mu.Lock()
	originalBase, originalIdentity := d.cto.base, d.cto.identity
	d.cto.mu.Unlock()
	target, err := url.Parse(originalBase)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	entered := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/message") && r.Method == http.MethodGet {
			entered <- struct{}{}
			<-r.Context().Done()
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	defer server.Close()
	d.cto.mu.Lock()
	d.cto.base, d.cto.identity = server.URL, "test-proxy"
	d.cto.mu.Unlock()
	defer func() {
		d.cto.mu.Lock()
		d.cto.base, d.cto.identity = originalBase, originalIdentity
		d.cto.mu.Unlock()
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); _, _ = d.openCodeCTOEvents(ctx, 0) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timeline did not reach message read")
	}
	presence := make(chan struct{})
	go func() { d.ctoRunning(); close(presence) }()
	select {
	case <-presence:
	case <-time.After(1500 * time.Millisecond):
		cancel()
		<-done
		t.Fatal("presence waited for the timeline HTTP read")
	}
	cancel()
	<-done
}

func waitWithin(t *testing.T, label string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(1500 * time.Millisecond):
		t.Fatalf("%s blocked for more than 1500ms", label)
	}
}

func openFIFOWriter(t *testing.T, path string) (entered <-chan struct{}, closeWriter func()) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	ch := make(chan struct{})
	wCh := make(chan *os.File, 1)
	go func() {
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		wCh <- f
		close(ch)
	}()
	return ch, func() {
		select {
		case f := <-wCh:
			_ = f.Close()
		default:
			// Unblock O_WRONLY if no reader ever arrived.
			if r, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0); err == nil {
				_ = r.Close()
			}
			select {
			case f := <-wCh:
				_ = f.Close()
			case <-time.After(time.Second):
			}
		}
	}
}

func TestOpenCodeSlowStartDoesNotBlockPresenceTimelineOrStop(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startClaudeNode(t, g, shortSet)
	d := n.d
	if err := d.setCTOConversation("opencode", "ses_slow"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	release := filepath.Join(dir, "release")
	bin := filepath.Join(dir, "opencode")
	script := "#!/bin/sh\n" +
		"touch " + started + "\n" +
		"while [ ! -f " + release + " ]; do sleep 0.05; done\n" +
		"exit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	d.cfg.HarnessPaths["opencode"] = bin

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = d.openCodeCTOEvents(ctx, 0)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slow opencode start did not launch")
		}
		time.Sleep(20 * time.Millisecond)
	}

	waitWithin(t, "presence during slow opencode start", func() { d.ctoRunning() })
	waitWithin(t, "claude timeline during slow opencode start", func() { _, _ = (claudeCTOHarness{}).events(ctx, d, 0) })
	waitWithin(t, "owner stop during slow opencode start", func() { _, _ = d.stopCTOSession(ctx) })

	_ = os.WriteFile(release, nil, 0600)
	cancel()
	<-done

	d.cto.mu.Lock()
	cmd := d.cto.cmd
	base := d.cto.base
	d.cto.mu.Unlock()
	if cmd != nil || base != "" {
		t.Fatalf("stop during slow start left published server: cmd=%v base=%q", cmd != nil, base)
	}
}

func TestStopDuringSlowOpenCodeStartLeavesNoPublishedServer(t *testing.T) {
	withFakeOpenCodeRouteSets(t, testStopDuringSlowOpenCodeStartLeavesNoPublishedServer)
}

func testStopDuringSlowOpenCodeStartLeavesNoPublishedServer(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startClaudeNode(t, g, shortSet)
	d := n.d
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	release := filepath.Join(dir, "release")
	bin := filepath.Join(dir, "opencode")
	script := "#!/bin/sh\n" +
		"touch " + started + "\n" +
		"while [ ! -f " + release + " ]; do sleep 0.05; done\n" +
		"exec " + os.Args[0] + " \"$@\"\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	d.cfg.HarnessPaths["opencode"] = bin

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = d.startOpenCodeRole(ctx, roleCTO)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slow opencode start did not launch")
		}
		time.Sleep(20 * time.Millisecond)
	}

	waitWithin(t, "owner stop during slow opencode start", func() { _, _ = d.stopCTOSession(ctx) })
	_ = os.WriteFile(release, nil, 0600)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		cancel()
		<-done
		t.Fatal("startOpenCodeRole did not finish after release")
	}

	d.cto.mu.Lock()
	cmd := d.cto.cmd
	base := d.cto.base
	d.cto.mu.Unlock()
	t.Cleanup(func() {
		d.stopOpenCodeCTO("test")
	})
	if cmd != nil || base != "" {
		t.Fatalf("stop during slow start left published server: cmd=%v base=%q", cmd != nil, base)
	}
}

func TestClaudeJobFileBlockDoesNotBlockOtherHarnessOrStop(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startClaudeNode(t, g, shortSet)
	d := n.d
	child := exec.Command("sleep", "60")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})
	tok, alive := procToken(child.Process.Pid)
	if !alive {
		t.Fatal("procToken(child) not alive")
	}
	d.cto.mu.Lock()
	d.claudeCTO.ref = "job_block"
	d.claudeCTO.token = tok
	d.claudeCTO.setSession("ses_block")
	d.claudeCTO.root.Store(&claudeCTORoot{pid: child.Process.Pid, token: tok})
	d.cto.mu.Unlock()
	t.Cleanup(func() {
		d.cto.mu.Lock()
		d.releaseClaudeCTOLocked()
		d.cto.mu.Unlock()
	})

	statePath := filepath.Join(filepath.Dir(d.cfg.ClaudeSessionsDir), "jobs", "job_block", "state.json")
	entered, closeWriter := openFIFOWriter(t, statePath)
	defer closeWriter()

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.ctoRunning()
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("ctoRunning did not reach state.json read")
	}

	ctx := context.Background()
	waitWithin(t, "opencode timeline during claude job read", func() { _, _ = d.openCodeCTOEvents(ctx, 0) })
	waitWithin(t, "owner stop during claude job read", func() { _, _ = d.stopCTOSession(ctx) })

	closeWriter()
	<-done
}

func TestClaudeTranscriptReadDoesNotBlockPresenceOtherHarnessOrStop(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startClaudeNode(t, g, shortSet)
	d := n.d
	if err := d.setCTOConversation("claude", "ses_block"); err != nil {
		t.Fatal(err)
	}
	transcriptPath := filepath.Join(filepath.Dir(d.cfg.ClaudeSessionsDir), "projects", "proj1", "ses_block.jsonl")
	entered, closeWriter := openFIFOWriter(t, transcriptPath)
	defer closeWriter()

	ctx := context.Background()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = (claudeCTOHarness{}).events(ctx, d, 0)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("claude events did not reach transcript read")
	}

	waitWithin(t, "presence during claude transcript read", func() { d.ctoRunning() })
	waitWithin(t, "opencode timeline during claude transcript read", func() { _, _ = d.openCodeCTOEvents(ctx, 0) })
	waitWithin(t, "owner stop during claude transcript read", func() { _, _ = d.stopCTOSession(ctx) })

	closeWriter()
	<-done
}
