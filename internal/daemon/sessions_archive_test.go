package daemon

import (
	"context"
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"testing"
)

// The CTO archives offline sessions at once; the owner can restore them, and
// an online session is refused and stays visible.
func TestSessionsArchiveByCTO(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	cli := cliClient(t, n)
	agent, _ := helloPi(t, n, "peer", "/w/peer", true)
	sid, err := g.gw.Store().UpsertSession(n.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: "archive-junk", Cwd: "/tmp/judge-junk"})
	if err != nil {
		t.Fatal(err)
	}
	if err = g.gw.Store().SetSessionOffline(n.cfg.NodeID, sid); err != nil {
		t.Fatal(err)
	}
	if _, err = agent.call("sessions_archive", map[string]any{"ids": []string{sid}}); err == nil {
		t.Fatal("agent may archive")
	}
	args, _ := json.Marshal(protocol.SessionsArchiveRequest{IDs: []string{sid}})
	if _, err := ctoRunNow(context.Background(), n.d, "sessions_archive", args); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := g.gw.Store().Agent(sid); ok {
		t.Fatal("the CTO archive did not archive")
	}
	cli.mustCall("sessions_archive", protocol.SessionsArchiveRequest{IDs: []string{sid}, Restore: true})
	if _, ok, _ := g.gw.Store().Agent(sid); !ok {
		t.Fatal("owner restore failed")
	}
	online, err := g.gw.Store().UpsertSession(n.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: "online-junk", Cwd: "/tmp/judge-online"})
	if err != nil {
		t.Fatal(err)
	}
	args, _ = json.Marshal(protocol.SessionsArchiveRequest{IDs: []string{online}})
	if _, err := ctoRunNow(context.Background(), n.d, "sessions_archive", args); err == nil {
		t.Fatal("archived an online session")
	}
	if a, ok, _ := g.gw.Store().Agent(online); !ok || !a.Online {
		t.Fatal("online session was hidden")
	}
}
