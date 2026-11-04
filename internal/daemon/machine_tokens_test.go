package daemon

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestMachineTokenAtomicSave(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "node.json")
	if err := os.WriteFile(path, []byte(`{"node_id":"n_test","token":"old","extra":true}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := saveRotatedToken(dir, "n_test", "old", "new"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	st, _ := os.Stat(path)
	if fields["token"] != "new" || fields["extra"] != true || st.Mode().Perm() != 0600 {
		t.Fatalf("saved %s, mode %v", raw, st.Mode())
	}
	if err := saveRotatedToken(dir, "wrong", "new", "bad"); err == nil {
		t.Fatal("wrong identity accepted")
	}
}

func TestMachineActionsLinkAndCTOProposal(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	owner := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi"})
	logs := &logBuf{}
	target := startNode(t, g, nodeOpts{logger: log.New(logs, "", 0)})
	cli := cliClient(t, owner)
	agent, _ := helloPi(t, owner, "machine-agent", "/work/test", true)
	args := map[string]string{"node_id": target.cfg.NodeID}
	for _, method := range []string{"machine_remove", "machine_rotate"} {
		if _, err := agent.call(method, args); err == nil || !strings.Contains(err.Error(), "only available to the owner") {
			t.Fatalf("%s: %v", method, err)
		}
	}
	target.d.mu.Lock()
	before := target.d.lk
	target.d.mu.Unlock()
	old := target.cfg.Token
	raw, _ := json.Marshal(args)
	proposal, err := owner.d.Propose(context.Background(), "settings_change", "machine_rotate", raw, "")
	if err != nil {
		t.Fatal(err)
	}
	var proposals []protocol.CTOProposal
	_ = json.Unmarshal(cli.mustCall("proposal_list", nil), &proposals)
	if len(proposals) != 1 || proposals[0].ID != proposal {
		t.Fatalf("proposals %+v", proposals)
	}
	cli.mustCall("proposal_answer", protocol.CTOProposalAnswer{ID: proposal, Action: "a"})
	eventually(t, "rotated node reconnected", func() bool {
		data, err := os.ReadFile(filepath.Join(target.cfg.NodeConfigDir, "node.json"))
		if err != nil {
			return false
		}
		var nc NodeConfig
		_ = json.Unmarshal(data, &nc)
		target.d.mu.Lock()
		reconnected := target.d.lk != nil && target.d.lk != before
		target.d.mu.Unlock()
		return nc.Token != "" && nc.Token != old && reconnected
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, res, err := websocket.Dial(ctx, target.cfg.GatewayURL+"/v1/node", &websocket.DialOptions{HTTPClient: target.d.client, HTTPHeader: http.Header{"Authorization": {"Bearer " + old}}})
	if ws != nil {
		ws.CloseNow()
	}
	if err == nil || res == nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token accepted: %v %+v", err, res)
	}
	proposal, err = owner.d.Propose(context.Background(), "settings_change", "machine_remove", raw, "")
	if err != nil {
		t.Fatal(err)
	}
	cli.mustCall("proposal_answer", protocol.CTOProposalAnswer{ID: proposal, Action: "a"})
	eventually(t, "removed node disconnected", func() bool { return !target.d.Connected() })
	eventually(t, "clear removal log", func() bool {
		return len(logs.lines("this machine was removed from the network; run agent-net setup to join again")) > 0
	})
	if _, err := owner.d.Propose(context.Background(), "settings_change", "machine_remove", raw, ""); err == nil {
		t.Fatal("removed target proposed again")
	}
}

func TestMachineActionsOwnNode(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	cli := cliClient(t, n)
	n.d.mu.Lock()
	before := n.d.lk
	n.d.mu.Unlock()
	cli.mustCall("machine_rotate", map[string]string{"node_id": n.cfg.NodeID})
	eventually(t, "own rotation reconnect", func() bool { n.d.mu.Lock(); defer n.d.mu.Unlock(); return n.d.lk != nil && n.d.lk != before })
	if _, err := cli.call("machine_remove", map[string]string{"node_id": n.cfg.NodeID}); err == nil || !strings.Contains(err.Error(), "cannot remove itself") {
		t.Fatalf("own removal: %v", err)
	}
	if !n.d.Connected() {
		t.Fatal("refused own removal disconnected the machine")
	}
}
