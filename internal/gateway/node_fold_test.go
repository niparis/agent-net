package gateway

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// An older release keeps the node side data in five tables with at most one row
// per node: node_health, node_tailscale, node_time_zones, node_silence and
// node_graph_refs. The upgrade folds them into the nodes row. inventory_reports
// is not folded and keeps its rows. These tests seed an older file, upgrade it,
// and check that the gateway reads the same values it read from the tables.

var nodeSideBase = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

// seedNodeSideTables gives three machines the rows an older release wrote. The
// first machine has a row in every table and a silent outage. The second has a
// time zone, a sleep that still goes on, and a Graph report that says it does
// not support typed Graph yet. The third reports supported Graph, but for a
// version other than the one its hello gave, so it counts as not supported.
func seedNodeSideTables(t *testing.T, s *Store) (alpha, beta, gamma string) {
	t.Helper()
	alpha, _, err := s.CreateNode("alpha-host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	beta, _, err = s.CreateNode("beta-host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	gamma, _, err = s.CreateNode("gamma-host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE nodes SET seen_at=?, version=? WHERE id=?`, nodeSideBase.UnixNano(), "v0.5.90", alpha)
	exec(`UPDATE nodes SET seen_at=?, version=? WHERE id=?`, nodeSideBase.UnixNano(), "v0.5.89", beta)
	exec(`UPDATE nodes SET seen_at=?, version=? WHERE id=?`, nodeSideBase.UnixNano(), "v0.5.90", gamma)
	exec(`INSERT INTO node_health(node_id,report,reported_at) VALUES(?,?,?)`, alpha,
		`{"restart_time":"2026-11-20T08:00:00Z","path":"/opt/agent-net/agent-net","helpers":{"git":"2.45"}}`, nodeSideBase.Unix())
	exec(`INSERT INTO node_tailscale(node_id,ip,updated_at) VALUES(?,?,?)`, alpha, "100.64.0.7", 11)
	exec(`INSERT INTO node_time_zones(node_id,zone,updated_at) VALUES(?,?,?)`, alpha, "Europe/Paris", 12)
	exec(`INSERT INTO node_time_zones(node_id,zone,updated_at) VALUES(?,?,?)`, beta, "UTC", 13)
	exec(`INSERT INTO node_silence(node_id,dropped_at,asleep_from,asleep_to,silent_at) VALUES(?,?,0,0,?)`,
		alpha, nodeSideBase.Add(-30*time.Minute).UnixNano(), nodeSideBase.Add(-10*time.Minute).UnixNano())
	exec(`INSERT INTO node_silence(node_id,dropped_at,asleep_from,asleep_to,silent_at) VALUES(?,?,?,0,0)`,
		beta, nodeSideBase.Add(-5*time.Minute).UnixNano(), nodeSideBase.Add(-6*time.Minute).UnixNano())
	exec(`INSERT INTO node_graph_refs(node_id,version,supported) VALUES(?,?,1)`, alpha, "v0.5.90")
	exec(`INSERT INTO node_graph_refs(node_id,version,supported) VALUES(?,?,0)`, beta, "v0.5.89")
	exec(`INSERT INTO node_graph_refs(node_id,version,supported) VALUES(?,?,1)`, gamma, "v0.5.88")
	exec(`INSERT INTO inventory_reports(node_id,report) VALUES(?,?)`, alpha,
		`{"state":"ok","items":[{"kind":"skill","name":"demo","hash":"abc","harness":"claude","scope":"user","location":"/home/u/.claude/skills/demo","source":"user","state":"ok"}]}`)
	return alpha, beta, gamma
}

// upgradeSeeded opens an older file, seeds it, and runs the migrations the
// older file did not have. It returns the store, the three machine IDs and the
// migration's version.
func upgradeSeeded(t *testing.T) (*Store, string, string, string, int) {
	t.Helper()
	idx := migrationVersion(t, "node side tables folded into nodes")
	s := migrateOnly(t, filepath.Join(t.TempDir(), "old.db"), idx-1)
	t.Cleanup(func() { s.Close() })
	alpha, beta, gamma := seedNodeSideTables(t, s)
	if err := replayMigrations(t, s); err != nil {
		t.Fatal(err)
	}
	return s, alpha, beta, gamma, idx
}

// nodeSideRow is what the nodes row holds for the folded data.
type nodeSideRow struct {
	Health         string
	HealthAt       int64
	TailscaleIP    string
	TailscaleAt    int64
	TimeZone       string
	TimeZoneAt     int64
	SilenceDropped int64
	SilenceFrom    int64
	SilenceTo      int64
	SilenceSilent  int64
	GraphRefsOn    int
}

func readNodeSideColumns(t *testing.T, s *Store, node string) nodeSideRow {
	t.Helper()
	var c nodeSideRow
	err := s.db.QueryRow(`SELECT health_report, health_reported_at, tailscale_ip, tailscale_updated_at,
		time_zone, time_zone_updated_at, silence_dropped_at, silence_asleep_from, silence_asleep_to, silence_silent_at,
		graph_refs_supported FROM nodes WHERE id=?`, node).Scan(
		&c.Health, &c.HealthAt, &c.TailscaleIP, &c.TailscaleAt, &c.TimeZone, &c.TimeZoneAt,
		&c.SilenceDropped, &c.SilenceFrom, &c.SilenceTo, &c.SilenceSilent, &c.GraphRefsOn)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// assertNodeSideValues checks the values the gateway reads after the upgrade,
// through the same functions the gateway answers with.
func assertNodeSideValues(t *testing.T, s *Store, alpha, beta string) {
	t.Helper()
	dropped := nodeSideBase.Add(-30 * time.Minute).Unix()
	nodes, err := s.Nodes()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]protocol.NodeView{}
	for _, n := range nodes {
		byID[n.NodeID] = n
	}
	a, b := byID[alpha], byID[beta]
	if a.TimeZone != "Europe/Paris" || a.TailscaleIP != "100.64.0.7" || a.DownState != "silent" || a.DownSince != dropped {
		t.Fatalf("alpha machine list: zone %q ip %q state %q since %d", a.TimeZone, a.TailscaleIP, a.DownState, a.DownSince)
	}
	if b.TimeZone != "UTC" || b.TailscaleIP != "" || b.DownState != "asleep" || b.DownSince != nodeSideBase.Add(-6*time.Minute).Unix() {
		t.Fatalf("beta machine list: zone %q ip %q state %q since %d", b.TimeZone, b.TailscaleIP, b.DownState, b.DownSince)
	}

	h, err := s.nodeHealth(alpha, false)
	if err != nil {
		t.Fatal(err)
	}
	if h.Status != "reported" || h.ReportedAt != "2026-11-20T10:00:00Z" || h.Path != "/opt/agent-net/agent-net" || h.Helpers["git"] != "2.45" {
		t.Fatalf("alpha health: %+v", h)
	}
	if h.UptimeSeconds == nil || *h.UptimeSeconds != 7200 {
		t.Fatalf("alpha uptime: %v, want 7200 seconds from 08:00 to 10:00", h.UptimeSeconds)
	}
	if h, err = s.nodeHealth(beta, false); err != nil || h.Status != "not reported" {
		t.Fatalf("beta health: %+v %v", h, err)
	}

	g := &Gateway{store: s}
	blockers, err := g.graphRefsBlockers(context.Background(), s.db, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"upgrade required: machine beta-host has not reported typed Graph support", "upgrade required: machine gamma-host has not reported typed Graph support"}; !reflect.DeepEqual(blockers, want) {
		t.Fatalf("graph blockers = %q, want %q", blockers, want)
	}

	reports, err := s.Inventories()
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || reports[0].NodeID != alpha || len(reports[0].Items) != 1 || reports[0].Items[0].Name != "demo" {
		t.Fatalf("inventories = %+v, want one report of alpha with the demo skill", reports)
	}
}

func TestNodeSideTablesFoldIntoTheNodesRowOnUpgrade(t *testing.T) {
	s, alpha, beta, gamma, _ := upgradeSeeded(t)

	var left int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table'
		AND name IN ('node_health','node_tailscale','node_time_zones','node_silence','node_graph_refs')`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("%d of the five side tables are still there", left)
	}
	// The inventory is read from inventory_reports, checked in assertNodeSideValues.
	var inventoryColumn int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('nodes') WHERE name='inventory_report'`).Scan(&inventoryColumn); err != nil {
		t.Fatal(err)
	}
	if inventoryColumn != 0 {
		t.Fatal("nodes has an inventory column; the inventory report must stay in its own table")
	}

	// Every value of the old rows is on the nodes row, and a node without a
	// row there reads the empty value the old tables read as no row.
	wantAlpha := nodeSideRow{
		Health: `{"restart_time":"2026-11-20T08:00:00Z","path":"/opt/agent-net/agent-net","helpers":{"git":"2.45"}}`, HealthAt: nodeSideBase.Unix(),
		TailscaleIP: "100.64.0.7", TailscaleAt: 11,
		TimeZone: "Europe/Paris", TimeZoneAt: 12,
		SilenceDropped: nodeSideBase.Add(-30 * time.Minute).UnixNano(), SilenceSilent: nodeSideBase.Add(-10 * time.Minute).UnixNano(),
		GraphRefsOn: 1,
	}
	if got := readNodeSideColumns(t, s, alpha); !reflect.DeepEqual(got, wantAlpha) {
		t.Fatalf("alpha nodes row = %+v\nwant %+v", got, wantAlpha)
	}
	wantBeta := nodeSideRow{
		TimeZone: "UTC", TimeZoneAt: 13,
		SilenceDropped: nodeSideBase.Add(-5 * time.Minute).UnixNano(), SilenceFrom: nodeSideBase.Add(-6 * time.Minute).UnixNano(),
	}
	if got := readNodeSideColumns(t, s, beta); !reflect.DeepEqual(got, wantBeta) {
		t.Fatalf("beta nodes row = %+v\nwant %+v", got, wantBeta)
	}
	// A Graph version that differs from the hello version folds to not supported.
	if got := readNodeSideColumns(t, s, gamma); !reflect.DeepEqual(got, nodeSideRow{}) {
		t.Fatalf("gamma nodes row = %+v, want the empty row", got)
	}
	assertNodeSideValues(t, s, alpha, beta)
}

// A rewind replays the migration after its tables are gone. The replay must
// neither fail nor write the defaults over the values the first run folded in.
func TestNodeSideFoldReplaysAfterARewind(t *testing.T) {
	s, alpha, beta, _, idx := upgradeSeeded(t)
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version >= ?`, idx); err != nil {
		t.Fatal(err)
	}
	if err := replayMigrations(t, s); err != nil {
		t.Fatalf("replaying the fold: %v", err)
	}
	assertNodeSideValues(t, s, alpha, beta)
}

// After the upgrade the writes keep the meaning they had with the tables. A
// cleared report reads as not reported, and a Tailscale address that does not
// parse clears the stored one.
func TestNodeSideWritesAfterTheUpgradeClearTheirValues(t *testing.T) {
	s, alpha, _, _, _ := upgradeSeeded(t)
	if err := s.SetNodeHealth(alpha, nil); err != nil {
		t.Fatal(err)
	}
	if h, err := s.nodeHealth(alpha, false); err != nil || h.Status != "not reported" {
		t.Fatalf("health after clearing: %+v %v", h, err)
	}
	if err := s.SetNodeTailscaleIP(alpha, "not an address"); err != nil {
		t.Fatal(err)
	}
	nodes, err := s.Nodes()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.NodeID == alpha && n.TailscaleIP != "" {
			t.Fatalf("tailscale address after a bad one: %q", n.TailscaleIP)
		}
	}
}
