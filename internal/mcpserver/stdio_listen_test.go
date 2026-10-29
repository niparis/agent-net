package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// What Claude Code 2.1.312 sends when it speaks MCP 2026-07-28: no initialize,
// every request carries its protocol version, and one subscriptions/listen
// request stays open for the whole session. The server answers it only when the
// client cancels it.
const (
	metaNew = `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"claude-code","version":"2.1.312"},"io.modelcontextprotocol/clientCapabilities":{}}`
	listen0 = `{"jsonrpc":"2.0","id":"listen:0","method":"subscriptions/listen","params":{` + metaNew + `,"notifications":{"toolsListChanged":true}}}`
)

func TestOpenListenDoesNotBlockExecAndIsHandedOver(t *testing.T) {
	r := newRig(t, nil, okCaller)
	r.send(listen0 + "\n")
	if l := r.next(); !strings.Contains(l, "subscriptions/acknowledged") {
		t.Fatalf("wanted the acknowledgement, got %s", l)
	}
	r.conn.requestExec("/new/binary")
	select {
	case h := <-r.execs:
		if len(h.Listens) != 1 || string(h.Listens[0]) != listen0 {
			t.Fatalf("listen not handed over: %q", h.Listens)
		}
	case <-time.After(waitMax):
		t.Fatal("no exec while a subscriptions/listen request is open")
	}
}

func TestCancelledRequestIsNotInFlight(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	call := func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		started <- struct{}{}
		<-release // does not return by itself, like a hung daemon call
		return nil, ctx.Err()
	}
	r := newRig(t, nil, call)
	t.Cleanup(func() { close(release) })
	r.send(listen0 + "\n")
	r.next()
	r.send(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{` + metaNew + `,"name":"agent_list","arguments":{}}}` + "\n")
	<-started
	r.conn.requestExec("/new/binary")
	select {
	case <-r.execs:
		t.Fatal("exec while a request was in flight")
	case <-time.After(150 * time.Millisecond):
	}
	// The client gives up on the call: it is no longer in flight.
	r.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":7}}` + "\n")
	select {
	case <-r.execs:
	case <-time.After(waitMax):
		t.Fatal("no exec after the client cancelled the only request")
	}
}

func TestReplayedListenIsAnnouncedNotAckedAgain(t *testing.T) {
	r := newRig(t, &handover{V: 1, Listens: [][]byte{[]byte(listen0)}}, okCaller)
	l := r.next()
	if strings.Contains(l, "acknowledged") {
		t.Fatalf("the client saw the acknowledgement already: %s", l)
	}
	if !strings.Contains(l, "notifications/tools/list_changed") || !strings.Contains(l, `"io.modelcontextprotocol/subscriptionId":"listen:0"`) {
		t.Fatalf("wanted tools/list_changed on subscription listen:0, got %s", l)
	}
	r.noLine()
	// The next upgrade must not be blocked by the replayed listen.
	r.conn.requestExec("/newer/binary")
	select {
	case h := <-r.execs:
		if len(h.Listens) != 1 {
			t.Fatalf("listen not handed over again: %q", h.Listens)
		}
	case <-time.After(waitMax):
		t.Fatal("second exec blocked")
	}
}

func TestSecondExecAfterLegacyReplay(t *testing.T) {
	r := newRig(t, &handover{V: 1, Init: []byte(initReq), Initialized: []byte(initDone)}, okCaller)
	r.next() // list_changed
	r.conn.requestExec("/newer/binary")
	select {
	case <-r.execs:
	case <-time.After(waitMax):
		t.Fatal("the swallowed initialize left a request in flight forever")
	}
}
