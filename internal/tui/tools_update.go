package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolsdiff"
)

// toolsUpdateSource is the part of the owner client that saves a changed copy
// and updates older copies. The Tools tab uses it only when the source has it.
type toolsUpdateSource interface {
	ToolsSaveChanged(context.Context, protocol.ToolImport) error
	ToolsUpdatePlan(context.Context, string) (protocol.ToolUpdatePlan, error)
	ToolsUpdateCopies(context.Context, string, []protocol.ToolImportLocation) (protocol.ToolUpdatePlan, error)
}

func changedCopyWhere(c toolsdiff.ChangedCopy) string {
	where := c.Machine
	if c.Project != "" {
		where += " in " + c.Project
	}
	return where
}

// saveChangedTool asks the owner to save one changed copy as the skill's next
// version, then shows the plan for updating the older copies.
func (m Model) saveChangedTool(r toolsdiff.Row, c toolsdiff.ChangedCopy) (tea.Model, tea.Cmd) {
	src, ok := m.src.(toolsUpdateSource)
	if !ok {
		m.toolsToast("saving a changed copy is unavailable")
		return m, nil
	}
	md := m.openModal("Save " + r.Name + " as the next version?")
	md.lines = []string{
		"The copy on " + sanitize(changedCopyWhere(c)) + " was edited after agent-net put it there:",
		"  " + sanitize(c.Location),
		"Saving stores it as the next version. Nothing is deployed yet: you see a plan for the other copies next.",
	}
	md.foot = []string{"enter", "save it", "esc", "cancel"}
	md.actions["enter"] = func(m Model) (Model, tea.Cmd) {
		md := m.tl.modal
		id := md.id
		md.loading, md.lines, md.actions, md.foot = true, []string{"Saving..."}, map[string]func(Model) (Model, tea.Cmd){}, []string{"esc", "back"}
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 3*Timeout)
			defer cancel()
			err := src.ToolsSaveChanged(ctx, protocol.ToolImport{Item: c.Item, Location: protocol.ToolImportLocation{NodeID: c.NodeID, Location: c.Location}})
			if err != nil {
				return toolsModalMsg{id: id, update: func(md *toolsModal) {
					md.loading, md.done, md.lines = false, true, []string{explain(err)}
					md.foot = []string{"enter", "close", "esc", "back"}
				}}
			}
			plan, perr := src.ToolsUpdatePlan(ctx, c.Item)
			return toolsModalMsg{id: id, reload: true, update: func(md *toolsModal) {
				md.loading = false
				if perr != nil {
					md.done, md.lines = true, []string{"Saved as the next version.", "The update plan is unavailable: " + explain(perr)}
					md.foot = []string{"enter", "close", "esc", "back"}
					return
				}
				armUpdate(md, src, plan, "Saved as v"+fmt.Sprint(plan.No)+".")
			}}
		}
	}
	return m, nil
}

// updateTool opens the plan for updating older copies to the stored version.
func (m Model) updateTool(r toolsdiff.Row) (tea.Model, tea.Cmd) {
	src, ok := m.src.(toolsUpdateSource)
	if !ok {
		m.toolsToast("updating copies is unavailable")
		return m, nil
	}
	item := r.Item.ID
	md := m.openModal("Update older copies of " + r.Name)
	md.loading = true
	md.foot = []string{"esc", "back"}
	id := md.id
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*Timeout)
		defer cancel()
		plan, err := src.ToolsUpdatePlan(ctx, item)
		return toolsModalMsg{id: id, update: func(md *toolsModal) {
			md.loading = false
			if err != nil {
				md.done, md.lines = true, []string{explain(err)}
				md.foot = []string{"enter", "close", "esc", "back"}
				return
			}
			armUpdate(md, src, plan, "")
		}}
	}
}

// armUpdate shows the update plan and, when it replaces anything, makes enter
// apply exactly the copies shown. Kept copies are listed with the reason and
// are never touched.
func armUpdate(md *toolsModal, src toolsUpdateSource, plan protocol.ToolUpdatePlan, lead string) {
	var lines []string
	if lead != "" {
		lines = append(lines, lead)
	}
	var replace []protocol.ToolImportLocation
	var kept []string
	for _, c := range plan.Copies {
		where := sanitize(c.Machine)
		if c.Project != "" {
			where += " in " + sanitize(c.Project)
		}
		from := "an older copy"
		if c.FromNo > 0 {
			from = fmt.Sprintf("v%d", c.FromNo)
		}
		if c.State == protocol.UpdateWillReplace {
			replace = append(replace, protocol.ToolImportLocation{NodeID: c.NodeID, Location: c.Location})
			lines = append(lines, fmt.Sprintf("  replace %s -> v%d on %s (%s)", from, plan.No, where, sanitize(c.Location)))
		} else {
			kept = append(kept, fmt.Sprintf("  keep on %s (%s): %s", where, sanitize(c.Location), sanitize(c.Reason)))
		}
	}
	if len(replace) == 0 {
		lines = append(lines, fmt.Sprintf("Every copy is up to date with v%d.", plan.No))
	} else {
		lines = append([]string{fmt.Sprintf("Update %d copy(ies) to v%d. Each replaced copy is backed up first.", len(replace), plan.No)}, lines...)
	}
	lines = append(lines, kept...)
	md.lines = lines
	md.actions = map[string]func(Model) (Model, tea.Cmd){}
	md.foot = []string{"enter", "close", "esc", "back"}
	md.done = true
	if len(replace) == 0 {
		return
	}
	md.done = false
	md.foot = []string{"enter", "confirm update", "esc", "cancel"}
	md.actions["enter"] = func(m Model) (Model, tea.Cmd) {
		md := m.tl.modal
		id := md.id
		md.loading, md.lines, md.actions, md.foot = true, []string{"Updating..."}, map[string]func(Model) (Model, tea.Cmd){}, []string{"esc", "back"}
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 10*Timeout)
			defer cancel()
			done, err := src.ToolsUpdateCopies(ctx, plan.Item, replace)
			return toolsModalMsg{id: id, reload: true, update: func(md *toolsModal) {
				md.loading, md.done = false, true
				md.foot = []string{"enter", "close", "esc", "back"}
				if err != nil {
					md.lines = []string{explain(err)}
					return
				}
				md.lines = nil
				for _, c := range done.Copies {
					where := sanitize(c.Machine)
					if c.Project != "" {
						where += " in " + sanitize(c.Project)
					}
					if c.State == protocol.UpdateApplied {
						md.lines = append(md.lines, fmt.Sprintf("updated %s to v%d", where, done.No))
					} else {
						md.lines = append(md.lines, fmt.Sprintf("NOT updated %s: %s", where, sanitize(strings.TrimSpace(c.Reason))))
					}
				}
			}}
		}
	}
}

// newestStored is the skill's highest stored version among the row's variants.
func newestStored(r toolsdiff.Row) (protocol.ToolItem, bool) {
	if r.Kind != protocol.InventorySkill {
		return protocol.ToolItem{}, false
	}
	best := protocol.ToolItem{}
	for _, v := range r.Variants {
		if v.VersionNo > best.VersionNo {
			best = v
		}
	}
	return best, best.VersionNo > 0
}
