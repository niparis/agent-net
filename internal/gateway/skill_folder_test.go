package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// The launch read (decision 0169, task 4): one target resolved along its node
// path, with the stored bundle of each version, so the daemon builds a folder
// without a second call per skill.
func TestSkillFolderResolvesSessionAgentWithBundles(t *testing.T) {
	s, _ := newTestStore(t)
	seedRepo(t, s, "alpha", "growing")
	seedArchiveSkill(t, s, "alpha", 1, 10, "Alpha skill.", "alpha body")
	seedArchiveSkill(t, s, "beta", 1, 20, "Beta skill.", "beta body")
	if _, err := s.db.Exec(`INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES('ag','Ag','',0,0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE repos SET owner_agent_id='ag' WHERE id='alpha'`); err != nil {
		t.Fatal(err)
	}
	setSkills(t, s, "global", "", []string{"beta"}, nil, nil)
	setSkills(t, s, "agent", "ag", []string{"alpha"}, nil, nil)
	if _, err := s.db.Exec(`INSERT INTO sessions(id,node_id,harness,native_id,repo_project,agent_id,last_seen) VALUES('s1','n','claude','x1','alpha','ag',0)`); err != nil {
		t.Fatal(err)
	}

	folder, err := s.SkillFolder(protocol.SkillFolderRequest{Session: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if folder.Level != "agent" || folder.Scope != "ag" {
		t.Fatalf("node: %+v", folder)
	}
	if len(folder.Skills) != 2 || folder.Skills[0].Name != "alpha" || folder.Skills[1].Name != "beta" {
		t.Fatalf("skills: %+v", folder.Skills)
	}
	for _, skill := range folder.Skills {
		version, err := s.SkillVersion(skill.Name, skill.Version)
		if err != nil {
			t.Fatalf("%s: bundle: %v", skill.Name, err)
		}
		dir := t.TempDir()
		if err := inventory.UnpackSkill(version.Data, dir); err != nil {
			t.Fatalf("%s: unpack: %v", skill.Name, err)
		}
		hash, err := inventory.SkillHash(dir)
		if err != nil || hash != skill.Hash || hash != version.Hash {
			t.Fatalf("%s: hash %q, set %q, bundle %q: %v", skill.Name, hash, skill.Hash, version.Hash, err)
		}
		if skill.Version != 1 {
			t.Errorf("%s: version %d", skill.Name, skill.Version)
		}
	}
}

// A session without an Agent takes the skills of its project's repo.
func TestSkillFolderSessionFallsBackToRepo(t *testing.T) {
	s, _ := newTestStore(t)
	seedRepo(t, s, "alpha", "growing")
	seedArchiveSkill(t, s, "alpha", 1, 10, "Alpha skill.", "alpha body")
	setSkills(t, s, "repo", "alpha", []string{"alpha"}, nil, nil)
	if _, err := s.db.Exec(`INSERT INTO sessions(id,node_id,harness,native_id,repo_project,last_seen) VALUES('s2','n','claude','x2','alpha',0)`); err != nil {
		t.Fatal(err)
	}

	folder, err := s.SkillFolder(protocol.SkillFolderRequest{Session: "s2"})
	if err != nil {
		t.Fatal(err)
	}
	if folder.Level != "repo" || folder.Scope != "alpha" || len(folder.Skills) != 1 || folder.Skills[0].Name != "alpha" {
		t.Fatalf("folder: %+v", folder)
	}
}

// A repo target with an empty effective list answers with no skill and no
// error: the daemon then passes no folder at all.
func TestSkillFolderEmptyList(t *testing.T) {
	s, _ := newTestStore(t)
	seedRepo(t, s, "alpha", "growing")

	folder, err := s.SkillFolder(protocol.SkillFolderRequest{Repo: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if folder.Level != "repo" || folder.Scope != "alpha" || len(folder.Skills) != 0 {
		t.Fatalf("folder: %+v", folder)
	}
}
