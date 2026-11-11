package gateway

import (
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

func TestCompactionCacheWindowMigration(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.db.Exec(`INSERT INTO setting_values(key,level,scope,item,value,revision) VALUES('sessions.idle_compaction_after','network','network','','"45m"',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES('cto_settings','{"compaction":{"cache_window_seconds":{"anthropic":600}}}',0) ON CONFLICT(key) DO UPDATE SET value=excluded.value`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.cutoverSessionCompactionTx(tx); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	snap, err := s.SettingsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	windows := snap.Map("sessions.cache_window_seconds", settings.Where{})
	if windows["default"] != 45*60 || windows["anthropic"] != 600 {
		t.Fatalf("migrated windows=%v", windows)
	}
}

func TestCompactionThresholdTeamAndAgentLevels(t *testing.T) {
	s, _ := newTestStore(t)
	for _, q := range []string{
		`INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES('shop-lead','shop-lead','',0,0,0)`,
		`INSERT INTO repos(id,display_name,created_at,updated_at,owner_agent_id) VALUES('shop','shop',0,0,'shop-lead')`,
		`INSERT INTO teams(id,name,created_at,updated_at) VALUES('forge','forge',0,0)`,
		`INSERT INTO team_members(member_kind,member_id,team_id) VALUES('repo','shop','forge')`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	value := func(v string) json.RawMessage { return json.RawMessage(v) }
	if _, err := s.WriteSettings([]protocol.SettingsChange{
		{Key: "sessions.compaction_threshold_percent", Level: "network", Scope: "network", Value: value("80")},
		{Key: "sessions.compaction_threshold_percent", Level: "team", Scope: "forge", Value: value("60")},
		{Key: "sessions.compaction_threshold_percent", Level: "agent", Scope: "shop-lead", Value: value("50")},
	}, "owner", "compaction policy"); err != nil {
		t.Fatal(err)
	}
	snap, err := s.SettingsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got := snap.Int("sessions.compaction_threshold_percent", settings.Where{Repo: "shop"}); got != 50 {
		t.Fatalf("repo threshold=%d, want the agent value 50", got)
	}
	if got := snap.Int("sessions.compaction_threshold_percent", settings.Where{Repo: "shop", Agent: "other"}); got != 60 {
		t.Fatalf("team threshold=%d, want 60", got)
	}
}

func TestCTOCompactionThresholdMigration(t *testing.T) {
	s, _ := newTestStore(t)
	snap, err := s.SettingsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got := snap.Int("sessions.compaction_threshold_percent", settings.Where{Agent: "cto"}); got != 50 {
		t.Fatalf("CTO threshold=%d, want 50", got)
	}
	if got := snap.Int("sessions.compaction_threshold_percent", settings.Where{}); got != 80 {
		t.Fatalf("network threshold=%d, want 80", got)
	}
	if got := snap.Map("sessions.cache_window_seconds", settings.Where{})["default"]; got != 300 {
		t.Fatalf("cache window=%d, want 300", got)
	}
}

func TestSettingsLastCompactedSessionNote(t *testing.T) {
	s, _ := newTestStore(t)
	n, _, err := s.CreateNode("test", "linux")
	if err != nil {
		t.Fatal(err)
	}
	in := protocol.SessionInfo{Harness: "claude", NativeID: "test", Status: "idle", LastCompactedAt: 1700000000, LastCompactedReason: "fill 82%"}
	id, err := s.UpsertSession(n, in)
	if err != nil {
		t.Fatal(err)
	}
	a, ok, err := s.Agent(id)
	if err != nil || !ok || a.LastCompactedAt != in.LastCompactedAt || a.LastCompactedReason != in.LastCompactedReason {
		t.Fatalf("note round trip: %+v %v", a, err)
	}
	in.LastCompactedAt, in.LastCompactedReason = 0, ""
	in.Status = "busy"
	if _, err := s.UpsertSession(n, in); err != nil {
		t.Fatal(err)
	}
	a, _, err = s.Agent(id)
	if err != nil || a.LastCompactedAt != 0 || a.LastCompactedReason != "" {
		t.Fatalf("clear note: %+v %v", a, err)
	}
}
