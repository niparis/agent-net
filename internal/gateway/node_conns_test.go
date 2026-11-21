package gateway

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// setTestConn makes id a live node connection, the way the server does when a
// node connects. It writes g.conns under g.mu: background goroutines such as
// settingsChanged read the map under that lock, so an unlocked write from a
// test races them under -race. The entry is removed when the test ends.
func setTestConn(t testing.TB, g *Gateway, id string, nc *nodeConn) {
	t.Helper()
	g.mu.Lock()
	g.conns[id] = nc
	g.mu.Unlock()
	t.Cleanup(func() { removeTestConn(g, id) })
}

// removeTestConn takes a node offline during a test.
func removeTestConn(g *Gateway, id string) {
	g.mu.Lock()
	delete(g.conns, id)
	g.mu.Unlock()
}

// resetTestConns takes every node offline during a test.
func resetTestConns(g *Gateway) {
	g.mu.Lock()
	g.conns = map[string]*nodeConn{}
	g.mu.Unlock()
}

// Tests must change g.conns only through the helpers above, so the lock is
// held on every write. This file defines them, so it is the one exception.
func TestGatewayTestsChangeConnsOnlyThroughHelpers(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	direct := regexp.MustCompile(`\.conns\[[^\]]+\]\s*=[^=]|\.conns\s*=[^=]|delete\([\w.]+\.conns,`)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, "_test.go") || name == "node_conns_test.go" {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if direct.MatchString(line) {
				t.Errorf("%s:%d changes g.conns without g.mu, use setTestConn, removeTestConn or resetTestConns: %s", name, i+1, strings.TrimSpace(line))
			}
		}
	}
}
