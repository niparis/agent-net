package tui

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
)

// fixtureNow is the moment the scrubbed fixture was copied from the live
// gateway, so ages and busy times in the frames do not move.
var fixtureNow = time.Date(2026, 11, 26, 5, 45, 0, 0, time.UTC)

// fixtureTreeSnapshot loads testdata/session_tree_0193.sql, a scrubbed copy
// of the live gateway of 2026-11-26, into a real gateway store and reads the
// session list the way the TUI gets it, so the gateway's placement rule
// (decision 0193) is what the tree draws. With migrate set, the store is
// opened once more with its last migration undone, so the part 2 migration of
// decision 0193 runs on the fixture as it does on the live gateway.
func fixtureTreeSnapshot(t *testing.T, migrate bool) Snapshot {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway.db")
	s, err := gateway.OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	fixture, err := os.ReadFile(filepath.Join("testdata", "session_tree_0193.sql"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(string(fixture)); err != nil {
		t.Fatal(err)
	}
	if migrate {
		// The tree migration of decision 0193 is the one before the stall
		// columns. OpenStore ran it on an empty database before the fixture
		// rows were inserted, so undo the two last versions and let both run
		// on the fixture. The stall migration only adds columns that exist.
		if _, err := db.Exec(`DELETE FROM schema_version WHERE version IN (SELECT version FROM schema_version ORDER BY version DESC LIMIT 2)`); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if s, err = gateway.OpenStore(path); err != nil {
			t.Fatal(err)
		}
	}
	snap := Snapshot{Connected: true, NodeID: "n_01"}
	rows, err := db.Query(`SELECT id, hostname FROM nodes ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n protocol.NodeView
		if err := rows.Scan(&n.NodeID, &n.Hostname); err != nil {
			t.Fatal(err)
		}
		n.Online, n.Version = true, "v0.0.0"
		snap.Nodes = append(snap.Nodes, n)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if snap.Agents, err = s.Agents(protocol.QueryAgents{}); err != nil {
		t.Fatal(err)
	}
	return snap
}

// TestSessionTreeFromScrubbedGateway renders the Sessions tree of the scrubbed
// live gateway at 120x34 and 80x24, in the view that shows every session. The
// owner judges the golden pictures. A session the rule cannot place shows
// "(no parent: <rule>)" under its project's lead, not as a root of its own.
func TestSessionTreeFromScrubbedGateway(t *testing.T) {
	renderFixtureTree(t, fixtureTreeSnapshot(t, false), "session_tree_0193")
}

// TestSessionTreeAfterTheTreeMigration renders the same fixture after the
// part 2 migration: the "(no parent: no lead)" marker and the old offline
// copy of a lead are gone.
func TestSessionTreeAfterTheTreeMigration(t *testing.T) {
	renderFixtureTree(t, fixtureTreeSnapshot(t, true), "session_tree_0193_migrated")
}

func renderFixtureTree(t *testing.T, snap Snapshot, name string) {
	t.Helper()
	for _, size := range []struct{ w, h int }{{120, 34}, {80, 24}} {
		src := &fakeSource{snap: snap}
		m := New(src)
		m.loc, m.now = time.UTC, func() time.Time { return fixtureNow }
		m = send(m, tea.WindowSizeMsg{Width: size.w, Height: size.h})
		m = settle(refresh(m, src))
		// The "all" view (v) shows every session the fixture holds, so the
		// dormant and offline shapes are in the picture too.
		m = keys(m, "v")
		golden(t, fmt.Sprintf("%s_%dx%d", name, size.w, size.h), m.testView())
	}
}
