package daemon

import (
	"context"
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
)

func TestSessionsArchiveProposalApproval(t *testing.T) {
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
	id, err := n.d.Propose(context.Background(), "sessions_archive", "", args, "Clear junk")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := g.gw.Store().Agent(sid); !ok {
		t.Fatal("proposal acted before approval")
	}
	raw := cli.mustCall(protocol.OpProposalList, nil)
	if !strings.Contains(string(raw), sid) || !strings.Contains(string(raw), "judge-junk") {
		t.Fatalf("missing dry run: %s", raw)
	}
	cli.mustCall(protocol.OpProposalAnswer, protocol.CTOProposalAnswer{ID: id, Action: "a"})
	if _, ok, _ := g.gw.Store().Agent(sid); ok {
		t.Fatal("approved proposal did not archive")
	}
	cli.mustCall("sessions_archive", protocol.SessionsArchiveRequest{IDs: []string{sid}, Restore: true})
	if _, ok, _ := g.gw.Store().Agent(sid); !ok {
		t.Fatal("owner restore failed")
	}
	// Pattern membership is frozen at proposal creation.
	pattern, _ := json.Marshal(protocol.SessionsArchiveRequest{Folder: "/tmp/judge-*"})
	id, err = n.d.Propose(context.Background(), "sessions_archive", "", pattern, "Clear junk")
	if err != nil {
		t.Fatal(err)
	}
	newer, err := g.gw.Store().UpsertSession(n.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: "newer-junk", Cwd: "/tmp/judge-newer"})
	if err != nil {
		t.Fatal(err)
	}
	if err = g.gw.Store().SetSessionOffline(n.cfg.NodeID, newer); err != nil {
		t.Fatal(err)
	}
	cli.mustCall(protocol.OpProposalAnswer, protocol.CTOProposalAnswer{ID: id, Action: "a"})
	if _, ok, _ := g.gw.Store().Agent(sid); ok {
		t.Fatal("pattern proposal did not archive original")
	}
	if _, ok, _ := g.gw.Store().Agent(newer); !ok {
		t.Fatal("proposal included a later match")
	}
	// Approval rechecks online status and fails without hiding a resumed session.
	args, _ = json.Marshal(protocol.SessionsArchiveRequest{IDs: []string{newer}})
	id, err = n.d.Propose(context.Background(), "sessions_archive", "", args, "Clear newer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.gw.Store().UpsertSession(n.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: "newer-junk", Cwd: "/tmp/judge-newer"}); err != nil {
		t.Fatal(err)
	}
	if _, err = cli.call(protocol.OpProposalAnswer, protocol.CTOProposalAnswer{ID: id, Action: "a"}); err == nil {
		t.Fatal("approved an online session")
	}
	if a, ok, _ := g.gw.Store().Agent(newer); !ok || !a.Online {
		t.Fatal("online session was hidden")
	}

}
