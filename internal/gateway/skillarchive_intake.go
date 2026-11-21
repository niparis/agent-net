package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// intakeFolder is one skill folder a machine reported. The user folders come
// from the node.skill_sources answer, read on the machine as it is now (decision
// 0169). A machine whose daemon does not read them live gives them from its last
// inventory scan. Kind is the source kind the stored version gets, and project
// names the checkout of a project folder.
type intakeFolder struct {
	name, hash, location, kind, project, state, reason string
}

// skillArchiveIntakeMachines pulls every skill folder of every enrolled
// machine into the archive and reports what each machine did. It only reads:
// nothing is written or deleted on a machine.
func (g *Gateway) skillArchiveIntakeMachines(ctx context.Context, by string) (protocol.SkillArchiveMultiResult, error) {
	out := protocol.SkillArchiveMultiResult{Skills: []protocol.SkillArchiveAddResult{}, Machines: []protocol.SkillArchiveMachine{}, Skipped: []protocol.SkillArchiveSkipped{}}
	rows, err := g.store.db.Query(`SELECT id, hostname FROM nodes WHERE state='enrolled' ORDER BY hostname, id`)
	if err != nil {
		return out, err
	}
	type node struct{ id, host string }
	var nodes []node
	for rows.Next() {
		var n node
		if err = rows.Scan(&n.id, &n.host); err != nil {
			rows.Close()
			return out, err
		}
		nodes = append(nodes, n)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	for _, n := range nodes {
		if err = g.skillArchiveIntakeMachine(ctx, n.id, n.host, by, &out); err != nil {
			return out, err
		}
	}
	out.Shared = sharedArchiveNames(out.Skills)
	return out, nil
}

// skipGroup is one skill that the intake did not store on one machine.
type skipGroup struct {
	name, reason string
	locations    []string
}

// skillArchiveIntakeMachine reads one machine. The counts are distinct skills:
// each name and content is one found skill, and each skipped name is one row,
// so Found = Added + Existing + Skipped.
func (g *Gateway) skillArchiveIntakeMachine(ctx context.Context, id, host, by string, out *protocol.SkillArchiveMultiResult) error {
	m := protocol.SkillArchiveMachine{NodeID: id, Name: host}
	defer func() { out.Machines = append(out.Machines, m) }()
	if g.conn(id) == nil {
		m.Result = "offline"
		return nil
	}
	registered, err := g.registeredProjectEntries(id)
	if err != nil {
		return err
	}
	sources, read, userRead, note := g.sourceFolders(ctx, id, registered)
	if !userRead {
		// Without the live listing there is nothing to read: no stored scan
		// stands in for it.
		m.Result, m.Reason = "skipped", "the machine did not list its user skill folders"
		if note != "" {
			m.Reason += ": " + note
		}
		return nil
	}
	m.Result = "scanned"
	if note != "" {
		m.Reason = note
	}
	folders := sources
	m.Read = intakeReadLine(folders, registered, read, note == "")

	// A name that a machine stores from one folder is the same skill when
	// another folder of that name cannot be read, such as a link to it.
	stored := map[string]bool{}
	for _, f := range folders {
		if f.state == protocol.InventoryOK && f.hash != "" {
			stored[f.name] = true
		}
	}
	groups := map[string]*skipGroup{}
	skip := func(f intakeFolder, reason string) {
		key := f.name
		if key == "" {
			key = "\x00" + f.location
		}
		grp := groups[key]
		if grp == nil {
			grp = &skipGroup{name: f.name, reason: reason}
			groups[key] = grp
		}
		for _, l := range grp.locations {
			if l == f.location {
				return
			}
		}
		grp.locations = append(grp.locations, f.location)
	}
	seen := map[string]bool{}
	for _, f := range folders {
		if f.state != protocol.InventoryOK || f.hash == "" {
			if f.name == "" || !stored[f.name] {
				skip(f, unreadableReason(f))
			}
			continue
		}
		key := f.name + "\x00" + f.hash
		if seen[key] {
			continue
		}
		seen[key] = true
		var live int
		if err = g.store.db.QueryRow(`SELECT COUNT(*) FROM skill_archive WHERE name=? AND hash=? AND removed_at=0`, f.name, f.hash).Scan(&live); err != nil {
			return err
		}
		if live > 0 {
			m.Existing++
			continue
		}
		// A removal stays until someone adds the skill again (decision 0169).
		actor, blocked, blockErr := archiveBlock(g.store.db, f.name, f.hash)
		if blockErr != nil {
			return blockErr
		}
		if blocked {
			skip(f, "removed by "+firstNonEmpty(actor, "owner")+": add it again to bring it back")
			continue
		}
		name, hash, bundle, fetchErr := g.requestSkillBundle(ctx, id, f.name, f.location)
		if fetchErr != nil {
			skip(f, fetchErr.Error())
			continue
		}
		content, readErr := skillContentFromBundle(name, bundle)
		if readErr != nil {
			skip(f, readErr.Error())
			continue
		}
		content.Hash = hash
		added, putErr := g.skillArchivePut(content, skillArchiveSource{Kind: f.kind, Node: id, Path: f.location, Project: f.project}, by, true)
		var refused Rejection
		if errors.As(putErr, &refused) {
			skip(f, putErr.Error())
			continue
		}
		if putErr != nil {
			return putErr
		}
		if added.State == "unchanged" {
			m.Existing++
			continue
		}
		m.Added++
		out.Skills = append(out.Skills, added)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		grp := groups[k]
		sort.Strings(grp.locations)
		out.Skipped = append(out.Skipped, protocol.SkillArchiveSkipped{Name: grp.name, NodeID: id, Machine: host, Locations: grp.locations, Reason: grp.reason})
	}
	m.Skipped = len(keys)
	m.Found = m.Added + m.Existing + m.Skipped
	return nil
}

// unreadableReason says why a folder was not stored when its content was not
// read. A folder the last scan did not see is stale.
func unreadableReason(f intakeFolder) string {
	switch {
	case f.state == protocol.InventoryStale:
		return "not seen in the latest scan"
	case f.state != protocol.InventoryOK:
		return firstNonEmpty(f.reason, "unreadable")
	}
	return "no content hash"
}

// errBadBundle marks a bundle that arrived but cannot be read as a skill.
var errBadBundle = errors.New("invalid skill bundle")

// skillContentFromBundle unpacks a verified bundle into archive content.
func skillContentFromBundle(name string, bundle []byte) (skillArchiveContent, error) {
	dir, err := os.MkdirTemp("", "agent-net-archive-machine-")
	if err != nil {
		return skillArchiveContent{}, err
	}
	defer os.RemoveAll(dir)
	if inventory.UnpackSkillLimit(bundle, dir, inventory.ArchiveLimit) != nil {
		return skillArchiveContent{}, errBadBundle
	}
	content, err := skillContentFromDir(name, dir)
	if err != nil {
		return skillArchiveContent{}, fmt.Errorf("cannot read skill: %v", err)
	}
	return content, nil
}

// registeredProjectEntries lists the projects registered on one machine. The
// intake reads the skill folders of each one.
func (g *Gateway) registeredProjectEntries(id string) ([]protocol.ProjectEntry, error) {
	rows, err := g.store.db.Query(`SELECT name, path FROM projects WHERE node_id=? ORDER BY path`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.ProjectEntry{}
	for rows.Next() {
		var p protocol.ProjectEntry
		if err = rows.Scan(&p.Name, &p.Path); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// sourceFolders asks one machine for its user folders as they are now, the
// folders of its registered projects, its plugin skills and the CTO's own skill.
// read names the projects whose folders the machine read; userRead says the
// user folders came from that answer. A daemon that cannot answer is not an
// error for the intake: note tells the CTO what was not read.
func (g *Gateway) sourceFolders(ctx context.Context, id string, projects []protocol.ProjectEntry) (folders []intakeFolder, read []string, userRead bool, note string) {
	host := g.conn(id)
	if host == nil {
		return nil, nil, false, "plugin and agent-net skills not read: machine went offline"
	}
	raw, err := host.request(ctx, protocol.FrameNodeSkillSources, protocol.SkillSourcesRequest{Projects: projects}, 60*time.Second)
	var result protocol.SkillSourcesResult
	if err == nil {
		err = json.Unmarshal(raw, &result)
	}
	if err != nil {
		return nil, nil, false, "plugin and agent-net skills not read: " + err.Error()
	}
	for _, src := range result.Sources {
		f := intakeFolder{name: src.Name, hash: src.Hash, location: src.Location, state: src.State, reason: src.Reason, kind: "plugin", project: src.Project}
		switch {
		case src.Origin == "user":
			f.kind = "machine"
		case src.Project != "":
			f.kind = "project"
		case src.Origin == "agent-net":
			f.kind = "agent-net"
		}
		folders = append(folders, f)
	}
	return folders, result.ProjectsRead, result.UserRead, ""
}

// intakeReadLine says how many folders of each kind one machine answered
// with. A registered project the machine did not read is named, so a missing
// project folder is not mistaken for an empty project.
func intakeReadLine(folders []intakeFolder, registered []protocol.ProjectEntry, read []string, sourcesRead bool) string {
	distinct := map[string]map[string]bool{}
	for _, f := range folders {
		if distinct[f.kind] == nil {
			distinct[f.kind] = map[string]bool{}
		}
		distinct[f.kind][f.name+"\x00"+f.hash+"\x00"+f.location] = true
	}
	readSet := map[string]bool{}
	for _, name := range read {
		readSet[name] = true
	}
	projects := map[string]bool{}
	readProjects := 0
	for _, p := range registered {
		if !projects[p.Name] {
			projects[p.Name] = true
			if readSet[p.Name] {
				readProjects++
			}
		}
	}
	parts := []string{plural(len(distinct["machine"]), "user folder")}
	switch {
	case len(registered) == 0:
		parts = append(parts, "no registered project")
	case readProjects == 0:
		parts = append(parts, "project folders not read")
	default:
		parts = append(parts, plural(len(distinct["project"]), "project folder")+" in "+plural(readProjects, "registered project"))
	}
	if sourcesRead {
		parts = append(parts, plural(len(distinct["plugin"]), "plugin folder"), plural(len(distinct["agent-net"]), "agent-net folder"))
	}
	return strings.Join(parts, ", ")
}

// plural counts n things of one kind in plain English: "1 folder", "2 folders".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
