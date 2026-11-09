package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// A daemon that refuses a request and hangs up at once must not look like an
// outage. The reply was read from the connection, so it wins over the close.
// This is what a refused hello does: the daemon answers and returns.
func TestCallReturnsAReplyReadBeforeTheClose(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "d.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		line, err := bufio.NewReader(c).ReadBytes('\n')
		if err != nil {
			return
		}
		var req struct {
			ID json.RawMessage `json:"id"`
		}
		json.Unmarshal(line, &req)
		out, _ := json.Marshal(map[string]any{"id": req.ID, "error": "hello refused for the test"})
		c.Write(append(out, '\n'))
		// Return at once: the close follows the write, as the real daemon
		// does when it refuses a hello.
	}()

	cl, err := Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cl.Close() })
	// Hold the wait until the read loop has seen the reply and the close.
	cl.beforeWait = func() { <-cl.dead }

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = cl.Call(ctx, "hello", nil)
	var re RemoteError
	if !errors.As(err, &re) || re.Error() != "hello refused for the test" {
		t.Fatalf("want the refusal that was read before the close, got %v", err)
	}
}
