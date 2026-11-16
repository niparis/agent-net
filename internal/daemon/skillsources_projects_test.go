package daemon

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The gateway sends the registered projects of the machine. The daemon lists
// the skill folders of each one, serves their bundles, and says which projects
// it read, so an empty project is not mistaken for a daemon that does not read
// projects.
func TestSkillSourcesReadsRegisteredProjectFolders(t *testing.T) {
	d, _ := promptTestDaemon(t)
	project := t.TempDir()
	alpha := filepath.Join(project, ".claude/skills/alpha")
	beta := filepath.Join(project, ".agents/skills/beta")
	writeSourceSkill(t, alpha, "---\ndescription: Alpha.\n---\nbody\n")
	writeSourceSkill(t, beta, "---\ndescription: Beta.\n---\nbody\n")
	bare := t.TempDir()
	sources, read := d.skillSources([]protocol.ProjectEntry{{Name: "demo", Path: project}, {Name: "bare", Path: bare}})
	got := map[string]protocol.SkillSource{}
	for _, s := range sources {
		got[s.Location] = s
	}
	for location, name := range map[string]string{alpha: "alpha", beta: "beta"} {
		s, ok := got[location]
		if !ok || s.Name != name || s.Origin != "project demo" || s.Project != "demo" || s.State != protocol.InventoryOK || s.Hash == "" {
			t.Fatalf("source %s = %+v (listed %v)", location, s, ok)
		}
		if _, err := d.skillBundleFor(protocol.SkillBundleRequest{Name: s.Name, Hash: s.Hash, Location: location}); err != nil {
			t.Fatalf("bundle %s: %v", location, err)
		}
	}
	if want := []string{"demo", "bare"}; !slices.Equal(read, want) {
		t.Fatalf("projects read = %v, want %v", read, want)
	}
}
