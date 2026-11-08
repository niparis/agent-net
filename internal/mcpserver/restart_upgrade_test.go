package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

func TestRestartNotificationRequiresClientSupport(t *testing.T) {
	cases := []struct {
		name         string
		capabilities string
		want         bool
	}{
		{"absent", `{}`, false},
		{"false", `{"tools":{"listChanged":false}}`, false},
		{"true", `{"tools":{"listChanged":true}}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			init := strings.Replace(initReq, `"capabilities":{}`, `"capabilities":`+tc.capabilities, 1)
			r := newRig(t, &handover{V: 1, Init: []byte(init), Initialized: []byte(initDone)}, okCaller)
			if tc.want {
				if l := r.next(); !strings.Contains(l, `"method":"notifications/tools/list_changed"`) {
					t.Fatalf("expected list_changed, got %s", l)
				}
			} else {
				r.noLine()
			}
			r.send(listReq + "\n")
			if l := r.next(); !strings.Contains(l, `"id":1`) || !strings.Contains(l, "agent_sessions_find") {
				t.Fatalf("tools/list after restart: %s", l)
			}
		})
	}
}

func TestRestartNotificationRequiresListenSubscription(t *testing.T) {
	listen := strings.Replace(listen0, `"toolsListChanged":true`, `"toolsListChanged":false,"resourcesListChanged":true`, 1)
	r := newRig(t, &handover{V: 1, Listens: [][]byte{[]byte(listen)}}, okCaller)
	r.noLine()
	r.send(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{` + metaNew + `}}` + "\n")
	if l := r.next(); !strings.Contains(l, `"id":1`) || !strings.Contains(l, "agent_sessions_find") {
		t.Fatalf("tools/list after restart: %s", l)
	}
}

func TestChangedExecutableWaitsUntilIdle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-net")
	writeVersion := func(v string) {
		t.Helper()
		next := path + ".new"
		if err := os.WriteFile(next, []byte("#!/bin/sh\necho "+v+"\n"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(next, path); err != nil {
			t.Fatal(err)
		}
	}
	writeVersion("v1.0.0")
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	call := func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		if method == "sessions_find" {
			started <- struct{}{}
			<-release
		}
		return json.RawMessage(`{"ok":true}`), nil
	}
	r := newRig(t, nil, call)
	sw := newSwitcher(Options{Version: "v1.0.0", Exe: path}, r.conn, func(string, ...any) {})
	checks := 0
	changed := sw.changed
	sw.changed = func() bool { checks++; return changed() }
	r.send(initReq + "\n" + initDone + "\n")
	r.next()
	r.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"agent_sessions_find","arguments":{}}}` + "\n")
	<-started
	writeVersion("v1.0.1")
	sw.checkExecutable(context.Background())
	if checks != 0 {
		t.Fatal("checked the executable while a request was in flight")
	}
	select {
	case <-r.execs:
		t.Fatal("exec while a request is in flight")
	default:
	}
	close(release)
	if l := r.next(); !strings.Contains(l, `"id":2`) {
		t.Fatalf("tool response: %s", l)
	}
	deadline := time.Now().Add(waitMax)
	for {
		r.conn.mu.Lock()
		idle := r.conn.idleLocked()
		r.conn.mu.Unlock()
		if idle {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server never went idle")
		}
		time.Sleep(time.Millisecond)
	}
	sw.checkExecutable(context.Background())
	select {
	case h := <-r.execs:
		if string(h.Init) != initReq || string(h.Initialized) != initDone {
			t.Fatalf("missing handshake: %+v", h)
		}
	case <-time.After(waitMax):
		t.Fatal("no exec after the request completed")
	}
	sw.checkExecutable(context.Background())
	select {
	case <-r.execs:
		t.Fatal("repeated exec after failure")
	default:
	}
}

func TestChangedExecutableRequiresNewerVersion(t *testing.T) {
	for _, disk := range []string{"v1.0.0", "v0.9.9", "dev"} {
		t.Run(disk, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "agent-net")
			if err := os.WriteFile(path, []byte("old"), 0755); err != nil {
				t.Fatal(err)
			}
			r := newRig(t, nil, okCaller)
			sw := newSwitcher(Options{Version: "v1.0.0", Exe: path}, r.conn, func(string, ...any) {})
			r.send(initReq + "\n" + initDone + "\n" + listReq + "\n")
			r.next()
			r.next()
			next := path + ".new"
			if err := os.WriteFile(next, []byte("#!/bin/sh\necho "+disk+"\n"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(next, path); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(waitMax)
			for {
				r.conn.mu.Lock()
				idle := r.conn.idleLocked()
				r.conn.mu.Unlock()
				if idle {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("server never went idle")
				}
				time.Sleep(time.Millisecond)
			}
			sw.checkExecutable(context.Background())
			select {
			case <-r.execs:
				t.Fatal("exec into same, older, or unorderable version")
			default:
			}
		})
	}
}

type heldWriter struct {
	started chan struct{}
	release chan struct{}
}

func (w *heldWriter) Write(p []byte) (int, error) {
	close(w.started)
	<-w.release
	return len(p), nil
}

func TestExecWaitsForOutgoingNotification(t *testing.T) {
	w := &heldWriter{started: make(chan struct{}), release: make(chan struct{})}
	c := newStdioConn(-1, w, nil, func(string, ...any) {})
	c.waiting = true
	execs := make(chan struct{}, 1)
	c.execFn = func(h handover, path string) error {
		execs <- struct{}{}
		return errors.New("fake exec failure")
	}
	msg, err := jsonrpc.DecodeMessage([]byte(listChangedLine))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- c.Write(context.Background(), msg) }()
	<-w.started
	c.requestExec("/new/binary")
	select {
	case <-execs:
		t.Fatal("exec while notification is being written")
	default:
	}
	close(w.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(waitMax):
		t.Fatal("write did not finish")
	}
	select {
	case <-execs:
	case <-time.After(waitMax):
		t.Fatal("no exec after write completed")
	}
	c.requestExec("/new/binary")
	select {
	case <-execs:
		t.Fatal("exec was retried after failure")
	default:
	}
}

func TestChangedExecutableRespectsAutoUpgradeOptOut(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-net")
	if err := os.WriteFile(path, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := newRig(t, nil, okCaller)
	sw := newSwitcher(Options{Version: "v1.0.0", Exe: path, NoAutoUpgrade: true}, r.conn, func(string, ...any) {})
	r.send(initReq + "\n" + initDone + "\n" + listReq + "\n")
	r.next()
	r.next()
	deadline := time.Now().Add(waitMax)
	for {
		r.conn.mu.Lock()
		idle := r.conn.idleLocked()
		r.conn.mu.Unlock()
		if idle {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server never went idle")
		}
		time.Sleep(time.Millisecond)
	}
	next := path + ".new"
	if err := os.WriteFile(next, []byte("#!/bin/sh\necho v1.0.1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, path); err != nil {
		t.Fatal(err)
	}
	sw.checkExecutable(context.Background())
	select {
	case <-r.execs:
		t.Fatal("exec despite auto-upgrade opt-out")
	default:
	}
}
