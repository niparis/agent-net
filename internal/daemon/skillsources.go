package daemon

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// skillSources lists the skill folders as they are on disk now: the user-level
// folders, Claude Code plugin skills (read only, they
// stay owned by the plugin), the CTO's own skill that this daemon wrote, and the
// skill folders of each project the gateway names. read lists the projects whose
// folders were read. Nothing is written or changed. The scans run without a
// lock; only the served-folder set is swapped under it.
func (d *Daemon) skillSources(projects []protocol.ProjectEntry) ([]protocol.SkillSource, []string) {
	home := d.cfg.InventoryHome
	var out []protocol.SkillSource
	var read []string
	add := func(root inventory.SkillRoot, origin string) {
		for _, it := range inventory.ScanSkillRoot(home, root).Items {
			if it.Kind != protocol.InventorySkill {
				continue
			}
			out = append(out, protocol.SkillSource{Name: it.Name, Hash: it.Hash, Location: it.Location, Origin: origin, Project: root.Project, State: it.State, Reason: it.Reason})
		}
	}
	// Codex and the agents keep built-in skills in .system folders. They ship
	// with the harness and are not archive material, so they are not listed.
	userRoots := inventory.UserSkillRoots(home, d.cfg.CodexHome)
	scanned := map[string]bool{}
	for _, root := range userRoots {
		// The harnesses share folders, so one folder is read once.
		if !scanned[root.Path] {
			scanned[root.Path] = true
			add(root, "user")
		}
	}
	for _, root := range inventory.PluginSkillRoots(home) {
		add(inventory.SkillRoot{Harness: "claude", Base: home, Path: root}, "plugin "+inventory.PluginName(root))
	}
	for _, p := range projects {
		for _, root := range inventory.ProjectSkillRoots(p.Name, p.Path) {
			add(root, "project "+p.Name)
		}
		read = append(read, p.Name)
	}
	locations := map[string]bool{}
	for _, s := range out {
		if s.State == protocol.InventoryOK {
			locations[s.Location] = true
		}
	}
	d.sourceMu.Lock()
	d.sourceLocations = locations
	d.sourceMu.Unlock()
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
		data = protocol.SkillSourcesResult{Sources: sources, ProjectsRead: read, UserRead: true}
	} else {
		err = errors.New("cannot read skill sources")
	}
	if b, e := protocol.EncodeResult(h.ID, data, err); e == nil {
		_ = l.write(ctx, b)
	}
}
