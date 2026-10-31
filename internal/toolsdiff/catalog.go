// Package toolsdiff turns the gateway's tool items (tools_items) into what the
// owner sees: one row per tool with an on or off cell per machine, and a list
// of differences, each with one suggested fix. It is pure: no calls, no
// clock. The Tools tab and the CTO tool tools_differences use it, so both
// give the same answer.
//
// The owner never sees harnesses. A skill is on or off for a machine, or on for
// chosen projects. A limit of one harness shows as a partial cell and a note.
package toolsdiff

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// Where a tool comes from.
const (
	OriginStore    = "store"    // agent-net store, with a version
	OriginMachines = "machines" // found on the machines, not managed
	OriginRepo     = "repo"     // inside a project's files, not put there by agent-net
	OriginGateway  = "gateway"  // an MCP server reached through the gateway
)

// CellState is what one machine cell shows.
type CellState int

const (
	CellOff     CellState = iota // dot: off
	CellOn                       // on
	CellPartial                  // on, with a note (a harness limit or a pending change)
	CellOther                    // other version than the managed one; Label is the version
	CellFound                    // there, not managed by agent-net
)

// Cell is the state of one tool on one machine.
type Cell struct {
	State CellState
	Label string
	Note  string
}

// Machine is a column.
type Machine struct {
	ID     string
	Name   string
	Online bool
}

// Project states in a row's project list.
const (
	ProjOn      = "on"
	ProjPending = "pending"
	ProjHidden  = "hidden" // the project's own copy hides the managed one
	ProjRepo    = "repo"
)

// ProjectState is the state of a tool for one project.
type ProjectState struct {
	Name  string
	State string
}

// Row is one line of the All tools screen.
type Row struct {
	Name    string
	Kind    string // protocol.InventorySkill or protocol.InventoryMCP
	Origin  string
	From    string
	Version string // short hash of the managed version, or ""
	Repo    string // project name for a repo row
	Cells   []Cell // one per machine, in the order of Catalog.Machines
	// Projects lists the projects the tool is on for (or the one project of a
	// repo row).
	Projects []ProjectState
	Note     string // short note for the grid; never names a harness
	Detail   string // one line for the details pane; may name a limit
	// Item is the managed (or federated) item, or the first variant.
	Item protocol.ToolItem
	// Variants are all items with this kind and name.
	Variants []protocol.ToolItem
}

// Catalog is everything the three screens show.
type Catalog struct {
	Machines []Machine
	Rows     []Row
	Diffs    []Difference
}

// Short is the version label of a content hash.
func Short(hash string) string {
	if i := strings.LastIndex(hash, ":"); i >= 0 {
		hash = hash[i+1:]
	}
	if len(hash) > 6 {
		hash = hash[:6]
	}
	return hash
}

// Changed is the label of a copy whose content is not any stored version: it
// was edited outside agent-net.
const Changed = "changed"

// Label names a content for the owner. A stored version reads "v3". When the
// skill has stored versions but this content is none of them, it reads
// "changed". A skill with no stored version has nothing to count from, so the
// content shows as a short hash.
func (g group) label(versionNo int, hash string) string {
	if versionNo > 0 {
		return "v" + strconv.Itoa(versionNo)
	}
	for _, v := range g.variants {
		if v.VersionNo > 0 {
			return Changed
		}
	}
	return Short(hash)
}

func managed(it protocol.ToolItem) bool {
	if it.Federated {
		return false
	}
	return it.Desired != nil || (it.Kind == protocol.InventorySkill && len(it.Locations) == 0)
}

// readOnlySource marks a copy that no one can move: plugins, packages and the
// system. They are left out of the differences.
func readOnlySource(l protocol.ToolLocation) bool {
	switch l.Source {
	case protocol.InventoryPlugin, protocol.InventoryPackage, protocol.InventorySystem:
		return true
	}
	return false
}

func projectOf(l protocol.ToolLocation) string {
	switch {
	case l.GraphNode != "":
		return l.GraphNode
	case l.Project != "":
		return l.Project
	case l.ProjectRoot != "":
		return filepath.Base(l.ProjectRoot)
	}
	return ""
}

type group struct {
	kind, name string
	variants   []protocol.ToolItem
}

func groupItems(items []protocol.ToolItem) []group {
	idx := map[string]int{}
	var gs []group
	for _, it := range items {
		k := it.Kind + "\x00" + it.Name
		i, ok := idx[k]
		if !ok {
			i = len(gs)
			idx[k] = i
			gs = append(gs, group{kind: it.Kind, name: it.Name})
		}
		gs[i].variants = append(gs[i].variants, it)
	}
	sort.SliceStable(gs, func(i, j int) bool {
		if gs[i].kind != gs[j].kind {
			return gs[i].kind == protocol.InventorySkill
		}
		return gs[i].name < gs[j].name
	})
	return gs
}

// machinesOf returns the columns: the known machines in their order, then any
// machine that only shows up in the items, by name.
func machinesOf(items []protocol.ToolItem, known []Machine) []Machine {
	out := append([]Machine(nil), known...)
	have := map[string]bool{}
	for _, m := range out {
		have[m.ID] = true
		have[m.Name] = true
	}
	var extra []Machine
	for _, it := range items {
		for _, l := range it.Locations {
			if !have[l.NodeID] && !have[l.Machine] {
				have[l.NodeID], have[l.Machine] = true, true
				extra = append(extra, Machine{ID: l.NodeID, Name: l.Machine, Online: true})
			}
		}
	}
	sort.Slice(extra, func(i, j int) bool { return extra[i].Name < extra[j].Name })
	return append(out, extra...)
}

func machineIndex(ms []Machine, nodeID, name string) int {
	for i, m := range ms {
		if (nodeID != "" && m.ID == nodeID) || (name != "" && m.Name == name) {
			return i
		}
	}
	return -1
}

// Build computes the catalog and the differences. known lists the machines to
// show even when they hold no tool.
func Build(items []protocol.ToolItem, known []Machine) Catalog {
	c := Catalog{Machines: machinesOf(items, known)}
	for _, g := range groupItems(named(items, c.Machines)) {
		c.Rows = append(c.Rows, rowsOf(g, c.Machines)...)
	}
	c.Diffs = Differences(items, c.Machines)
	return c
}

// userCopies returns the machine-wide copies of a group, with read-only
// sources left out.
func userCopies(g group) []protocol.ToolLocation {
	var out []protocol.ToolLocation
	for _, v := range g.variants {
		for _, l := range v.Locations {
			if l.Scope == protocol.InventoryUser && !readOnlySource(l) {
				out = append(out, l)
			}
		}
	}
	return out
}

func rowsOf(g group, ms []Machine) []Row {
	var rows []Row
	var mg *protocol.ToolItem // the managed or federated variant
	for i := range g.variants {
		if g.variants[i].Federated || managed(g.variants[i]) {
			mg = &g.variants[i]
			break
		}
	}
	managedHash := map[string]bool{}
	for _, v := range g.variants {
		if managed(v) {
			managedHash[v.Hash] = true
		}
	}
	copies := userCopies(g)
	// Copies inside project files that agent-net did not put there.
	repoByProject := map[string]*Row{}
	var repoOrder []string
	for _, v := range g.variants {
		if managedHash[v.Hash] || v.Federated {
			continue
		}
		for _, l := range v.Locations {
			if l.Scope != protocol.InventoryProject || readOnlySource(l) {
				continue
			}
			p := projectOf(l)
			if p == "" {
				p = "(project)"
			}
			if repoByProject[p] == nil {
				repoOrder = append(repoOrder, p)
				repoByProject[p] = &Row{Name: g.name, Kind: g.kind, Origin: OriginRepo, From: "repo " + p, Repo: p, Cells: make([]Cell, len(ms)), Projects: []ProjectState{{Name: p, State: ProjRepo}}, Item: v, Variants: g.variants}
			}
		}
	}
	if len(copies) > 0 || mg != nil {
		r := Row{Name: g.name, Kind: g.kind, Cells: make([]Cell, len(ms)), Variants: g.variants}
		if mg != nil {
			r.Item = *mg
		} else {
			r.Item = g.variants[0]
		}
		switch {
		case mg != nil && mg.Federated:
			r.Origin = OriginGateway
			r.From = "gateway"
			if mg.Host != "" {
				r.From = "gateway on " + mg.Host
			}
			fillFederated(&r, *mg, ms)
		case mg != nil:
			r.Origin = OriginStore
			r.Version = g.label(mg.VersionNo, mg.Hash)
			r.From = "agent-net " + r.Version
			fillManaged(&r, g, *mg, copies, ms, repoByProject)
		default:
			r.Origin = OriginMachines
			r.From = "your machines"
			fillFound(&r, copies, ms)
		}
		rows = append(rows, r)
	}
	for _, p := range repoOrder {
		r := *repoByProject[p]
		for _, o := range rows {
			if o.Origin != OriginRepo && hasCopyOrManaged(o) {
				r.Note = "hides your copy here"
			}
		}
		r.Detail = "committed in the " + p + " project files. Sessions in that project get it on every machine."
		rows = append(rows, r)
	}
	return rows
}

func hasCopyOrManaged(r Row) bool {
	if r.Origin == OriginStore {
		return true
	}
	for _, c := range r.Cells {
		if c.State != CellOff {
			return true
		}
	}
	return false
}

func fillFederated(r *Row, it protocol.ToolItem, ms []Machine) {
	r.Detail = "MCP server running on " + orDash(it.Host) + ", reached through the agent-net gateway. Nothing is copied."
	if !it.HostOnline {
		r.Note = "host offline"
	}
	if it.Scope == "projects" {
		for _, p := range it.ScopeProjects {
			r.Projects = append(r.Projects, ProjectState{Name: p, State: ProjOn})
		}
		r.Detail += " On for chosen projects only."
		return
	}
	for i := range ms {
		r.Cells[i] = Cell{State: CellOn}
	}
	r.Detail += " On for every project on every machine."
}

func fillFound(r *Row, copies []protocol.ToolLocation, ms []Machine) {
	byM := map[int]map[string]bool{}
	for _, l := range copies {
		i := machineIndex(ms, l.NodeID, l.Machine)
		if i < 0 {
			continue
		}
		if byM[i] == nil {
			byM[i] = map[string]bool{}
		}
		byM[i][l.Hash] = true
	}
	var on []string
	for i := range ms {
		switch n := len(byM[i]); {
		case n == 0:
		case n == 1:
			r.Cells[i] = Cell{State: CellFound}
			on = append(on, ms[i].Name)
		default:
			r.Cells[i] = Cell{State: CellFound, Note: "two different copies"}
			r.Note = "two different copies"
			on = append(on, ms[i].Name)
		}
	}
	if r.Kind == protocol.InventoryMCP {
		r.Detail = "set up by hand in a harness config. Found on: " + strings.Join(on, ", ")
		if imp := importInfo(copies); imp != nil && imp.Class == protocol.MCPUnsupported {
			r.Note = "cannot go through the gateway"
			r.Detail += ". It cannot go through the gateway: " + imp.Reason
		} else {
			r.Detail += ". a sends it through the gateway; the hand-made entry stays until you remove it."
		}
		return
	}
	r.Detail = "a plain folder agent-net does not manage yet. Found on: " + strings.Join(on, ", ") + ". a imports it and turns it on where you choose."
}

func importInfo(copies []protocol.ToolLocation) *protocol.MCPImportInfo {
	for _, l := range copies {
		if l.Import != nil {
			return l.Import
		}
	}
	return nil
}

func fillManaged(r *Row, g group, mg protocol.ToolItem, copies []protocol.ToolLocation, ms []Machine, repos map[string]*Row) {
	scope := "global"
	if mg.Desired != nil {
		scope = mg.Desired.Assignment.Scope
	}
	pending := map[string]bool{} // node IDs with a result that is not applied
	failReason := ""
	for _, res := range mg.Results {
		if res.State != "applied" {
			pending[res.NodeID] = true
			if res.State == "conflict" || res.State == "failed" {
				failReason = res.Reason
			}
		}
	}
	limit := ""
	// A machine the desired state does not name is not waiting for anything.
	inScope := func(m Machine) bool { return mg.Desired == nil || mg.Desired.Assignment.OnMachine(m.ID) }
	for i, m := range ms {
		var cell Cell
		hasManaged, other := false, ""
		for _, l := range copies {
			if machineIndex(ms, l.NodeID, l.Machine) != i {
				continue
			}
			if l.Hash == mg.Hash {
				hasManaged = true
				if len(l.Unsupported) > 0 && cell.Note == "" {
					cell.Note = l.Unsupported[0]
					if limit == "" {
						limit = l.Unsupported[0]
					}
				}
			} else if other == "" {
				other = g.label(l.VersionNo, l.Hash)
			}
		}
		switch {
		case hasManaged && cell.Note != "":
			cell.State = CellPartial
		case hasManaged && pending[m.ID]:
			cell.State, cell.Note = CellPartial, "change pending"
		case hasManaged:
			cell.State = CellOn
		case other != "":
			cell.State, cell.Label = CellOther, other
		case scope == "global" && inScope(m):
			cell.State, cell.Note = CellPartial, "pending"
			if !m.Online {
				cell.Note = "pending: machine offline"
			}
		}
		r.Cells[i] = cell
	}
	for i, c := range r.Cells {
		if c.State == CellOther && r.Note == "" {
			r.Note = ms[i].Name + " on another version"
		}
	}
	if limit != "" && r.Note == "" {
		r.Note = "limited on one harness"
	}
	if scope == "projects" && mg.Desired != nil {
		for _, p := range mg.Desired.Assignment.Projects {
			st := ProjOn
			if repos[p] != nil {
				st = ProjHidden
			} else {
				applied := false
				for _, res := range mg.Results {
					if res.Project == p && res.State == "applied" {
						applied = true
					}
				}
				if !applied {
					st = ProjPending
				}
			}
			r.Projects = append(r.Projects, ProjectState{Name: p, State: st})
		}
	}
	only := ""
	if mg.Desired != nil {
		only = machineNames(ms, mg.Desired.Assignment.Machines)
	}
	keep := ""
	if only != "" {
		keep = " Other machines keep what they have."
	}
	switch scope {
	case "projects":
		who := "Every machine gets it"
		if only != "" {
			who = only + " only gets it"
		}
		r.Detail = "agent-net " + r.Version + " on for: " + projectList(r.Projects) + ". " + who + " for those projects." + keep
	case "uninstalled":
		where := "everywhere"
		if only != "" {
			where = "on " + only
		}
		r.Detail = "agent-net " + r.Version + " is kept in the store and turned off " + where + "." + keep
	default:
		r.Detail = "agent-net " + r.Version + " on every project on: " + strings.Join(onNames(ms, r.Cells), ", ") + ". agent-net handles where each harness reads skills."
		if only != "" {
			r.Detail = "agent-net " + r.Version + " on every project, set for " + only + " only: " + strings.Join(onNames(ms, r.Cells), ", ") + "." + keep
		}
	}
	if limit != "" {
		r.Detail = "agent-net " + r.Version + ": " + limit
	}
	if failReason != "" {
		r.Detail += " Last problem: " + failReason
	}
}

// machineNames returns the names of the machines with these IDs, comma
// separated, or "" when no machine is named.
func machineNames(ms []Machine, ids []string) string {
	var out []string
	for _, id := range ids {
		if i := machineIndex(ms, id, ""); i >= 0 {
			out = append(out, ms[i].Name)
		} else {
			out = append(out, id)
		}
	}
	return strings.Join(out, ", ")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func projectList(ps []ProjectState) string {
	var out []string
	for _, p := range ps {
		s := p.Name
		switch p.State {
		case ProjPending:
			s += " (pending)"
		case ProjHidden:
			s += " (hidden by the repo's copy)"
		}
		out = append(out, s)
	}
	return strings.Join(out, ", ")
}

func onNames(ms []Machine, cells []Cell) []string {
	var out []string
	for i, c := range cells {
		if c.State == CellOn || c.State == CellPartial {
			out = append(out, ms[i].Name)
		}
	}
	if len(out) == 0 {
		out = append(out, "no machine yet")
	}
	return out
}

// ProjectSummary returns the text for the PROJECTS column, and whether it
// deserves a warning colour.
func (r Row) ProjectSummary() (text string, warn bool) {
	if len(r.Projects) == 0 {
		return "-", false
	}
	if r.Origin == OriginRepo {
		return "⌂ " + r.Repo, false
	}
	pend, hid := 0, 0
	for _, p := range r.Projects {
		switch p.State {
		case ProjPending:
			pend++
		case ProjHidden:
			hid++
		}
	}
	text = strconv.Itoa(len(r.Projects)) + " project"
	if len(r.Projects) > 1 {
		text += "s"
	}
	if pend > 0 {
		text += " · " + strconv.Itoa(pend) + " pending"
	}
	if hid > 0 {
		text += " · " + strconv.Itoa(hid) + " hidden"
	}
	return text, pend+hid > 0
}
