package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// benchFixture is a gateway the size of the real one: 40 machines, 60 online
// sessions, 20 projects, 300 changes and 20 skills on 5 machines. It needs no
// real database. Run it with:
//
//	go test ./internal/gateway -run XXX -bench Fixture -benchtime 50x
type benchFixture struct {
	g        *Gateway
	nodes    []string
	sessions map[string][]protocol.SessionInfo // by node
	ids      []string                          // session IDs
	idNode   map[string]string
}

func newBenchFixture(b testing.TB) *benchFixture {
	b.Helper()
	s, err := OpenStore(filepath.Join(b.TempDir(), "gw.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	s.tempFolders = []string{}
	g := &Gateway{store: s, conns: map[string]*nodeConn{}}
	f := &benchFixture{g: g, sessions: map[string][]protocol.SessionInfo{}, idNode: map[string]string{}}
	for i := 0; i < 40; i++ {
		id, _, err := s.CreateNode(fmt.Sprintf("host%02d", i), "linux")
		if err != nil {
			b.Fatal(err)
		}
		if err := s.SetNodeHello(id, fmt.Sprintf("host%02d", i), "0.4.199"); err != nil {
			b.Fatal(err)
		}
		f.nodes = append(f.nodes, id)
		g.conns[id] = &nodeConn{id: id}
	}
	for i := 0; i < 60; i++ {
		node := f.nodes[i%len(f.nodes)]
		project := fmt.Sprintf("proj%02d", i%20)
		root := "/work/" + project
		si := protocol.SessionInfo{Harness: "claude", NativeID: fmt.Sprintf("native-%d", i), Cwd: root, PID: 1000 + i, Name: "task",
			Status: []string{"idle", "busy"}[i%2], Repo: protocol.RepoInfo{Root: root, Worktree: root, Project: project, Branch: "main"}}
		id, err := s.UpsertSession(node, si)
		if err != nil {
			b.Fatal(err)
		}
		si.SessionID = id
		f.sessions[node] = append(f.sessions[node], si)
		f.ids = append(f.ids, id)
		f.idNode[id] = node
	}
	for i := 0; i < 300; i++ {
		id := f.ids[i%len(f.ids)]
		_, err := s.SubmitChange(f.idNode[id], protocol.ChangeSubmitRequest{SessionID: id, Project: fmt.Sprintf("proj%02d", i%20),
			Branch: fmt.Sprintf("feature/b%d", i), TargetBranch: "main", BaseCommit: fmt.Sprintf("%040x", i), HeadCommit: fmt.Sprintf("%040x", i+1000),
			Origin: protocol.ChangeOrigin{Kind: protocol.OriginSession, SessionID: id}})
		if err != nil {
			b.Fatalf("change %d: %v", i, err)
		}
	}
	for n := 0; n < 5; n++ {
		var items []protocol.InventoryItem
		for k := 0; k < 20; k++ {
			items = append(items, protocol.InventoryItem{Kind: "skill", Name: fmt.Sprintf("skill%02d", k), Hash: fmt.Sprintf("h%02d", k), State: "ok", Scope: "user", Location: fmt.Sprintf("/skills/skill%02d", k)})
		}
		if err := s.SetInventory(f.nodes[n], protocol.InventoryReport{State: "ok", ScannedAt: time.Now(), Items: items}); err != nil {
			b.Fatal(err)
		}
	}
	return f
}

func (f *benchFixture) handle(b *testing.B, node, typ string, v any) {
	b.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := f.g.handle(context.Background(), f.g.conns[node], typ, raw); err != nil {
		b.Fatal(err)
	}
	f.g.standingWG.Wait()
}

func BenchmarkFixturePolicyGet(b *testing.B) {
	f := newBenchFixture(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := f.ids[i%len(f.ids)]
		f.handle(b, f.idNode[id], "policy.get", protocol.PolicyGet{SessionID: id, Turn: true})
	}
}

func BenchmarkFixtureQueryAgents(b *testing.B) {
	f := newBenchFixture(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.handle(b, f.nodes[0], "query.agents", protocol.QueryAgents{})
	}
}

// The upsert writes, so every iteration invalidates what a read kept.
func BenchmarkFixtureSessionUpsert(b *testing.B) {
	f := newBenchFixture(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		node := f.nodes[i%len(f.nodes)]
		si := f.sessions[node][i%len(f.sessions[node])]
		si.Status = []string{"idle", "busy"}[i%2]
		f.handle(b, node, "session.upsert", protocol.SessionUpsert{Session: si})
	}
}

// One machine reports all its sessions, as after a reconnect.
func BenchmarkFixtureSessionSync(b *testing.B) {
	f := newBenchFixture(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		node := f.nodes[i%len(f.nodes)]
		f.handle(b, node, "session.sync", protocol.SessionSync{Sessions: f.sessions[node]})
	}
}

// What a busy gateway does in one second: upserts interleaved with the reads
// of prompts and TUI polls.
func BenchmarkFixtureMixed(b *testing.B) {
	f := newBenchFixture(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		node := f.nodes[i%len(f.nodes)]
		si := f.sessions[node][i%len(f.sessions[node])]
		f.handle(b, node, "session.upsert", protocol.SessionUpsert{Session: si})
		id := f.ids[i%len(f.ids)]
		f.handle(b, f.idNode[id], "policy.get", protocol.PolicyGet{SessionID: id, Turn: true})
		f.handle(b, f.nodes[0], "query.agents", protocol.QueryAgents{})
		f.handle(b, f.nodes[0], "query.agents", protocol.QueryAgents{})
	}
}
