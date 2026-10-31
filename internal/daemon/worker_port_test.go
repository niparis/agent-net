package daemon

import (
	"context"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestWorkerRetriesPortCollision(t *testing.T) {
	for _, harness := range []string{"codex", "opencode"} {
		t.Run(harness, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "occupied", http.StatusServiceUnavailable) }))
			occupied := ln.Addr().(*net.TCPAddr).Port
			calls := 0
			g := startGateway(t, t.TempDir(), "")
			t.Setenv(envFakeCodex, "1")
			t.Setenv("CODEX_HOME", t.TempDir())
			w := startWorkerNode(t, g, shortSet, func(c *Config) {
				c.HarnessPaths = map[string]string{"codex": os.Args[0]}
				c.testFreePort = func() (int, error) {
					calls++
					if calls == 1 {
						return occupied, nil
					}
					return freePort()
				}
			})
			repo := newRepo(t, "shop")
			w.register(t, repo)
			_, err = w.start(protocol.WorkerStartRequest{Project: "shop", Harness: harness, Task: "say ok"})
			if err != nil {
				t.Fatal(err)
			}
			if calls < 2 {
				t.Fatal("port collision was not retried")
			}
		})
	}
}

func TestCTORetriesPortCollision(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "occupied", http.StatusServiceUnavailable) }))
	occupied := ln.Addr().(*net.TCPAddr).Port
	calls := 0
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) {
		c.HarnessPaths = map[string]string{"opencode": os.Args[0]}
		c.testFreePort = func() (int, error) {
			calls++
			if calls == 1 {
				return occupied, nil
			}
			return freePort()
		}
	}})
	n.d.cto.mu.Lock()
	err = n.d.startCTOLocked(context.Background())
	n.d.cto.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if calls < 2 {
		t.Fatal("port collision was not retried")
	}
}
