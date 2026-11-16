package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// sourceKey names one listed source folder. node.skill_bundle serves only
// folders the last node.skill_sources answer listed, with the hash it gave.
func sourceKey(name, hash, location string) string { return name + "\x00" + hash + "\x00" + location }

// skillSources lists the skill folders the inventory scan does not: Claude Code
// plugin skills (read only, they stay owned by the plugin), the CTO's own skill
// that this daemon wrote, and the skill folders of each project the gateway
// names. read lists the projects whose folders were read. Nothing is written or
// changed. The scans run without a lock; only the served-folder set is swapped
// under it.
func (d *Daemon) skillSources(projects []protocol.ProjectEntry) ([]protocol.SkillSource, []string) {
	home := d.cfg.InventoryHome
	var out []protocol.SkillSource
	var read []string
	add := func(root inventory.SkillRoot, origin string) {
		for _, it := range inventory.Scan(inventory.Options{Home: home, OnlyRoot: &root}).Items {
			if it.Kind != protocol.InventorySkill {
				continue
			}
			out = append(out, protocol.SkillSource{Name: it.Name, Hash: it.Hash, Location: it.Location, Origin: origin, Project: root.Project, State: it.State, Reason: it.Reason})
		}
	}
	for _, root := range inventory.PluginSkillRoots(home) {
		add(inventory.SkillRoot{Harness: "claude", Base: home, Path: root}, "plugin "+inventory.PluginName(root))
	}
	for _, harness := range []string{"claude", "opencode"} {
		if dir, ok := ctoSkillDir(d.cfg.StateDir, harness); ok {
			if _, err := os.Lstat(dir); err == nil {
				add(inventory.SkillRoot{Harness: harness, Base: d.cfg.StateDir, Path: filepath.Dir(dir)}, "agent-net")
			}
		}
	}
	for _, p := range projects {
		for _, root := range inventory.ProjectSkillRoots(p.Name, p.Path) {
			add(root, "project "+p.Name)
		}
		read = append(read, p.Name)
	}
	served := map[string]bool{}
	for _, s := range out {
		if s.State == protocol.InventoryOK && s.Hash != "" {
			served[sourceKey(s.Name, s.Hash, s.Location)] = true
		}
	}
	d.toolsMu.Lock()
	d.sourceSkills = served
	d.toolsMu.Unlock()
	return out, read
}

func (d *Daemon) onSkillSources(ctx context.Context, l *link, h protocol.Header, raw []byte) {
	var req protocol.SkillSourcesRequest
	err := json.Unmarshal(raw, &req)
	var data any
	if err == nil {
		sources, read := d.skillSources(req.Projects)
		if sources == nil {
			sources = []protocol.SkillSource{}
		}
		data = protocol.SkillSourcesResult{Sources: sources, ProjectsRead: read}
	} else {
		err = errors.New("cannot read skill sources")
	}
	if b, e := protocol.EncodeResult(h.ID, data, err); e == nil {
		_ = l.write(ctx, b)
	}
}
