package daemon

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
)

// Exercise the daemon's actual group-send path across the gateway migration.
func TestAdvisorGroupSendEdgeMigration(t *testing.T) {
	dir := t.TempDir()
	g := startGateway(t, dir, "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa, cmdline: "pi"})
	fa.setSessions(fakeSession("na", "alice"), fakeSession("nb", "bob"))
	eventually(t, "recipients known", func() bool { return fa.host.SessionID("fake", "na") != "" && fa.host.SessionID("fake", "nb") != "" })
	st := g.gw.Store()
	if _, err := st.AddProjects(n.d.cfg.NodeID, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "shop", Path: "/w/shop"}}}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddArchitect("architect", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetEdge(protocol.Edge{From: "architect", To: "shop", Kind: "governs", Mode: "advisor"}, "owner"); err != nil {
		t.Fatal(err)
	}
	advisor, _ := helloPi(t, n, "advisor", "/w/architect", true)

	// Recreate the pre-3c schema in the temporary gateway database. The
	// gateway is idle while the schema transaction runs.
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "gateway.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`BEGIN;
 DROP TRIGGER edge_kinds_insert; DROP TRIGGER edge_history_kinds_insert;
 DROP TRIGGER merger_removed; DROP TRIGGER merger_replaced;
 ALTER TABLE graph_edges DROP COLUMN source_kind; ALTER TABLE graph_edges DROP COLUMN target_kind;
 ALTER TABLE graph_history DROP COLUMN source_kind; ALTER TABLE graph_history DROP COLUMN target_kind;
 DELETE FROM schema_version WHERE version=(SELECT max(version) FROM schema_version);
 COMMIT;`)
	if err != nil {
		t.Fatal(err)
	}
	send := func(body string) []string {
		t.Helper()
		raw, err := advisor.call("send", map[string]any{"to_set": map[string]any{"harness": "fake"}, "body": body})
		if err != nil {
			t.Fatal(err)
		}
		result := groupReceipts(t, raw)
		var recipients []string
		for _, receipt := range result.Sent {
			if receipt.MessageID == "" || receipt.State == "" {
				t.Fatalf("incomplete receipt: %+v", receipt)
			}
			recipients = append(recipients, receipt.To.ID)
		}
		sort.Strings(recipients)
		return recipients
	}
	before := send("before migration")
	if len(before) != 2 || before[0] == before[1] {
		t.Fatalf("before recipients: %v", before)
	}
	migrated, err := gateway.OpenStore(filepath.Join(dir, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := migrated.Close(); err != nil {
		t.Fatal(err)
	}
	after := send("after migration")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("recipients changed: %v -> %v", before, after)
	}
	eventually(t, "both group sends delivered", func() bool { return len(fa.delivered()) == 4 })
}
