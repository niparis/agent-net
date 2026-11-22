package daemon

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// The daemon listens in a folder of its own, and the public path is a link to
// it. That folder holds only the socket, so a sandbox that binds it exposes
// nothing else, and the new socket a restarted daemon creates there replaces
// the old one for every client.
func TestDaemonSocketLivesInItsOwnFolder(t *testing.T) {
	path := filepath.Join(shortDir(t), "daemon.sock")
	srv, err := listenLocal(&Daemon{}, path)
	if err != nil {
		t.Fatal(err)
	}
	open := srv
	t.Cleanup(func() { open.close() })

	target, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("the public socket path is not a link: %v", err)
	}
	if want := filepath.Join("sock", "daemon.sock"); target != want {
		t.Fatalf("the link points at %q, want %q", target, want)
	}
	entries, err := os.ReadDir(socketDir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "daemon.sock" {
		t.Fatalf("the socket folder holds %v, want only daemon.sock", entries)
	}

	dial := func() {
		t.Helper()
		c, err := net.Dial("unix", path)
		if err != nil {
			t.Fatalf("a client cannot reach the public path: %v", err)
		}
		c.Close()
	}
	dial()

	// A restart removes the socket and creates a new one at the same path.
	srv.close()
	srv, err = listenLocal(&Daemon{}, path)
	if err != nil {
		t.Fatal(err)
	}
	open = srv
	dial()
}
