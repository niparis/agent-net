package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// ctoSkills runs the node skill list actions (decision 0169, task 3). The
// gateway computes and stores the lists; the daemon routes and writes the Desk
// notice with undo.
func (d *Daemon) ctoSkills(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	actor := ctoActor(ctx)
	if actor == "" {
		actor = "cto"
	}
	switch name {
	case "skills_effective":
		var p protocol.SkillEffectiveRequest
		if json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad skills effective arguments")
		}
		p.By = actor
		return d.call(ctx, protocol.FrameSkillsEffective, p)
	case "skills_set":
		var p struct {
			Level  string   `json:"level"`
			Scope  string   `json:"scope"`
			Add    []string `json:"add"`
			Remove []string `json:"remove"`
			Pins   []struct {
				Name    string `json:"name"`
				Version int    `json:"version"`
			} `json:"pins"`
		}
		if json.Unmarshal(raw, &p) != nil || p.Level == "" {
			return nil, errors.New("skills set needs a level")
		}
		pins := map[string]int{}
		for _, pin := range p.Pins {
			if pin.Name != "" && pin.Version > 0 {
				pins[pin.Name] = pin.Version
			}
		}
		return d.call(ctx, protocol.FrameSkillsSet, protocol.SkillSetRequest{
			Level: p.Level, Scope: p.Scope,
			Add: p.Add, Remove: p.Remove, Pins: pins, By: actor,
		})
	case "skills_undo":
		var p struct {
			Change string `json:"change"`
		}
		if json.Unmarshal(raw, &p) != nil || p.Change == "" {
			return nil, errors.New("skills undo needs a change")
		}
		return d.call(ctx, protocol.FrameSkillsUndo, protocol.SkillUndoRequest{Change: p.Change, By: actor})
	}
	return nil, errors.New("unknown skills action")
}

// skillsOwnerUndo is the owner's undo in the Desk.
func (d *Daemon) skillsOwnerUndo(ctx context.Context, change string) (any, error) {
	if strings.TrimSpace(change) == "" {
		return nil, errors.New("skills_undo needs a change")
	}
	return d.call(ctx, protocol.FrameSkillsUndo, protocol.SkillUndoRequest{Change: change, By: "owner"})
}

// skillsNotice builds the Desk line for a set or undo: what changed, the
// token total, the sessions to restart, and the one-click undo.
func skillsNotice(concrete string, result any) (text, diff string, options []string) {
	var r protocol.SkillSetResult
	b, _ := json.Marshal(result)
	if json.Unmarshal(b, &r) != nil {
		return "", "", nil
	}
	where := r.Level
	if r.Scope != "" {
		where += " " + cleanLine(r.Scope)
	}
	if concrete == "skills_undo" {
		text = "CTO undid a skills set for " + where
	} else if r.Change == "" {
		text = "CTO checked skills for " + where + ": unchanged"
	} else {
		var parts []string
		if len(r.Added) > 0 {
			parts = append(parts, "added "+strings.Join(r.Added, ", "))
		}
		if len(r.Removed) > 0 {
			parts = append(parts, "removed "+strings.Join(r.Removed, ", "))
		}
		text = "CTO set skills for " + where
		if len(parts) > 0 {
			text += ": " + strings.Join(parts, "; ")
		}
		text += " (" + strconv.Itoa(r.AfterTokens) + " context tokens, was " + strconv.Itoa(r.BeforeTokens) + ")"
		if r.PinnedByMaturity {
			text += ", pinned to current versions: the repo is mature"
		}
	}
	var lines []string
	for _, skill := range r.Effective {
		line := skill.Name
		if skill.Version > 0 {
			line += " v" + strconv.Itoa(skill.Version)
		}
		if skill.Pin > 0 {
			line += " (pinned)"
		}
		line += " from " + skill.Level
		if skill.Scope != "" {
			line += " " + cleanLine(skill.Scope)
		}
		line += " (" + strconv.Itoa(skill.ContextTokens) + " context tokens, " + strconv.Itoa(skill.SizeTokens) + " in full)"
		lines = append(lines, line)
	}
	if len(r.Restart.Sessions) > 0 {
		var names []string
		for _, session := range r.Restart.Sessions {
			name := cleanLine(session.Name)
			if name == "" {
				name = session.SessionID
			}
			names = append(names, name+" ("+session.SessionID+")")
		}
		lines = append(lines, "restart to pick it up: "+strings.Join(names, ", "))
	}
	diff = strings.Join(lines, "\n")
	if r.Change != "" {
		raw, _ := json.Marshal(map[string]string{"id": r.Change})
		options = []string{"skills_undo", string(raw)}
	}
	return text, diff, options
}
