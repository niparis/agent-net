package daemon

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A daemon.db from before decision 0199 holds the CTO's stored proposals. On
// upgrade each open one goes to the chief of staff as a request from the CTO,
// with its action, target and reason, and the table is dropped. A proposal
// that was decided sends nothing. A restart, or a crash after the send but
// before the local row was removed, never sends one twice.
func TestOpenCTOProposalsGoToTheChiefOnUpgrade(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi"})
	ctx := context.Background()
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	cto := n.d.ctoSessionID()
	_, chief := helloPi(t, n, "carry-chief", "/w/chief", true)
	if err := g.gw.Store().SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	n.d.Stop()

	// The tables as the last daemon with stored proposals left them.
	db, err := sql.Open("sqlite", "file:"+filepath.Join(n.cfg.StateDir, "daemon.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
CREATE TABLE cto_proposals (id INTEGER PRIMARY KEY AUTOINCREMENT, tool TEXT NOT NULL, action TEXT NOT NULL, args TEXT NOT NULL, reason TEXT NOT NULL, created_at INTEGER NOT NULL, status TEXT NOT NULL, note TEXT NOT NULL DEFAULT '', result TEXT NOT NULL DEFAULT '', note_delivered INTEGER NOT NULL DEFAULT 0);
CREATE TABLE cto_proposal_messages (proposal_id INTEGER NOT NULL, sender TEXT NOT NULL, at INTEGER NOT NULL);
INSERT INTO cto_proposals(id,tool,action,args,reason,created_at,status) VALUES
 (7,'graph_change','node_remove','{"action":"node_remove","node":"old-repo"}','old-repo has had no session for 90 days',?,'open'),
 (8,'cto_op','machine_remove','{"method":"machine_remove","args":{"node_id":"n_gone"}}','the machine was sold',?,'open'),
 (9,'graph_change','repo_archive','{"action":"repo_archive","repo":"done-repo"}','already decided',?,'applied');
INSERT INTO cto_proposal_messages VALUES (7,'s_x',1);`, time.Now().UnixNano(), time.Now().UnixNano(), time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	db.Close()

	toChief := func(t *testing.T) []protocol.Message {
		t.Helper()
		all, err := g.gw.Store().Messages(n.d.NodeID(), protocol.QueryMessages{SessionID: chief, Limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		var out []protocol.Message
		for _, m := range all {
			if m.From == cto && m.To == chief && strings.Contains(m.Body, "CTO proposal") {
				out = append(out, m)
			}
		}
		return out
	}
	restart := func(t *testing.T) *Daemon {
		t.Helper()
		d, err := New(n.cfg)
		if err != nil {
			t.Fatal(err)
		}
		d.inSession = func(int) bool { return false }
		if err := d.Start(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(d.Stop)
		eventually(t, "daemon connected to gateway", d.Connected)
		return d
	}
	carried := func(t *testing.T, d *Daemon) int {
		t.Helper()
		var count int
		if err := d.st.db.QueryRow(`SELECT COUNT(*) FROM cto_proposal_carry`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}

	d := restart(t)
	eventually(t, "both open proposals reach the chief", func() bool { return len(toChief(t)) == 2 && carried(t, d) == 0 })
	for _, m := range toChief(t) {
		if m.Type != protocol.TypeRequest {
			t.Errorf("message %s is a %s, want a request", m.ID, m.Type)
		}
	}
	bodies := toChief(t)[0].Body + "\n" + toChief(t)[1].Body
	for _, want := range []string{
		"graph_change node_remove", `"node":"old-repo"`, "old-repo has had no session for 90 days",
		"cto_op machine_remove", `"node_id":"n_gone"`, "the machine was sold", "owner_words",
	} {
		if !strings.Contains(bodies, want) {
			t.Errorf("the messages do not name %q:\n%s", want, bodies)
		}
	}
	if strings.Contains(bodies, "done-repo") {
		t.Errorf("a decided proposal was sent:\n%s", bodies)
	}
	for _, table := range []string{"cto_proposals", "cto_proposal_messages"} {
		var left int
		if err := d.st.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&left); err != nil || left != 0 {
			t.Fatalf("table %s still exists (%d, %v)", table, left, err)
		}
	}

	// A crash after the send and before the row was removed leaves it in
	// the carry table. The next start sends it again with the same key, and
	// the gateway keeps the first message.
	if _, err := d.st.db.Exec(`INSERT INTO cto_proposal_carry(id,tool,action,args,reason,created_at) VALUES (7,'graph_change','node_remove','{"action":"node_remove","node":"old-repo"}','old-repo has had no session for 90 days',1)`); err != nil {
		t.Fatal(err)
	}
	d.Stop()
	d = restart(t)
	eventually(t, "the replayed row is cleared", func() bool { return carried(t, d) == 0 })
	if got := len(toChief(t)); got != 2 {
		t.Fatalf("the chief has %d proposal messages after a replay, want 2", got)
	}
}
