//go:build measure

package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// TestMeasureTUIPollingBytes prints how many bytes each list the TUI polls
// answers on a copy of a real gateway database. It is a measurement, not a
// check, and runs only when AGENT_NET_MEASURE_DB names the copy:
//
//	AGENT_NET_MEASURE_DB=/path/to/gw.db go test -tags measure ./internal/gateway -run TestMeasureTUIPollingBytes -v
func TestMeasureTUIPollingBytes(t *testing.T) {
	src := os.Getenv("AGENT_NET_MEASURE_DB")
	if src == "" {
		t.Skip("AGENT_NET_MEASURE_DB is not set")
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gateway.db"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	g, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.store.Close() })
	owner := ""
	_ = g.store.db.QueryRow(`SELECT value FROM owner_settings WHERE key='owner_node'`).Scan(&owner)
	nc := &nodeConn{id: owner}
	versions := g.topicVersions()
	type call struct {
		name, frame string
		params      any
	}
	calls := []call{
		{"query.agents", "query.agents", protocol.QueryAgents{IncludeOwners: true, Lite: true}},
		{"query.nodes", "query.nodes", protocol.QueryNodes{}},
		{"change.list", "change.list", protocol.ChangeListRequest{Lite: true}},
		{"query.projects", "query.projects", protocol.QueryProjects{}},
		{"asks.list", "asks.list", nil},
		{"chief.sorts", "chief.sorts", nil},
		{"query.counts", "query.counts", nil},
		{"query.usage", "query.usage", nil},
		{"query.events", "query.events", protocol.QueryEvents{Limit: 100}},
		{"query.agents if_version", "query.agents", protocol.QueryAgents{IncludeOwners: true, Lite: true, IfVersion: versions[protocol.TopicAgents]}},
		{"query.nodes if_version", "query.nodes", protocol.QueryNodes{IfVersion: versions[protocol.TopicNodes]}},
		{"change.list if_version", "change.list", protocol.ChangeListRequest{Lite: true, IfVersion: g.store.readStamp(topicTables[protocol.TopicChanges])}},
	}
	for _, c := range calls {
		var in []byte
		if c.params != nil {
			in, _ = json.Marshal(c.params)
		}
		start := time.Now()
		res, err := g.handle(context.Background(), nc, c.frame, in)
		took := time.Since(start)
		out, _ := json.Marshal(res)
		t.Logf("%-26s %9d bytes %8s err=%v", c.name, len(out), took.Round(time.Millisecond), err)
	}
}
