package gateway

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func autoCutoverGateway(t *testing.T, now time.Time) (*Gateway, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	g, err := New(Options{
		DataDir: filepath.Join(t.TempDir(), "gateway"),
		Version: "v0.4.145",
		Logger:  log.New(logs, "", 0),
		Now:     func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	return g, logs
}

func reportGraphNode(t *testing.T, g *Gateway, host, version string, supported bool) string {
	t.Helper()
	id, _, err := g.store.CreateNode(host, "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.store.SetNodeGraphRefs(id, version, supported); err != nil {
		t.Fatal(err)
	}
	if err := g.store.SetNodeHello(id, host, version); err != nil {
		t.Fatal(err)
	}
	return id
}

func waitGraphCutoverOn(t *testing.T, g *Gateway) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		on, err := g.store.graphRefsEnabled()
		if err != nil {
			t.Fatal(err)
		}
		if on {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the Graph cutover")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func graphCutoverBackup(t *testing.T, g *Gateway) string {
	t.Helper()
	var backup string
	if err := g.store.db.QueryRow(`SELECT backup FROM graph_ref_gate WHERE id=1`).Scan(&backup); err != nil {
		t.Fatal(err)
	}
	return backup
}

func openGraphCutoverNotices(t *testing.T, g *Gateway, marker string) []string {
	t.Helper()
	rows, err := g.store.db.Query(`SELECT text FROM inbox_items WHERE message_id=? AND status='open' ORDER BY created_at`, marker)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			t.Fatal(err)
		}
		out = append(out, text)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestGraphCutoverAutoEnablesAtStart(t *testing.T) {
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	g, logs := autoCutoverGateway(t, now)
	reportGraphNode(t, g, "fixture", "v0.4.145", true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.RunGraphCutover(ctx)
	waitGraphCutoverOn(t, g)

	backup := graphCutoverBackup(t, g)
	wantDir := filepath.Dir(g.opts.DataDir)
	if !strings.HasPrefix(backup, filepath.Join(wantDir, "gateway-before-typed-graph-")) {
		t.Fatalf("backup %q is not beside the data directory %q", backup, g.opts.DataDir)
	}
	if _, err := os.Stat(filepath.Join(backup, "gateway.db")); err != nil {
		t.Fatalf("missing backup database: %v", err)
	}
	if !strings.Contains(logs.String(), backup) {
		t.Fatalf("log does not name the backup %q: %s", backup, logs.String())
	}
}

func TestGraphCutoverWaitsWhileANodeIsBehind(t *testing.T) {
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	g, _ := autoCutoverGateway(t, now)
	reportGraphNode(t, g, "ready", "v0.4.145", true)
	reportGraphNode(t, g, "behind", "v0.4.144", false)

	g.checkGraphCutover(context.Background())

	if on, err := g.store.graphRefsEnabled(); err != nil || on {
		t.Fatalf("cutover enabled with a machine behind: %v %v", on, err)
	}
	if backup := graphCutoverBackup(t, g); backup != "" {
		t.Fatalf("backup taken while blocked: %q", backup)
	}
}

func TestGraphCutoverEnablesWhenTheLastNodeUpgrades(t *testing.T) {
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	g, _ := autoCutoverGateway(t, now)
	first := reportGraphNode(t, g, "first", "v0.4.145", true)
	second, _, err := g.store.CreateNode("second", "linux")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := g.handle(context.Background(), &nodeConn{id: first}, "hello", []byte(`{"version":"v0.4.145","hostname":"first","graph_refs":true}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := g.handle(context.Background(), &nodeConn{id: second}, "hello", []byte(`{"version":"v0.4.144","hostname":"second"}`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if on, err := g.store.graphRefsEnabled(); err != nil || on {
		t.Fatalf("cutover enabled before every machine upgraded: %v %v", on, err)
	}

	if _, err := g.handle(context.Background(), &nodeConn{id: second}, "hello", []byte(`{"version":"v0.4.145","hostname":"second","graph_refs":true}`)); err != nil {
		t.Fatal(err)
	}
	waitGraphCutoverOn(t, g)
}

func TestGraphCutoverFailureKeepsBackupAndRaisesOneInboxItem(t *testing.T) {
	now := time.Date(2026, 11, 7, 10, 0, 5, 0, time.UTC)
	g, _ := autoCutoverGateway(t, now)
	reportGraphNode(t, g, "fixture", "v0.4.145", true)
	if _, err := g.store.db.Exec(`CREATE TRIGGER fail_graph_cutover BEFORE UPDATE ON graph_ref_gate BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}

	g.checkGraphCutover(context.Background())

	if on, err := g.store.graphRefsEnabled(); err != nil || on {
		t.Fatalf("failed cutover changed the gate: %v %v", on, err)
	}
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(g.opts.DataDir), "gateway-before-typed-graph-*"))
	if err != nil || len(backups) == 0 {
		t.Fatalf("backup not kept after failure: %v %v", backups, err)
	}
	items := openGraphCutoverNotices(t, g, graphCutoverFailureMarker)
	if len(items) != 1 || !strings.Contains(items[0], "fixture failure") {
		t.Fatalf("failure inbox: %v", items)
	}

	g.checkGraphCutover(context.Background())
	if items = openGraphCutoverNotices(t, g, graphCutoverFailureMarker); len(items) != 1 {
		t.Fatalf("retry raised a second inbox item: %v", items)
	}

	if _, err := g.store.db.Exec(`DROP TRIGGER fail_graph_cutover`); err != nil {
		t.Fatal(err)
	}
	g.checkGraphCutover(context.Background())
	waitGraphCutoverOn(t, g)
	if items = openGraphCutoverNotices(t, g, graphCutoverFailureMarker); len(items) != 0 {
		t.Fatalf("failure inbox not cleared: %v", items)
	}
}

func TestGraphCutoverStaleBlockersRaiseOneInboxItem(t *testing.T) {
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	g, _ := autoCutoverGateway(t, now)
	node := reportGraphNode(t, g, "behind", "v0.4.144", false)

	if _, err := g.store.db.Exec(`UPDATE graph_cutover_state SET upgraded_at=? WHERE id=1`, now.Add(-23*time.Hour).UnixNano()); err != nil {
		t.Fatal(err)
	}
	g.checkGraphCutover(context.Background())
	if items := openGraphCutoverNotices(t, g, graphCutoverBlockerMarker); len(items) != 0 {
		t.Fatalf("notice before 24 hours: %v", items)
	}

	if _, err := g.store.db.Exec(`UPDATE graph_cutover_state SET upgraded_at=? WHERE id=1`, now.Add(-25*time.Hour).UnixNano()); err != nil {
		t.Fatal(err)
	}
	g.checkGraphCutover(context.Background())
	items := openGraphCutoverNotices(t, g, graphCutoverBlockerMarker)
	if len(items) != 1 || !strings.Contains(items[0], "behind") {
		t.Fatalf("stale blocker notice: %v", items)
	}

	g.checkGraphCutover(context.Background())
	if items = openGraphCutoverNotices(t, g, graphCutoverBlockerMarker); len(items) != 1 {
		t.Fatalf("stale blocker notice repeated: %v", items)
	}

	if err := g.store.SetNodeGraphRefs(node, "v0.4.145", true); err != nil {
		t.Fatal(err)
	}
	if err := g.store.SetNodeHello(node, "behind", "v0.4.145"); err != nil {
		t.Fatal(err)
	}
	g.checkGraphCutover(context.Background())
	waitGraphCutoverOn(t, g)
	if items = openGraphCutoverNotices(t, g, graphCutoverBlockerMarker); len(items) != 0 {
		t.Fatalf("stale blocker notice not cleared: %v", items)
	}
}
