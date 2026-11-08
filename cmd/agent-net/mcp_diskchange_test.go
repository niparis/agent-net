package main

import (
	"strings"
	"testing"
)

func TestMCPDetectsBinaryReplacementWithoutDaemonRestart(t *testing.T) {
	tmp, exe, newBin, sock := setupSelfUpgrade(t)
	d := newFakeDaemon(t, sock, "v0.0.1")
	p := startMCP(t, exe, sock, tmp)
	d.waitHello(t, "v0.0.1")
	handshake(t, p.client)
	swapBinary(t, newBin, exe)
	d.waitHello(t, "v0.0.2")
	p.client.send(`{"jsonrpc":"2.0","id":20,"method":"tools/list"}`)
	line, _ := p.client.reply(20)
	if !strings.Contains(line, "agent_sessions_find") {
		t.Fatalf("tools/list after replacement: %s", line)
	}
}
