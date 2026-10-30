package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Claude Code 2.1.312 speaks MCP 2026-07-28: no initialize, every request
// carries its protocol version, and it opens one subscriptions/listen request
// that stays open for the whole session (the server never answers it). The
// switch must not treat that request as work in progress, and the new process
// must take over the subscription.
const (
	newMeta     = `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"claude-code","version":"2.1.312"},"io.modelcontextprotocol/clientCapabilities":{}}`
	listenLine  = `{"jsonrpc":"2.0","id":"listen:0","method":"subscriptions/listen","params":{` + newMeta + `,"notifications":{"toolsListChanged":true}}}`
	discoverReq = `{"jsonrpc":"2.0","id":0,"method":"server/discover","params":{` + newMeta + `}}`
)

func newListReq(id int) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/list","params":{%s}}`, id, newMeta)
}

func newCallReq(id int) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{%s,"name":"agent_list","arguments":{}}}`, id, newMeta)
}

func waitExe(t *testing.T, pid int, want string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		got, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
		if err == nil && got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("/proc/%d/exe = %q (%v), want %q", pid, got, err, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestMCPSwitchesWithOpenSubscription(t *testing.T) {
	tmp, exe, newBin, sock := setupSelfUpgrade(t)
	newBin3 := filepath.Join(tmp, "agent-net-new3")
	buildAs(t, newBin3, "v0.0.3")
	d := newFakeDaemon(t, sock, "v0.0.1")
	p := startMCP(t, exe, sock, tmp)
	pid := p.cmd.Process.Pid
	c := p.client
	d.waitHello(t, "v0.0.1")

	// The session as Claude Code runs it: discover, listen, list tools, call one.
	c.send(discoverReq)
	c.reply(0)
	c.send(listenLine)
	if l := c.next(); !strings.Contains(l, "notifications/subscriptions/acknowledged") {
		t.Fatalf("wanted the acknowledgement of the listen, got %s", l)
	}
	c.send(newListReq(1))
	before, _ := c.reply(1)
	c.send(newCallReq(2))
	if l, _ := c.reply(2); !strings.Contains(l, "from-fake-daemon") {
		t.Fatalf("tools/call before the switch: %s", l)
	}

	// Two upgrades in a row, each with no further tool call: the idle session
	// notices the daemon restart by itself and switches while the listen is open.
	for i, step := range []struct{ from, version string }{{newBin, "v0.0.2"}, {newBin3, "v0.0.3"}} {
		swapBinary(t, step.from, exe)
		d.restart(step.version)
		d.waitHello(t, step.version)
		l := c.next()
		if strings.Contains(l, "acknowledged") {
			t.Fatalf("upgrade %d: the new process acknowledged the listen again: %s", i+1, l)
		}
		if !strings.Contains(l, "notifications/tools/list_changed") || !strings.Contains(l, `"io.modelcontextprotocol/subscriptionId":"listen:0"`) {
			t.Fatalf("upgrade %d: wanted tools/list_changed on subscription listen:0, got %s", i+1, l)
		}
		if runtime.GOOS == "linux" {
			waitExe(t, pid, exe)
		}
		// Same process, same pipes: the session still works.
		c.send(newListReq(10 + i))
		after, notes := c.reply(10 + i)
		if len(notes) != 0 {
			t.Fatalf("upgrade %d: unexpected lines: %v", i+1, notes)
		}
		if toolNames(t, after) != toolNames(t, before) {
			t.Fatalf("upgrade %d: tools %q, before %q", i+1, toolNames(t, after), toolNames(t, before))
		}
		c.send(newCallReq(20 + i))
		if l, _ := c.reply(20 + i); !strings.Contains(l, "from-fake-daemon") {
			t.Fatalf("upgrade %d: tools/call after the switch: %s", i+1, l)
		}
	}
	if p.cmd.Process.Pid != pid {
		t.Fatal("pid changed")
	}
	select {
	case <-p.exited:
		t.Fatal("the process exited")
	default:
	}
}
