package daemon

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
	"modernc.org/sqlite"
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

	// Recreate the pre-3c schema while the test gateway is running. Acquire
	// the write lock before reading schema state, and retry with a fresh
	// connection if a concurrent gateway write wins the lock.
	dbPath := filepath.Join(dir, "gateway.db")
	isBusy := func(err error) bool {
		var sqliteErr *sqlite.Error
		return errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == 5
	}
	rewriteSchema := func(statements string) bool {
		db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)")
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.Exec("BEGIN IMMEDIATE;" + statements + "COMMIT;")
		if err != nil {
			_, _ = db.Exec(`ROLLBACK`)
		}
		if closeErr := db.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if err != nil && !isBusy(err) {
			t.Fatal(err)
		}
		return err == nil
	}
	eventually(t, "rewrite gateway schema", func() bool {
		return rewriteSchema(`
 DROP TRIGGER edge_kinds_insert; DROP TRIGGER edge_history_kinds_insert;
 DROP TRIGGER merger_removed; DROP TRIGGER merger_replaced;
 ALTER TABLE graph_edges DROP COLUMN source_kind; ALTER TABLE graph_edges DROP COLUMN target_kind;
 ALTER TABLE graph_history DROP COLUMN source_kind; ALTER TABLE graph_history DROP COLUMN target_kind;
 DELETE FROM schema_version WHERE version>=113;`)
	})
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
	// The current gateway needs the identity columns for the before-send.
	// Remove them only afterwards, so replay starts from the real pre-114
	// schema and applies the frozen migration without rewriting it.
	eventually(t, "rewind session identity schema", func() bool {
		return rewriteSchema(`
 DROP TRIGGER session_identity_repo_update;
 ALTER TABLE sessions DROP COLUMN agent_id;
 ALTER TABLE sessions DROP COLUMN repo_id;
 ALTER TABLE worker_runs DROP COLUMN caller_agent_id;
 ALTER TABLE worker_starts DROP COLUMN caller_agent_id;
 DROP TABLE session_agent_history;`)
	})
	// OpenStore's migration also uses a transaction that can lose a
	// snapshot to a live gateway write. A failed OpenStore closes its DB.
	eventually(t, "migrate gateway schema", func() bool {
		migrated, err := gateway.OpenStore(dbPath)
		if err != nil {
			if isBusy(err) {
				return false
			}
			t.Fatal(err)
		}
		if err := migrated.Close(); err != nil {
			t.Fatal(err)
		}
		return true
	})
	after := send("after migration")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("recipients changed: %v -> %v", before, after)
	}
	eventually(t, "both group sends delivered", func() bool { return len(fa.delivered()) == 4 })
}
