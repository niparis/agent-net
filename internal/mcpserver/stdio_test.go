package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	initReq  = `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	initDone = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	listReq  = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
)

// waitMax is how long a test waits for something that must happen. Every wait
// returns as soon as the event comes, so this is a failure ceiling and not a
// sleep. It stays at 2 s.
const waitMax = 2 * time.Second

// rig is an MCP server on a stdioConn with pipes for stdin and stdout.
type rig struct {
	t      *testing.T
	conn   *stdioConn
	stdin  *os.File // the test writes the client's bytes here
	lines  chan string
	execs  chan handover
	cancel context.CancelFunc
	done   chan error
}

func pipe(t *testing.T) (r, w *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	return r, w
}

func newRig(t *testing.T, h *handover, call Caller) *rig {
	t.Helper()
	inR, inW := pipe(t)
	outR, outW := pipe(t)
	rg := &rig{t: t, stdin: inW, lines: make(chan string, 100), execs: make(chan handover, 4), done: make(chan error, 1)}
	rg.conn = newStdioConn(int(inR.Fd()), outW, h, func(string, ...any) {})
	rg.conn.execFn = func(h handover, path string) error {
		rg.execs <- h
		return errors.New("stub: not really exec'ing")
	}
	go rg.conn.readLoop()
	// readLoop polls its fd and only ends at end of input. Closing the fd under
	// it does not wake the poll, and the next test's pipe may get the same fd
	// number, so a leftover loop would steal that test's input. (This was the
	// cause of a rare "timed out waiting for a line".) So end the input first
	// and wait for the loop to see it. This cleanup runs before the pipes close.
	t.Cleanup(func() {
		inW.Close()
		deadline := time.Now().Add(waitMax)
		for time.Now().Before(deadline) {
			rg.conn.mu.Lock()
			eof := rg.conn.eof
			rg.conn.mu.Unlock()
			if eof {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Error("the input loop did not end")
	})
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			rg.lines <- sc.Text()
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	rg.cancel = cancel
	t.Cleanup(cancel)
	srv := New(call, "")
	go func() { rg.done <- srv.Run(ctx, stdioTransport{rg.conn}) }()
	return rg
}

func (r *rig) send(s string) {
	r.t.Helper()
	if _, err := r.stdin.WriteString(s); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) next() string {
	r.t.Helper()
	select {
	case l := <-r.lines:
		return l
	case <-time.After(waitMax):
		r.t.Fatal("timed out waiting for a line from the server")
		return ""
	}
}

func (r *rig) noLine() {
	r.t.Helper()
	select {
	case l := <-r.lines:
		r.t.Fatalf("unexpected line: %s", l)
	case <-time.After(150 * time.Millisecond):
	}
}

func okCaller(context.Context, string, any) (json.RawMessage, error) {
	return json.RawMessage(`{"ok":true}`), nil
}

func TestFramingSplitWritesAndBlankLines(t *testing.T) {
	r := newRig(t, nil, okCaller)
	// The request arrives in three pieces, with CRLF and blank lines around it.
	r.send("\r\n\n" + initReq[:20])
	r.noLine()
	r.send(initReq[20:] + "\r\n")
	if l := r.next(); !strings.Contains(l, `"id":0`) || !strings.Contains(l, "protocolVersion") {
		t.Fatalf("initialize reply: %s", l)
	}
	// Two messages in one write are both handled.
	r.send(initDone + "\n" + listReq + "\n")
	if l := r.next(); !strings.Contains(l, "agent_list") {
		t.Fatalf("tools/list reply: %s", l)
	}
}

func TestExecWaitsForIdleAndHandsOverUnreadBytes(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	call := func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		if method == "list" {
			started <- struct{}{}
			<-release
		}
		return json.RawMessage(`{"ok":true}`), nil
	}
	r := newRig(t, nil, call)
	r.send(initReq + "\n" + initDone + "\n")
	r.next() // initialize reply

	// A tool call is in flight when the exec is requested: nothing may happen.
	r.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"agent_list","arguments":{}}}` + "\n")
	<-started
	r.conn.requestExec("/new/binary")
	// More input arrives while the call runs: one whole request and half of another.
	r.send(listReq + "\n" + `{"jsonrpc":"2.0","id":3,"meth`)
	select {
	case <-r.execs:
		t.Fatal("exec while a request was in flight")
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	// tools/list (already in the buffer) and the tool call both get answered.
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		l := r.next()
		if strings.Contains(l, `"id":2`) {
			seen["call"] = true
		} else if strings.Contains(l, `"id":1`) && strings.Contains(l, "agent_list") {
			seen["list"] = true
		} else {
			t.Fatalf("unexpected line: %s", l)
		}
	}
	if !seen["call"] || !seen["list"] {
		t.Fatalf("answers: %v", seen)
	}
	var h handover
	select {
	case h = <-r.execs:
	case <-time.After(waitMax):
		t.Fatal("no exec once idle")
	}
	if string(h.Leftover) != `{"jsonrpc":"2.0","id":3,"meth` {
		t.Fatalf("leftover = %q", h.Leftover)
	}
	if string(h.Init) != initReq || string(h.Initialized) != initDone {
		t.Fatalf("handshake lines not kept: %q %q", h.Init, h.Initialized)
	}
	// The stub exec "failed", so this process carries on: finish the half line.
	r.send(`od":"ping"}` + "\n")
	if l := r.next(); !strings.Contains(l, `"id":3`) {
		t.Fatalf("ping reply after a failed exec: %s", l)
	}
}

func TestReplayIntoNewProcess(t *testing.T) {
	h := &handover{V: 1, Init: []byte(initReq), Initialized: []byte(initDone),
		Leftover: []byte(listReq + "\n" + `{"jsonrpc":"2.0","id":9,"method":"pi`)}
	r := newRig(t, h, okCaller)
	// The client never sees an initialize reply. It sees the tool list change,
	// then the answer to the request that was left over.
	if l := r.next(); !strings.Contains(l, "notifications/tools/list_changed") {
		t.Fatalf("first line should be list_changed, got %s", l)
	}
	// list_changed is sent once the replay is processed; the leftover list may
	// be answered before or after it, but never an initialize result.
	r.send(`ng"}` + "\n")
	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		l := r.next()
		switch {
		case strings.Contains(l, `"id":1`) && strings.Contains(l, "agent_list"):
			got["list"] = true
		case strings.Contains(l, `"id":9`):
			got["ping"] = true
		default:
			t.Fatalf("unexpected line: %s", l)
		}
	}
	if !got["list"] || !got["ping"] {
		t.Fatalf("missing answers: %v", got)
	}
	r.noLine()
}

func TestReplayWithoutInitializedDoesNotAnnounce(t *testing.T) {
	r := newRig(t, &handover{V: 1, Init: []byte(initReq)}, okCaller)
	r.noLine() // the initialize reply is swallowed and no list_changed is sent
	r.send(initDone + "\n" + listReq + "\n")
	if l := r.next(); !strings.Contains(l, "agent_list") {
		t.Fatalf("tools/list reply: %s", l)
	}
}

func TestNoExecBeforeInitializeDone(t *testing.T) {
	// A fresh connection that has not seen anything is idle: exec is allowed and
	// hands over an empty handshake, which the new process treats as "none".
	r := newRig(t, nil, okCaller)
	time.Sleep(50 * time.Millisecond)
	r.conn.requestExec("/new/binary")
	select {
	case h := <-r.execs:
		if len(h.Init) != 0 || len(h.Initialized) != 0 {
			t.Fatalf("unexpected handshake: %+v", h)
		}
	case <-time.After(waitMax):
		t.Fatal("no exec on an idle connection")
	}
}

func TestHandoverFileRoundTrip(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	p, err := writeHandover(handover{V: 1, Init: []byte("a"), Leftover: []byte("b\n")})
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatalf("handover file mode %v", fi.Mode())
	}
	t.Setenv(HandoverEnv, p)
	h := takeHandover(func(string, ...any) {})
	if h == nil || string(h.Init) != "a" || string(h.Leftover) != "b\n" {
		t.Fatalf("got %+v", h)
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatal("handover file not removed")
	}
	if os.Getenv(HandoverEnv) != "" {
		t.Fatal("env not cleared")
	}
	if takeHandover(func(string, ...any) {}) != nil {
		t.Fatal("second take should find nothing")
	}
}

func TestExePath(t *testing.T) {
	if got := ExePath("/home/u/.local/bin/agent-net (deleted)"); got != "/home/u/.local/bin/agent-net" {
		t.Fatal(got)
	}
	if got := ExePath("/bin/agent-net"); got != "/bin/agent-net" {
		t.Fatal(got)
	}
}

func TestSwitcherDecisions(t *testing.T) {
	cases := []struct {
		name          string
		mine, daemon  string
		noAuto        bool
		wantAsk       bool
		fakeDiskIsOld bool
	}{
		{"newer", "v0.3.1", "v0.3.2", false, true, false},
		{"same", "v0.3.2", "v0.3.2", false, false, false},
		{"daemon older", "v0.3.2", "v0.3.1", false, false, false},
		{"dev build", "dev", "v0.3.2", false, false, false},
		{"daemon dev", "v0.3.1", "dev", false, false, false},
		{"old daemon no version", "v0.3.1", "", false, false, false},
		{"switched off", "v0.3.1", "v0.3.2", true, false, false},
		{"disk is not the daemon's version", "v0.3.1", "v0.3.2", false, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			disk := c.daemon
			if c.fakeDiskIsOld {
				disk = c.mine
			}
			exe := dir + "/agent-net"
			os.WriteFile(exe, []byte("#!/bin/sh\necho "+disk+"\n"), 0o755)
			r := newRig(t, nil, okCaller)
			sw := &switcher{opts: Options{Version: c.mine, Exe: exe + " (deleted)", NoAutoUpgrade: c.noAuto},
				conn: r.conn, logf: func(string, ...any) {}}
			time.Sleep(30 * time.Millisecond)
			sw.consider(context.Background(), c.daemon)
			select {
			case <-r.execs:
				if !c.wantAsk {
					t.Fatal("exec was requested")
				}
			case <-time.After(300 * time.Millisecond):
				if c.wantAsk {
					t.Fatal("exec was not requested")
				}
			}
		})
	}
}
