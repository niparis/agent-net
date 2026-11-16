package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// ctoSkillArchive runs the skill archive actions (decision 0169). The gateway
// holds the rows, so the daemon only routes and checks the arguments.
func (d *Daemon) ctoSkillArchive(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	actor := proposalActor(ctx)
	if actor == "" {
		actor = "cto"
	}
	switch name {
	case "skill_archive_list":
		return d.call(ctx, protocol.FrameSkillArchiveList, protocol.SkillArchiveList{By: actor})
	case "skill_archive_read":
		var p protocol.SkillArchiveVersion
		if json.Unmarshal(raw, &p) != nil || p.Name == "" {
			return nil, errors.New("skill_archive read needs a name")
		}
		p.By = actor
		return d.call(ctx, protocol.FrameSkillArchiveRead, p)
	case "skill_archive_add":
		var p struct {
			NodeID string `json:"node_id"`
			Folder string `json:"folder"`
		}
		if json.Unmarshal(raw, &p) != nil || p.NodeID == "" || p.Folder == "" {
			return nil, errors.New("skill_archive add needs node_id and folder")
		}
		return d.call(ctx, protocol.FrameSkillArchiveAdd, protocol.SkillArchiveAdd{
			Location: protocol.ToolImportLocation{NodeID: p.NodeID, Location: p.Folder}, By: actor,
		})
	case "skill_archive_add_git":
		var p struct {
			URL  string `json:"url"`
			Path string `json:"path"`
		}
		if json.Unmarshal(raw, &p) != nil || p.URL == "" {
			return nil, errors.New("skill_archive add_git needs a url")
		}
		return d.call(ctx, protocol.FrameSkillArchiveAddGit, protocol.SkillArchiveAddGit{URL: p.URL, Path: p.Path, By: actor})
	case "skill_archive_remove":
		var p struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &p) != nil || p.Name == "" {
			return nil, errors.New("skill_archive remove needs a name")
		}
		return d.call(ctx, protocol.FrameSkillArchiveRemove, protocol.SkillArchiveRemove{Name: p.Name, By: actor})
	case "skill_archive_undo":
		var p struct {
			Change string `json:"change"`
		}
		if json.Unmarshal(raw, &p) != nil || p.Change == "" {
			return nil, errors.New("skill_archive undo needs a change")
		}
		return d.call(ctx, protocol.FrameSkillArchiveUndo, protocol.SkillArchiveUndo{Change: p.Change, By: actor})
	case "skill_archive_intake":
		return d.call(ctx, protocol.FrameSkillArchiveIntake, protocol.SkillArchiveIntake{By: actor})
	case "skill_archive_promote":
		var p struct {
			Name    string `json:"name"`
			Version int    `json:"version"`
		}
		if json.Unmarshal(raw, &p) != nil || p.Name == "" || p.Version <= 0 {
			return nil, errors.New("skill_archive promote needs a name and a version")
		}
		return d.call(ctx, protocol.FrameSkillArchivePromote, protocol.SkillArchivePromote{Name: p.Name, Version: p.Version, By: actor})
	}
	return nil, errors.New("unknown skill_archive action")
}

// skillArchiveOwnerUndo is the owner's undo in the Desk. It reaches the same
// gateway operation as the CTO tool, with the owner as the actor.
func (d *Daemon) skillArchiveOwnerUndo(ctx context.Context, change string) (any, error) {
	if strings.TrimSpace(change) == "" {
		return nil, errors.New("skill_archive_undo needs a change")
	}
	return d.call(ctx, protocol.FrameSkillArchiveUndo, protocol.SkillArchiveUndo{Change: change, By: "owner"})
}

// skillArchiveNotice describes an archive change for the Inbox: one line, and
// the undo when the call made exactly one change.
func skillArchiveNotice(concrete string, result any) (text, diff string, options []string) {
	undo := func(change string) {
		if change == "" {
			return
		}
		raw, _ := json.Marshal(map[string]string{"change": change})
		options = []string{"skill_archive_undo", string(raw)}
	}
	switch concrete {
	case "skill_archive_add", "skill_archive_remove", "skill_archive_undo", "skill_archive_promote":
		var r protocol.SkillArchiveAddResult
		b, _ := json.Marshal(result)
		if json.Unmarshal(b, &r) != nil {
			return "", "", nil
		}
		switch {
		case concrete == "skill_archive_add" && r.State == "unchanged":
			text = "CTO checked skill " + cleanLine(r.Name) + ": already in the archive"
			diff = "same content: version " + strconv.Itoa(r.Version) + " unchanged"
		case concrete == "skill_archive_add" && r.State == "restored":
			text = "CTO restored skill " + cleanLine(r.Name) + " at version " + strconv.Itoa(r.Version)
			undo(r.Change)
		case concrete == "skill_archive_add":
			text = "CTO added skill " + cleanLine(r.Name) + " version " + strconv.Itoa(r.Version)
			diff = cleanLine(r.Description)
			undo(r.Change)
		case concrete == "skill_archive_remove":
			text = "CTO removed skill " + cleanLine(r.Name) + " (" + strconv.Itoa(r.Versions) + " versions)"
			undo(r.Change)
		case concrete == "skill_archive_promote" && r.State == "unchanged":
			text = "CTO checked skill " + cleanLine(r.Name) + ": version " + strconv.Itoa(r.Version) + " is already the latest"
		case concrete == "skill_archive_promote":
			text = "CTO chose version " + strconv.Itoa(r.Version) + " of skill " + cleanLine(r.Name) + " as the latest"
			undo(r.Change)
		default:
			verb := "restored"
			switch r.State {
			case "removed":
				verb = "removed"
			case "reset":
				verb = "reset the latest of"
			}
			text = "CTO undid an archive change: " + verb + " " + cleanLine(r.Name)
		}
		return text, diff, options
	case "skill_archive_add_git", "skill_archive_intake":
		var r protocol.SkillArchiveMultiResult
		b, _ := json.Marshal(result)
		if json.Unmarshal(b, &r) != nil {
			return "", "", nil
		}
		var names, candidates, noDescription []string
		changed := 0
		last := ""
		for _, skill := range r.Skills {
			if skill.State == "added" || skill.State == "restored" {
				names = append(names, cleanLine(skill.Name)+" v"+strconv.Itoa(skill.Version))
				if skill.Latest != 0 && skill.Latest != skill.Version {
					candidates = append(candidates, cleanLine(skill.Name)+" v"+strconv.Itoa(skill.Version)+" (latest stays v"+strconv.Itoa(skill.Latest)+")")
				}
				if skill.NoDescription {
					noDescription = append(noDescription, cleanLine(skill.Name))
				}
				changed++
				last = skill.Change
			}
		}
		where := "a Git URL"
		if concrete == "skill_archive_intake" {
			where = "the online machines"
		}
		switch {
		case changed == 0:
			text = "CTO checked " + where + ": the archive already has every skill"
		case changed == 1:
			text = "CTO added " + names[0] + " from " + where
			undo(last)
		default:
			text = "CTO added " + strconv.Itoa(changed) + " skills from " + where
			diff = strings.Join(names, "\n") + "\nUndo one with skill_archive undo and its change id."
		}
		if len(r.Shared) > 0 {
			if diff != "" {
				diff += "\n"
			}
			diff += "More than one version: " + strings.Join(r.Shared, ", ")
		}
		for _, line := range []string{
			joinLabeled("Not latest", candidates),
			joinLabeled("No description", noDescription),
		} {
			if line == "" {
				continue
			}
			if diff != "" {
				diff += "\n"
			}
			diff += line
		}
		if line := intakeMachineLine(r); line != "" {
			if diff != "" {
				diff += "\n"
			}
			diff += line
		}
		return text, diff, options
	}
	return "", "", nil
}

// joinLabeled is "label: a, b", or empty when there is nothing to name.
func joinLabeled(label string, items []string) string {
	if len(items) == 0 {
		return ""
	}
	return label + ": " + strings.Join(items, ", ")
}

// intakeMachineLine names the machines an intake did not read and the count of
// skills it did not store, so the Inbox never reads as "all done" by accident.
func intakeMachineLine(r protocol.SkillArchiveMultiResult) string {
	var parts []string
	for _, m := range r.Machines {
		if m.Result != "scanned" {
			parts = append(parts, cleanLine(m.Name)+" "+m.Result)
		}
	}
	line := ""
	if len(parts) > 0 {
		line = "Not read: " + strings.Join(parts, ", ") + "."
	}
	if len(r.Skipped) > 0 {
		if line != "" {
			line += " "
		}
		noun := " skills"
		if len(r.Skipped) == 1 {
			noun = " skill"
		}
		line += strconv.Itoa(len(r.Skipped)) + noun + " not imported."
	}
	return line
}
