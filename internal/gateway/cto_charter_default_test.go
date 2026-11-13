package gateway

import (
	"database/sql"
	"errors"
	"testing"
)

// ctoCharter reads the agent-level charter of the cto agent node.
func ctoCharter(t *testing.T, s *Store) map[string]string {
	t.Helper()
	rows, err := s.db.Query(`SELECT attr,value FROM agent_charter WHERE agent_id='cto'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var attr, value string
		if err := rows.Scan(&attr, &value); err != nil {
			t.Fatal(err)
		}
		got[attr] = value
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return got
}

// rerunCTOCharterSeed rewinds the store to before the seed migration and
// migrates it again. fresh also clears the seed marker, as a gateway from
// before this release has none.
func rerunCTOCharterSeed(t *testing.T, s *Store, fresh bool) {
	t.Helper()
	if fresh {
		if _, err := s.db.Exec(`DELETE FROM owner_settings WHERE key=?`, ctoDefaultCharterKey); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "CTO default charter seed")); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
}

func wantCTODefaults(t *testing.T, got map[string]string, skip string) {
	t.Helper()
	for _, f := range ctoDefaultCharter {
		if f.attr != skip && got[f.attr] != f.value {
			t.Errorf("cto %s = %q, want the default %q", f.attr, got[f.attr], f.value)
		}
	}
}

// A new install starts with the CTO's charter. The owner's words, relayed by
// the CTO on 2026-11-13: "your charter is good and needs to ship with
// agent-net".
func TestCTODefaultCharterOnNewInstall(t *testing.T) {
	s, _ := newTestStore(t)
	got := ctoCharter(t, s)
	if len(got) != len(ctoDefaultCharter) {
		t.Fatalf("cto charter = %v, want exactly the %d default fields", got, len(ctoDefaultCharter))
	}
	wantCTODefaults(t, got, "")
}

// An existing gateway on upgrade gets the default only in empty fields: a
// value the owner or the CTO set stays.
func TestCTODefaultCharterKeepsOwnValues(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.db.Exec(`DELETE FROM agent_charter WHERE agent_id='cto'`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAgentCharter("cto", "accepts", "Owner's own accepts", "owner"); err != nil {
		t.Fatal(err)
	}
	rerunCTOCharterSeed(t, s, true)
	got := ctoCharter(t, s)
	if got["accepts"] != "Owner's own accepts" {
		t.Fatalf("cto accepts = %q, want the owner's value kept", got["accepts"])
	}
	wantCTODefaults(t, got, "accepts")
	var actor string
	if err := s.db.QueryRow(`SELECT updated_by FROM agent_charter WHERE agent_id='cto' AND attr='rules'`).Scan(&actor); err != nil || actor != ctoDefaultCharterActor {
		t.Fatalf("rules updated_by = %q, %v; want %q", actor, err, ctoDefaultCharterActor)
	}
}

// The seed runs once. A field the owner clears after it stays empty when the
// migrations run again.
func TestCTODefaultCharterRunsOnce(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.SetAgentCharter("cto", "rules", "", "owner"); err != nil {
		t.Fatal(err)
	}
	rerunCTOCharterSeed(t, s, false)
	var rules string
	if err := s.db.QueryRow(`SELECT value FROM agent_charter WHERE agent_id='cto' AND attr='rules'`).Scan(&rules); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cleared rules came back as %q (%v)", rules, err)
	}
}
