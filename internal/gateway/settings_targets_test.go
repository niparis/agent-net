package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func targetNamesakes(t *testing.T) (*Store, string) {
	t.Helper()
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("fixture", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO repos(id,display_name,created_at,updated_at) VALUES('shop','shop',0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES('agent-shop','shop','',0,0,0)`); err != nil {
		t.Fatal(err)
	}
	return s, node
}

func TestSettingsTargetRepoAndAgentNamesakes(t *testing.T) {
	s, _ := targetNamesakes(t)
	repo, err := s.ResolveSettingsTarget(protocol.SettingsTarget{Level: "repo", Name: "shop"})
	if err != nil || repo.Target == nil {
		t.Fatalf("repo: %+v %v", repo, err)
	}
	agent, err := s.ResolveSettingsTarget(protocol.SettingsTarget{Level: "agent", Name: "shop"})
	if err != nil || agent.Target == nil {
		t.Fatalf("agent: %+v %v", agent, err)
	}
	if repo.Target.Kind != "repo" || repo.Target.ID != "shop" || agent.Target.Kind != "agent" || agent.Target.ID != "agent-shop" {
		t.Fatalf("namesakes conflated: %+v / %+v", repo.Target, agent.Target)
	}
	if repo.Target.Token == "" || agent.Target.Token == "" || repo.Target.Token == agent.Target.Token {
		t.Fatalf("tokens: %+v / %+v", repo.Target, agent.Target)
	}
	// The same ID in another kind is a different target with its own token.
	if network, err := s.ResolveSettingsTarget(protocol.SettingsTarget{Level: "network"}); err != nil || network.Target == nil || network.Target.Kind != "network" || network.Target.Token == "" {
		t.Fatalf("network: %+v %v", network, err)
	}
}

func TestSettingsTargetAmbiguousAndUnknown(t *testing.T) {
	s, _ := targetNamesakes(t)
	if _, err := s.db.Exec(`INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES('shop-two','shop','',0,0,0)`); err != nil {
		t.Fatal(err)
	}
	resolution, err := s.ResolveSettingsTarget(protocol.SettingsTarget{Level: "agent", Name: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Target != nil || len(resolution.Choices) != 2 {
		t.Fatalf("ambiguous resolution: %+v", resolution)
	}
	for _, c := range resolution.Choices {
		if c.Ref.Kind != "agent" || c.Name != "shop" || c.Ref.ID == "" {
			t.Fatalf("choice: %+v", c)
		}
	}
	if _, err = s.ResolveSettingsTarget(protocol.SettingsTarget{Level: "repo", Name: "missing"}); err == nil || !strings.Contains(err.Error(), "unknown repo") {
		t.Fatalf("unknown repo: %v", err)
	}
	if _, err = s.ResolveSettingsTarget(protocol.SettingsTarget{Level: "agent", Name: ""}); err == nil {
		t.Fatal("agent without a name accepted")
	}
	if _, err = s.ResolveSettingsTarget(protocol.SettingsTarget{Level: "network", Name: "shop"}); err == nil {
		t.Fatal("network with a name accepted")
	}
	if _, err = s.ResolveSettingsTarget(protocol.SettingsTarget{Level: "team", Name: "shop"}); err == nil {
		t.Fatal("unknown level accepted")
	}
}

func TestSettingsTargetTokensSurviveRenameAndChangeOnRecreate(t *testing.T) {
	s, _ := targetNamesakes(t)
	first, err := s.ResolveSettingsTarget(protocol.SettingsTarget{Level: "repo", Name: "shop"})
	if err != nil || first.Target == nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE repos SET display_name='store' WHERE id='shop'`); err != nil {
		t.Fatal(err)
	}
	renamed, err := s.ResolveSettingsTarget(protocol.SettingsTarget{Level: "repo", Name: "store"})
	if err != nil || renamed.Target == nil || renamed.Target.Token != first.Target.Token {
		t.Fatalf("rename changed token: %+v %v", renamed, err)
	}
	if _, err = s.ResolveSettingsTarget(protocol.SettingsTarget{Level: "repo", Name: "shop"}); err == nil {
		t.Fatal("old name still resolves after rename")
	}
	if _, err = s.db.Exec(`DELETE FROM repos WHERE id='shop'`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO repos(id,display_name,created_at,updated_at) VALUES('shop','store',0,0)`); err != nil {
		t.Fatal(err)
	}
	recreated, err := s.ResolveSettingsTarget(protocol.SettingsTarget{Level: "repo", Name: "store"})
	if err != nil || recreated.Target == nil || recreated.Target.Token == first.Target.Token {
		t.Fatalf("recreate kept token: %+v %v", recreated, err)
	}

	agent, err := s.ResolveSettingsTarget(protocol.SettingsTarget{Level: "agent", Name: "shop"})
	if err != nil || agent.Target == nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE agents SET tombstone=1 WHERE id='agent-shop'`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveSettingsTarget(protocol.SettingsTarget{Level: "agent", Name: "shop"}); err == nil {
		t.Fatal("tombstoned agent resolved")
	}
	if _, err = s.db.Exec(`UPDATE agents SET tombstone=0 WHERE id='agent-shop'`); err != nil {
		t.Fatal(err)
	}
	restored, err := s.ResolveSettingsTarget(protocol.SettingsTarget{Level: "agent", Name: "shop"})
	if err != nil || restored.Target == nil || restored.Target.Token == agent.Target.Token {
		t.Fatalf("agent recreate kept token: %+v %v", restored, err)
	}
}

func TestSettingsTargetSeedsExistingRecords(t *testing.T) {
	s, _ := targetNamesakes(t)
	// The migration seeds records that exist when it runs. Rewind and replay it
	// now that the records are present, as an upgraded database would.
	version := migrationVersion(t, "Immutable settings target identities")
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, version); err != nil {
		t.Fatal(err)
	}
	if err := migrateOldFixture(t, s); err != nil {
		t.Fatal(err)
	}
	repos, agents, err := s.RepoAgents()
	if err != nil {
		t.Fatal(err)
	}
	var network int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM settings_target_identity WHERE kind='network' AND id='network' AND token<>''`).Scan(&network); err != nil || network != 1 {
		t.Fatalf("network seed: %d %v", network, err)
	}
	for _, r := range repos {
		var n int
		if err = s.db.QueryRow(`SELECT COUNT(*) FROM settings_target_identity WHERE kind='repo' AND id=? AND token<>''`, r.ID).Scan(&n); err != nil || n != 1 {
			t.Fatalf("repo seed %s: %d %v", r.ID, n, err)
		}
	}
	for _, a := range agents {
		var n int
		if err = s.db.QueryRow(`SELECT COUNT(*) FROM settings_target_identity WHERE kind='agent' AND id=? AND token<>''`, a.ID).Scan(&n); err != nil || n != 1 {
			t.Fatalf("agent seed %s: %d %v", a.ID, n, err)
		}
	}
}

func TestSettingsTargetMigrationReplayKeepsTokens(t *testing.T) {
	s, _ := targetNamesakes(t)
	if got := migrationVersion(t, "Immutable settings target identities"); got != 125 {
		t.Fatalf("settings target identity migration is %d, want 125", got)
	}
	first, err := s.ResolveSettingsTarget(protocol.SettingsTarget{Level: "repo", Name: "shop"})
	if err != nil || first.Target == nil {
		t.Fatal(err)
	}
	version := migrationVersion(t, "Immutable settings target identities")
	if _, err = s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, version); err != nil {
		t.Fatal(err)
	}
	if err = migrateOldFixture(t, s); err != nil {
		t.Fatal(err)
	}
	again, err := s.ResolveSettingsTarget(protocol.SettingsTarget{Level: "repo", Name: "shop"})
	if err != nil || again.Target == nil || again.Target.Token != first.Target.Token {
		t.Fatalf("replay changed token: %+v %v", again, err)
	}
}
