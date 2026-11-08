package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/temppath"
)

// Enumerate registered projects and eligible session repositories in one pass.
func (s *Store) knownToolProjectsTx(tx interface {
	Query(string, ...any) (*sql.Rows, error)
}) ([]string, error) {
	rows, err := tx.Query(`SELECT name FROM projects`)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		if name != "" {
			known[name] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = tx.Query(`SELECT repo_root, repo_project, no_project FROM sessions WHERE archived_at=0 AND harness != 'owner' AND repo_root<>''`)
	if err != nil {
		return nil, err
	}
	roots := s.tempRoots()
	for rows.Next() {
		var root, project string
		var noProject int
		if err = rows.Scan(&root, &project, &noProject); err != nil {
			rows.Close()
			return nil, err
		}
		if noProject != 0 || temppath.Under(root, roots) {
			continue
		}
		if project == "" {
			project = filepath.Base(root)
		}
		if project != "" {
			known[project] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(known))
	for name := range known {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func initToolProjectSettingTx(tx *sql.Tx, item string, on bool, names []string) error {
	res, err := tx.Exec(`INSERT OR IGNORE INTO tool_project_settings(item,new_projects) VALUES(?,?)`, item, boolInt(on))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	for _, p := range names {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO tool_project_seen(item,project) VALUES(?,?)`, item, p); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ToolProjectStates() ([]protocol.ToolProjectState, error) {
	return s.toolProjectStates(false)
}
func (s *Store) toolProjectStates(validate bool) ([]protocol.ToolProjectState, error) {
	items, err := s.ToolsItems(protocol.ToolsFilter{Kind: protocol.InventorySkill}, func(string) bool { return true })
	if err != nil {
		return nil, err
	}
	return s.toolProjectStatesOf(items, validate)
}

// toolProjectStatesOf is toolProjectStates for skill items the caller has read.
func (s *Store) toolProjectStatesOf(items []protocol.ToolItem, validate bool) ([]protocol.ToolProjectState, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	names, err := s.knownToolProjectsTx(tx)
	if err != nil {
		return nil, err
	}
	out := []protocol.ToolProjectState{}
	for _, item := range items {
		a := observedToolAssignment(item)
		if err = initToolProjectSettingTx(tx, item.ID, a.Scope == "global", names); err != nil {
			return nil, err
		}
		row := protocol.ToolProjectState{Item: item.ID, Projects: map[string]bool{}, Machines: []string{}}
		if err = tx.QueryRow(`SELECT new_projects FROM tool_project_settings WHERE item=?`, item.ID).Scan(&row.NewProjects); err != nil {
			return nil, err
		}
		for _, p := range names {
			row.Projects[p] = a.Scope == "global"
			for _, selected := range a.Projects {
				if selected == p {
					row.Projects[p] = true
				}
			}
		}
		machines := map[string]bool{}
		for _, loc := range item.Locations {
			machines[loc.NodeID] = true
		}
		for n := range machines {
			row.Machines = append(row.Machines, n)
		}
		sort.Strings(row.Machines)
		out = append(out, row)
	}
	if !validate {
		err = tx.Commit()
	}
	return out, err
}

func observedToolAssignment(item protocol.ToolItem) protocol.ToolAssignment {
	if item.Desired != nil {
		return item.Desired.Assignment
	}
	a := protocol.ToolAssignment{Scope: "uninstalled"}
	if item.Global {
		a.Scope = "global"
	} else if len(item.Projects) > 0 {
		a.Scope = "projects"
		a.Projects = append([]string(nil), item.Projects...)
	}
	// Migration never extends an unmanaged skill to another machine.
	found := map[string]bool{}
	for _, loc := range item.Locations {
		found[loc.NodeID] = true
	}
	for n := range found {
		a.Machines = append(a.Machines, n)
	}
	sort.Strings(a.Machines)
	return a
}

func (s *Store) SetToolNewProjects(p protocol.ToolNewProjectsRequest, actor string, validate bool) error {
	if !validChangeActor(actor) {
		return errors.New("owner authority required")
	}
	rows, err := s.toolProjectStates(validate)
	if err != nil {
		return err
	}
	found := false
	for _, r := range rows {
		found = found || r.Item == p.Item
	}
	if !found {
		return errors.New("skill item not found")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	names, err := s.knownToolProjectsTx(tx)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO tool_project_settings(item,new_projects) VALUES(?,?) ON CONFLICT(item) DO UPDATE SET new_projects=excluded.new_projects`, p.Item, boolInt(p.On)); err != nil {
		return err
	}
	// Enabling a rule affects future projects, not existing off cells.
	for _, name := range names {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO tool_project_seen(item,project) VALUES(?,?)`, p.Item, name); err != nil {
			return err
		}
	}
	if validate {
		return nil
	}
	return tx.Commit()
}

func (s *Store) ToolToggleAssignment(p protocol.ToolToggleRequest) (protocol.ToolAssignment, error) {
	return s.toolToggleAssignment(p, false)
}
func (s *Store) toolToggleAssignment(p protocol.ToolToggleRequest, validate bool) (protocol.ToolAssignment, error) {
	rows, err := s.toolProjectStates(validate)
	if err != nil {
		return protocol.ToolAssignment{}, err
	}
	items, err := s.ToolsItems(protocol.ToolsFilter{Kind: protocol.InventorySkill}, func(string) bool { return true })
	if err != nil {
		return protocol.ToolAssignment{}, err
	}
	for _, row := range rows {
		if row.Item != p.Item {
			continue
		}
		if _, ok := row.Projects[p.Project]; !ok {
			return protocol.ToolAssignment{}, errors.New("project is not known")
		}
		var a protocol.ToolAssignment
		for _, item := range items {
			if item.ID == p.Item {
				a = observedToolAssignment(item)
			}
		}
		row.Projects[p.Project] = p.On
		// A user-level copy already serves every project. Switching one on
		// changes nothing and must not be turned into project copies.
		if p.On && a.Scope == "global" {
			return normalizeAssignment(a)
		}
		a.Scope = "projects"
		a.Projects = nil
		for name, on := range row.Projects {
			if on {
				a.Projects = append(a.Projects, name)
			}
		}
		if len(a.Projects) == 0 {
			a.Scope = "uninstalled"
		}
		return normalizeAssignment(a)
	}
	return protocol.ToolAssignment{}, errors.New("skill item not found")
}

type skillToggleKey struct{}
type standingProjectsKey struct{}

func (g *Gateway) toggleTool(ctx context.Context, p protocol.ToolToggleRequest, actor string) (out protocol.ToolPlan, err error) {
	start := time.Now()
	defer func() {
		g.logToolOperation("tools.toggle", p.Item, p.Project, len(out.Targets), len(out.Skipped), start, err)
	}()
	if !validChangeActor(actor) {
		return protocol.ToolPlan{}, errors.New("owner authority required")
	}
	if _, e := g.refreshToolObservations(ctx); e != nil {
		return protocol.ToolPlan{}, e
	}
	ctx = context.WithValue(ctx, skillToggleKey{}, p)
	if group, ok, e := g.toggleSkillGroup(ctx, p, actor); ok || e != nil {
		return group, e
	}
	a, err := g.store.toolToggleAssignment(p, validatingProposal(ctx))
	if err != nil {
		return protocol.ToolPlan{}, err
	}
	return g.planSkillAssignment(ctx, p.Item, a, actor)
}

func (g *Gateway) ensureSkillImported(ctx context.Context, itemID string) error {
	var imported int
	var err error
	if err = g.store.db.QueryRow(`SELECT COUNT(*) FROM skill_versions WHERE item=?`, itemID).Scan(&imported); err != nil {
		return err
	}
	if imported == 0 {
		items, e := g.store.ToolsItems(protocol.ToolsFilter{Kind: protocol.InventorySkill}, func(string) bool { return true })
		if e != nil {
			return e
		}
		var location protocol.ToolImportLocation
		for _, item := range items {
			if item.ID == itemID {
				for _, loc := range item.Locations {
					if (loc.Scope == protocol.InventoryUser || ctx.Value(skillGroupKey{}) != nil && loc.Scope == protocol.InventoryProject && !loc.Tracked) && loc.State == protocol.InventoryOK && (loc.Source == "" || loc.Source == protocol.InventoryNative) && g.conn(loc.NodeID) != nil {
						location = protocol.ToolImportLocation{NodeID: loc.NodeID, Location: loc.Location}
						break
					}
				}
			}
		}
		if location.NodeID == "" {
			for _, item := range items {
				if item.ID != itemID {
					continue
				}
				for _, loc := range item.Locations {
					report := protocol.InventoryReport{Items: []protocol.InventoryItem{loc.InventoryItem}}
					if reason := inventory.PlanItemProblem(report, item.Kind, item.Name, loc.Location); reason != "" {
						return fmt.Errorf("%s: %s", loc.Machine, reason)
					}
				}
			}
			return errors.New("unmanaged skill has no online user-folder copy to import")
		}
		if _, err = g.importSkill(ctx, protocol.ToolImport{Item: itemID, Location: location}); err != nil {
			return err
		}
	}
	return nil
}

func (g *Gateway) planSkillAssignment(ctx context.Context, itemID string, a protocol.ToolAssignment, actor string) (protocol.ToolPlan, error) {
	if err := g.ensureSkillImported(ctx, itemID); err != nil {
		return protocol.ToolPlan{}, err
	}
	operation := ""
	if ctx.Value(skillGroupKey{}) != nil {
		operation = "skill_member"
	}
	return g.planTool(ctx, protocol.ToolPlanRequest{Item: itemID, Target: a, Operation: operation}, actor)
}

// scheduleNewSkillProjects runs addNewSkillProjects in the background so a node
// request never waits for it. One run at a time; requests that arrive during a
// run set a pending flag and cause one more run, not many.
func (g *Gateway) scheduleNewSkillProjects() {
	g.standingMu.Lock()
	defer g.standingMu.Unlock()
	if g.standingStopped {
		return
	}
	if g.standingRunning {
		g.standingPending = true
		return
	}
	if g.standingCtx == nil {
		g.standingCtx, g.standingCancel = context.WithCancel(context.Background())
	}
	g.standingRunning = true
	g.standingWG.Add(1)
	go func() {
		defer g.standingWG.Done()
		for {
			ctx, cancel := context.WithTimeout(g.standingCtx, 30*time.Second)
			err := g.addNewSkillProjects(ctx)
			cancel()
			if err != nil {
				g.logLine("skills.standing_choice.refused", "error", err)
			}
			g.standingMu.Lock()
			if !g.standingPending || g.standingStopped {
				g.standingRunning, g.standingPending = false, false
				g.standingMu.Unlock()
				return
			}
			g.standingPending = false
			g.standingMu.Unlock()
		}
	}()
}

func (g *Gateway) stopStandingChoices() {
	g.standingMu.Lock()
	g.standingStopped = true
	if g.standingCancel != nil {
		g.standingCancel()
	}
	g.standingMu.Unlock()
	g.standingWG.Wait()
}

// Called after project discovery. Seen cells remember existing off choices.
// A failed plan is logged once and the projects are still marked seen, so the
// plan is tried once per new project and not on every report.
func (g *Gateway) addNewSkillProjects(ctx context.Context) error {
	g.skillProjectsMu.Lock()
	defer g.skillProjectsMu.Unlock()
	g.skillScans.Add(1)
	before, beforeErr := g.store.skillInputsOf()
	err := g.addNewSkillProjectsLocked(ctx)
	if err == nil && beforeErr == nil {
		// Settled only when nothing but the choice's own marks changed while
		// it ran. Otherwise the next report runs it again.
		if after, afterErr := g.store.skillInputsOf(); afterErr == nil && after.shape() == before.shape() {
			g.skillSettled.Store(&after)
		}
	}
	return err
}

func (g *Gateway) addNewSkillProjectsLocked(ctx context.Context) error {
	items, err := g.store.ToolsItems(protocol.ToolsFilter{Kind: protocol.InventorySkill}, func(string) bool { return true })
	if err != nil {
		return err
	}
	rows, err := g.store.toolProjectStatesOf(items, false)
	if err != nil {
		return err
	}
	var firstErr error
	for _, row := range rows {
		var a protocol.ToolAssignment
		for _, item := range items {
			if item.ID == row.Item {
				a = observedToolAssignment(item)
			}
		}
		seen, err := g.store.toolProjectsSeen(row.Item)
		if err != nil {
			return err
		}
		unseen := []string{}
		for name := range row.Projects {
			if !seen[name] {
				unseen = append(unseen, name)
			}
		}
		sort.Strings(unseen)
		if len(unseen) == 0 {
			continue
		}
		allUnseen := unseen
		if row.NewProjects && a.Scope == "projects" {
			// Observed project copies can outlive their registered project. Do not
			// let one stale name prevent placement in all the other projects.
			tx, e := g.store.db.Begin()
			if e != nil {
				return e
			}
			placeable := func(names []string) ([]string, error) {
				var out []string
				for _, name := range names {
					known, e := g.store.projectKnownTx(tx, name)
					if e != nil {
						return nil, e
					}
					if !known {
						g.logLine("skills.standing_choice.skipped", "item", row.Item, "project", name, "reason", "project is not known")
						continue
					}
					out = append(out, name)
				}
				return out, nil
			}
			a.Projects, e = placeable(a.Projects)
			if e == nil {
				unseen, e = placeable(unseen)
			}
			tx.Rollback()
			if e != nil {
				return e
			}
			for _, name := range unseen {
				exists := false
				for _, p := range a.Projects {
					exists = exists || p == name
				}
				if !exists {
					a.Projects = append(a.Projects, name)
				}
			}
			// Keep project placement here: this standing choice only adds projects.
			if len(unseen) == 0 {
				for _, name := range allUnseen {
					if _, err = g.store.db.Exec(`INSERT OR IGNORE INTO tool_project_seen(item,project) VALUES(?,?)`, row.Item, name); err != nil {
						return err
					}
				}
				continue
			}
			if g.onStandingPlan != nil {
				g.onStandingPlan(row.Item, unseen)
			}
			planCtx := context.WithValue(ctx, standingProjectsKey{}, append([]string(nil), unseen...))
			plan, e := g.planTool(planCtx, protocol.ToolPlanRequest{Item: row.Item, Target: a}, "standing choice")
			if e == nil {
				for _, target := range plan.Targets {
					if target.NodeID == "" && (target.Action == "import" || target.Action == "verify") && target.Conflict != "" {
						e = errors.New(target.Conflict)
						break
					}
				}
			}
			if e == nil {
				_, e = g.applyTool(ctx, plan.ID, "standing choice")
			}
			if e != nil {
				g.logLine("skills.standing_choice.failed", "item", row.Item, "projects", strings.Join(unseen, ","), "error", e)
				if firstErr == nil {
					firstErr = e
				}
			}
		}
		for _, name := range allUnseen {
			if _, err = g.store.db.Exec(`INSERT OR IGNORE INTO tool_project_seen(item,project) VALUES(?,?)`, row.Item, name); err != nil {
				return err
			}
		}
	}
	return firstErr
}

// skillInputs is what decides whether the standing choice for new projects has
// work. Items is the skills it looks at and Projects the projects it knows;
// the choice has work when a skill has no seen mark for a project, so only a
// new skill, a new project or a change of the marks can give it work. Marks
// is the count of seen marks and Settings the count of skill settings, which
// the choice itself changes.
type skillInputs struct {
	Items, Projects string
	Marks, Settings int
}

// shape is the part of the inputs that the choice does not change itself.
func (a skillInputs) shape() [2]string { return [2]string{a.Items, a.Projects} }

// skillInputsOf reads the inputs of the standing choice. It is a few small
// reads, where the choice itself reads every skill with all its locations.
func (s *Store) skillInputsOf() (skillInputs, error) {
	var in skillInputs
	// The skill items are the identities that have a location, or that the
	// owner keeps through a version or a revision: what ToolsItems lists.
	err := s.db.QueryRow(`SELECT COALESCE(group_concat(id, ','), ''),
		(SELECT COUNT(*) FROM tool_project_seen), (SELECT COUNT(*) FROM tool_project_settings)
		FROM (SELECT i.id FROM tool_identities i WHERE i.kind=? AND (
		 EXISTS (SELECT 1 FROM tool_locations l WHERE l.identity_id=i.id)
		 OR EXISTS (SELECT 1 FROM skill_versions v WHERE v.item=i.id)
		 OR EXISTS (SELECT 1 FROM tool_revisions r WHERE r.item=i.id)) ORDER BY i.id)`,
		protocol.InventorySkill).Scan(&in.Items, &in.Marks, &in.Settings)
	if err != nil {
		return in, err
	}
	names, err := s.knownToolProjectsTx(s.db)
	if err != nil {
		return in, err
	}
	in.Projects = strings.Join(names, "\x00")
	return in, nil
}

// skillInputsSettled says that nothing has changed since the standing choice
// last found nothing to do. A failed read says no.
func (g *Gateway) skillInputsSettled() bool {
	settled := g.skillSettled.Load()
	if settled == nil {
		return false
	}
	now, err := g.store.skillInputsOf()
	return err == nil && now == *settled
}

// skillSettingsMissing tells whether some skill has no project settings yet.
// When none is missing, ToolProjectStates would change nothing. It may say yes
// for an identity that ToolProjectStates would skip; it never says no when
// there is something to write.
func (s *Store) skillSettingsMissing() (bool, error) {
	var missing bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM tool_identities i WHERE i.kind=? AND NOT EXISTS (SELECT 1 FROM tool_project_settings t WHERE t.item=i.id))`, protocol.InventorySkill).Scan(&missing)
	return missing, err
}

// toolProjectsSeen is the set of projects already marked seen for a skill.
func (s *Store) toolProjectsSeen(item string) (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT project FROM tool_project_seen WHERE item=?`, item)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, err
		}
		seen[name] = true
	}
	return seen, rows.Err()
}

// The setting is an owner choice immediately. A placement change still needs
// the returned plan to be confirmed through tools.apply.
func (g *Gateway) setToolNewProjects(ctx context.Context, p protocol.ToolNewProjectsRequest, actor string) (protocol.ToolPlan, error) {
	if !validChangeActor(actor) {
		return protocol.ToolPlan{}, errors.New("owner authority required")
	}
	rows, err := g.store.toolProjectStates(validatingProposal(ctx))
	if err != nil {
		return protocol.ToolPlan{}, err
	}
	items, err := g.store.ToolsItems(protocol.ToolsFilter{Kind: protocol.InventorySkill}, func(string) bool { return true })
	if err != nil {
		return protocol.ToolPlan{}, err
	}
	for _, item := range items {
		if item.ID != p.Item {
			continue
		}
		a := observedToolAssignment(item)
		for _, row := range rows {
			if row.Item != p.Item {
				continue
			}
			if len(row.Projects) == 0 && a.Scope == "global" && p.On {
				break
			}
			a.Projects = nil
			for name, on := range row.Projects {
				if on {
					a.Projects = append(a.Projects, name)
				}
			}
			a.Scope = "projects"
			if len(a.Projects) == 0 {
				a.Scope = "uninstalled"
			} else if len(a.Projects) == len(row.Projects) && p.On {
				a.Scope = "global"
				a.Projects = nil
			}
		}
		a, err = normalizeAssignment(a)
		if err != nil {
			return protocol.ToolPlan{}, err
		}
		// Import refusal must also refuse the setting change. Generate a preview
		// first, then bind the final plan to the saved setting's snapshot.
		previewCtx := context.WithValue(ctx, proposalValidationKey{}, true)
		if _, err = g.planSkillAssignment(previewCtx, p.Item, a, actor); err != nil {
			return protocol.ToolPlan{}, err
		}
		if err = g.store.SetToolNewProjects(p, actor, validatingProposal(ctx)); err != nil {
			return protocol.ToolPlan{}, err
		}
		return g.planSkillAssignment(ctx, p.Item, a, actor)
	}
	return protocol.ToolPlan{}, errors.New("skill item not found")
}
