package claude

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestDeliveryHealth(t *testing.T) {
	if got := deliveryHealth(nil); got != "unknown" {
		t.Fatal(got)
	}
	if got := deliveryHealth(map[string]found{"s": {}}); got != "broken" {
		t.Fatal(got)
	}
	path := filepath.Join(t.TempDir(), "inbox")
	if got := deliveryHealth(map[string]found{"s": {sock: path}}); got != "broken" {
		t.Fatal(got)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			c.Close()
		}
	}()
	if got := deliveryHealth(map[string]found{"s": {sock: path}}); got != "ok" {
		t.Fatal(got)
	}
	ln.Close()
	if got := deliveryHealth(map[string]found{"s": {sock: path}}); got != "broken" {
		t.Fatal("refused socket:", got)
	}
	os.Remove(path)
}

func TestClaudeDeliveryHealthSessionFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENT_NET_CLAUDE_SESSIONS_DIR", dir)
	a := New(Config{})
	if got := a.CheckDelivery("test-version"); got != "" {
		t.Fatal(got)
	}
	raw := fmt.Sprintf(`{"pid":%d,"sessionId":"live"}`, os.Getpid())
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", os.Getpid())), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	want := "Claude delivery broken by Claude Code test-version"
	// One broken check alone is not enough: a socket can be briefly away.
	if got := a.CheckDelivery("test-version"); got != "" {
		t.Fatalf("one broken check warned: %q", got)
	}
	if got := a.CheckDelivery("test-version"); got != want {
		t.Fatalf("got %q", got)
	}
	target := protocol.SessionInfo{NativeID: "live"}
	if got := a.Deliver(context.Background(), target, protocol.Message{}); got.Delivered || got.Detail != want {
		t.Fatalf("delivery: %+v", got)
	}
	if got := a.DeliverCTO(context.Background(), target, "id", "body"); got.Delivered || got.Detail != want {
		t.Fatalf("CTO delivery: %+v", got)
	}
	path := filepath.Join(dir, "sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	raw = fmt.Sprintf(`{"pid":%d,"sessionId":"live","messagingSocketPath":%q}`, os.Getpid(), path)
	os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", os.Getpid())), []byte(raw), 0600)
	if got := a.CheckDelivery("later-version"); got != "" {
		t.Fatal(got)
	}
	if a.deliveryWarning() != "" {
		t.Fatal("warning not cleared")
	}
	ln.Close()
	if got := a.CheckDelivery("later-version"); got != "" {
		t.Fatalf("one broken check warned: %q", got)
	}
	if got := a.CheckDelivery("later-version"); got != "Claude delivery broken by Claude Code later-version" {
		t.Fatal(got)
	}
	os.Remove(filepath.Join(dir, fmt.Sprintf("%d.json", os.Getpid())))
	if got := a.CheckDelivery("later-version"); got != "" {
		t.Fatal("unknown blocks delivery:", got)
	}
}
