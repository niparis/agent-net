package toolsdiff

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// Kinds of difference.
const (
	KindDrift      = "drift"        // two machines hold different versions
	KindTwoCopies  = "two-copies"   // one machine holds two different copies
	KindHidden     = "hidden"       // a project's own copy hides another one
	KindDuplicate  = "duplicate"    // two names with the same content
	KindUnmanaged  = "unmanaged"    // only on some machines and not managed
	KindPending    = "pending"      // waits for a machine
	KindFailed     = "failed"       // a target could not be changed
	KindMCPNative  = "mcp-native"   // an MCP server set up by hand
	KindMCPLeftOld = "mcp-leftover" // on the gateway and still set up by hand
)

// Severities. They only order the list and pick the colour.
const (
	SevErr  = "err"
	SevWarn = "warn"
	SevDim  = "dim"
)

// Kinds of suggested fix.
const (
	ActNone            = "none"
	ActAssign          = "assign"      // put Item on every machine
	ActChoose          = "choose"      // keep one of Copies
	ActRemoveRepo      = "remove-repo" // remove Name from the repo of Project
	ActRemove          = "remove"      // turn Name off everywhere (import first)
	ActImportAll       = "import-all"  // import Copies and put them on every machine
	ActMCPImport       = "mcp-import"  // send the server Name through the gateway
	ActMCPNativeRemove = "mcp-native-remove"
)

// Copy is one place a skill lives: enough to import it.
type Copy struct {
	Item     string `json:"item"`
	NodeID   string `json:"node_id"`
	Machine  string `json:"machine"`
	Location string `json:"location"`
	Checkout string `json:"checkout,omitempty"`
	Hash     string `json:"hash"`
	Name     string `json:"name,omitempty"`
	// Label is the version of this copy: "v2", "changed" or a short hash.
	Label string `json:"label,omitempty"`
}

// Version is the label to show for the copy.
func (c Copy) Version() string {
	if c.Label != "" {
		return c.Label
	}
	return Short(c.Hash)
}

// Action is the one suggested fix, in data. The TUI turns it into a plan.
type Action struct {
	Kind    string   `json:"kind"`
	Item    string   `json:"item,omitempty"`
	Name    string   `json:"name,omitempty"`
	Project string   `json:"project,omitempty"`
	Copies  []Copy   `json:"copies,omitempty"`
	Names   []string `json:"names,omitempty"`
}

// Difference is one line of the Differences screen.
type Difference struct {
	Key      string   `json:"key"`
	Kind     string   `json:"kind"`
	Severity string   `json:"severity"`
	Title    string   `json:"title"`
	Fix      string   `json:"fix"`
	Name     string   `json:"name,omitempty"`
	Machines []string `json:"machines,omitempty"`
	Projects []string `json:"projects,omitempty"`
	// Info lines have nothing to do: they are not acceptable and not counted.
	Info   bool   `json:"info,omitempty"`
	Action Action `json:"-"`
}

func sevRank(s string) int {
	switch s {
	case SevErr:
		return 0
	case SevWarn:
		return 1
	}
	return 2
}

// Differences finds what differs between machines and projects. ms are the
// machine columns.
func Differences(items []protocol.ToolItem, ms []Machine) []Difference {
	ms = machinesOf(items, ms)
	gs := groupItems(named(items, ms))
	var out []Difference
	skipUnmanaged := map[string]bool{}
	for _, g := range gs {
		if g.kind != protocol.InventorySkill {
			out = append(out, mcpDiffs(g)...)
			continue
		}
		ds := skillDiffs(g, ms)
		for _, d := range ds {
			if d.Kind == KindDrift || d.Kind == KindTwoCopies {
				skipUnmanaged[g.name] = true
			}
		}
		out = append(out, ds...)
	}
	out = append(out, duplicateDiffs(gs)...)
	out = append(out, unmanagedDiffs(gs, ms, skipUnmanaged)...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Info != b.Info {
			return !a.Info
		}
		if sevRank(a.Severity) != sevRank(b.Severity) {
			return sevRank(a.Severity) < sevRank(b.Severity)
		}
		return a.Key < b.Key
	})
	return out
}

func managedOf(g group) *protocol.ToolItem {
	for i := range g.variants {
		if managed(g.variants[i]) {
			return &g.variants[i]
		}
	}
	return nil
}

func nameList(names []string, max int) string {
	if len(names) <= max {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:max], ", ") + fmt.Sprintf(" and %d more", len(names)-max)
}

func copyOf(g group, v protocol.ToolItem, l protocol.ToolLocation) Copy {
	return Copy{Item: v.ID, NodeID: l.NodeID, Machine: l.Machine, Location: l.Location, Checkout: l.ProjectRoot, Hash: v.Hash, Name: v.Name, Label: g.label(v.VersionNo, v.Hash)}
}

func skillDiffs(g group, ms []Machine) []Difference {
	var out []Difference
	mg := managedOf(g)
	// hashes per machine, with the copy that holds each.
	type held struct {
		hashes map[string]Copy
		paths  map[string]bool
	}
	per := map[string]*held{}
	nodeOf := map[string]string{}
	var order []string
	// Observations from older peers have no content fingerprint. Reuse a known
	// fingerprint for the same strict hash and otherwise retain the strict hash.
	fingerprints := map[string]string{}
	for _, v := range g.variants {
		for _, l := range v.Locations {
			if l.ContentFingerprint != "" {
				fingerprints[v.Hash] = l.ContentFingerprint
			}
		}
	}
	comparisonHash := func(hash string) string {
		if f := fingerprints[hash]; f != "" {
			return "content:" + f
		}
		return "strict:" + hash
	}
	userStrict := map[string]bool{}
	// A machine the desired state does not name is not part of the change:
	// "on research only" is not drift against the other machines.
	inScope := func(machine string) bool {
		return mg == nil || mg.Desired == nil || mg.Desired.Assignment.OnMachine(nodeOf[machine])
	}
	for _, v := range g.variants {
		for _, l := range v.Locations {
			if l.Scope != protocol.InventoryUser || readOnlySource(l) {
				continue
			}
			h := per[l.Machine]
			if h == nil {
				h = &held{hashes: map[string]Copy{}, paths: map[string]bool{}}
				per[l.Machine] = h
				nodeOf[l.Machine] = l.NodeID
				order = append(order, l.Machine)
			}
			if l.Location != "" && h.paths[l.Location] {
				continue
			}
			if l.Location != "" {
				h.paths[l.Location] = true
			}
			userStrict[v.Hash] = true
			key := comparisonHash(v.Hash)
			if _, ok := h.hashes[key]; !ok {
				h.hashes[key] = copyOf(g, v, l)
			}
		}
	}
	sort.Strings(order)
	// two copies on one machine
	twoOn := map[string]bool{}
	for _, m := range order {
		h := per[m]
		if len(h.hashes) < 2 {
			continue
		}
		twoOn[m] = true
		var cs []Copy
		for _, c := range h.hashes {
			cs = append(cs, c)
		}
		sort.Slice(cs, func(i, j int) bool { return cs[i].Location < cs[j].Location })
		out = append(out, Difference{
			Key: "two-copies:" + g.name + ":" + m, Kind: KindTwoCopies, Severity: SevErr, Name: g.name, Machines: []string{m},
			Title:  fmt.Sprintf("%s has %d different copies on %s", g.name, len(cs), m),
			Fix:    "compare, then keep one",
			Action: Action{Kind: ActChoose, Name: g.name, Copies: cs},
		})
	}
	// versions that differ between machines
	byHash := map[string][]string{}
	firstCopy := map[string]Copy{}
	for _, m := range order {
		if twoOn[m] || !inScope(m) {
			continue
		}
		for h, c := range per[m].hashes {
			byHash[h] = append(byHash[h], m)
			if _, ok := firstCopy[h]; !ok {
				firstCopy[h] = c
			}
		}
	}
	if len(byHash) >= 2 {
		var hs []string
		for h := range byHash {
			hs = append(hs, h)
		}
		sort.Slice(hs, func(i, j int) bool {
			if len(byHash[hs[i]]) != len(byHash[hs[j]]) {
				return len(byHash[hs[i]]) > len(byHash[hs[j]])
			}
			return hs[i] < hs[j]
		})
		labelOf := map[string]string{}
		for _, v := range g.variants {
			labelOf[comparisonHash(v.Hash)] = g.label(v.VersionNo, v.Hash)
		}
		// One part per machine: "Mac on v1, research on v3".
		hashOf := map[string]string{}
		for _, h := range hs {
			for _, m := range byHash[h] {
				hashOf[m] = h
			}
		}
		var parts, machines []string
		for _, m := range order {
			if h, ok := hashOf[m]; ok {
				parts = append(parts, fmt.Sprintf("%s on %s", m, labelOf[h]))
				machines = append(machines, m)
			}
		}
		d := Difference{Key: "drift:" + g.name, Kind: KindDrift, Severity: SevWarn, Name: g.name, Machines: machines,
			Title: fmt.Sprintf("%s differs: %s", g.name, strings.Join(parts, ", "))}
		if mg != nil {
			d.Fix = "make every machine use " + g.label(mg.VersionNo, mg.Hash)
			d.Action = Action{Kind: ActAssign, Item: mg.ID, Name: g.name}
		} else {
			d.Fix = "compare, then keep one"
			var cs []Copy
			for _, h := range hs {
				cs = append(cs, firstCopy[h])
			}
			d.Action = Action{Kind: ActChoose, Name: g.name, Copies: cs}
		}
		out = append(out, d)
	}
	// a project's own copy hides a machine-wide one
	userHashes := userStrict
	if mg != nil {
		userHashes[mg.Hash] = true
	}
	if len(userHashes) > 0 {
		seen := map[string]bool{}
		for _, v := range g.variants {
			if userHashes[v.Hash] {
				continue
			}
			for _, l := range v.Locations {
				if l.Scope != protocol.InventoryProject || readOnlySource(l) {
					continue
				}
				p := projectOf(l)
				mark := p + "\x00" + fmt.Sprint(l.Tracked)
				if p == "" || seen[mark] {
					continue
				}
				seen[mark] = true
				var cs []Copy
				for _, w := range g.variants {
					for _, m := range w.Locations {
						if m.Scope == protocol.InventoryProject && projectOf(m) == p && m.Tracked == l.Tracked && !userHashes[w.Hash] {
							cs = append(cs, copyOf(g, w, m))
						}
					}
				}
				if !l.Tracked {
					// Not committed, so there is no repo change to plan: it is
					// a copy on one machine.
					out = append(out, Difference{
						Key: "hidden-local:" + g.name + ":" + p, Kind: KindHidden, Severity: SevWarn, Name: g.name, Projects: []string{p},
						Title:  fmt.Sprintf("%s has its own local copy of %s; it hides yours", p, g.name),
						Fix:    "turn the local copy off on its row in All tools",
						Action: Action{Kind: ActNone, Name: g.name, Project: p, Copies: cs},
					})
					continue
				}
				out = append(out, Difference{
					Key: "hidden:" + g.name + ":" + p, Kind: KindHidden, Severity: SevWarn, Name: g.name, Projects: []string{p},
					Title:  fmt.Sprintf("%s's repo has its own %s; it hides yours", p, g.name),
					Fix:    "remove the repo copy",
					Action: Action{Kind: ActRemoveRepo, Name: g.name, Project: p, Copies: cs},
				})
			}
		}
	}
	// results that are not applied
	if mg != nil && mg.Desired != nil && mg.Desired.Assignment.Scope != "uninstalled" {
		byNode := map[string]string{}
		for _, m := range ms {
			byNode[m.ID] = m.Name
		}
		var waiting, failed []string
		reason := ""
		for _, r := range mg.Results {
			if r.State == "applied" {
				continue
			}
			who := byNode[r.NodeID]
			if who == "" {
				who = r.NodeID
			}
			if r.State == "conflict" || r.State == "failed" {
				failed = appendOnce(failed, who)
				if reason == "" {
					reason = r.Reason
				}
			} else {
				waiting = appendOnce(waiting, who)
			}
		}
		if len(mg.Results) == 0 && mg.Applied == "pending" {
			for _, m := range ms {
				if mg.Desired.Assignment.OnMachine(m.ID) {
					waiting = appendOnce(waiting, m.Name)
				}
			}
		}
		if len(failed) > 0 {
			t := fmt.Sprintf("%s could not be changed on %s", g.name, strings.Join(failed, ", "))
			if reason != "" {
				t += ": " + reason
			}
			out = append(out, Difference{Key: "failed:" + g.name, Kind: KindFailed, Severity: SevWarn, Name: g.name, Machines: failed, Title: t,
				Fix: "fix the cause, then plan the change again", Action: Action{Kind: ActAssign, Item: mg.ID, Name: g.name}})
		}
		if len(waiting) > 0 {
			sort.Strings(waiting)
			out = append(out, Difference{Key: "pending:" + g.name, Kind: KindPending, Severity: SevDim, Name: g.name, Machines: waiting, Info: true,
				Title: fmt.Sprintf("%s waits for %s", g.name, strings.Join(waiting, ", ")),
				Fix:   "nothing to do: it applies when the machine is back", Action: Action{Kind: ActNone}})
		}
	}
	return out
}

func appendOnce(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// duplicateDiffs finds skills with different names and the same content.
func duplicateDiffs(gs []group) []Difference {
	type entry struct {
		name    string
		managed bool
		copy    Copy
	}
	byHash := map[string][]entry{}
	var hashes []string
	for _, g := range gs {
		if g.kind != protocol.InventorySkill {
			continue
		}
		for _, v := range g.variants {
			if v.Hash == "" || len(v.Locations) == 0 {
				continue
			}
			var c Copy
			ok := false
			for _, l := range v.Locations {
				if l.Scope == protocol.InventoryUser && !readOnlySource(l) {
					c, ok = copyOf(g, v, l), true
					break
				}
			}
			if !ok {
				continue
			}
			if _, seen := byHash[v.Hash]; !seen {
				hashes = append(hashes, v.Hash)
			}
			byHash[v.Hash] = append(byHash[v.Hash], entry{name: v.Name, managed: managed(v), copy: c})
		}
	}
	sort.Strings(hashes)
	var out []Difference
	for _, h := range hashes {
		es := byHash[h]
		names := map[string]bool{}
		for _, e := range es {
			names[e.name] = true
		}
		if len(names) < 2 {
			continue
		}
		sort.SliceStable(es, func(i, j int) bool {
			if es[i].managed != es[j].managed {
				return es[i].managed
			}
			return es[i].name < es[j].name
		})
		orig := es[0].name
		done := map[string]bool{}
		for _, e := range es[1:] {
			if e.name == orig || done[e.name] {
				continue
			}
			done[e.name] = true
			out = append(out, Difference{
				Key: "duplicate:" + e.name + ":" + orig, Kind: KindDuplicate, Severity: SevWarn, Name: e.name, Machines: []string{e.copy.Machine},
				Title:  fmt.Sprintf("%s looks like a copy of %s", e.name, orig),
				Fix:    "remove " + e.name,
				Action: Action{Kind: ActRemove, Name: e.name, Item: e.copy.Item, Copies: []Copy{e.copy}},
			})
		}
	}
	return out
}

// unmanagedDiffs groups skills that agent-net does not manage and that exist on
// only some machines. One line per set of machines.
func unmanagedDiffs(gs []group, ms []Machine, skip map[string]bool) []Difference {
	if len(ms) < 2 {
		return nil
	}
	type found struct {
		names  []string
		copies []Copy
	}
	by := map[string]*found{}
	var keys []string
	for _, g := range gs {
		if g.kind != protocol.InventorySkill || skip[g.name] || managedOf(g) != nil {
			continue
		}
		on := map[string]bool{}
		var cs []Copy
		hashes := map[string]bool{}
		for _, v := range g.variants {
			for _, l := range v.Locations {
				if l.Scope != protocol.InventoryUser || readOnlySource(l) {
					continue
				}
				if !on[l.Machine] {
					on[l.Machine] = true
					if !hashes[v.Hash] {
						hashes[v.Hash] = true
						cs = append(cs, copyOf(g, v, l))
					}
				}
			}
		}
		if len(on) == 0 || len(on) >= len(ms) || len(hashes) != 1 {
			continue
		}
		var machines []string
		for m := range on {
			machines = append(machines, m)
		}
		sort.Strings(machines)
		k := strings.Join(machines, ", ")
		if by[k] == nil {
			by[k] = &found{}
			keys = append(keys, k)
		}
		by[k].names = append(by[k].names, g.name)
		by[k].copies = append(by[k].copies, cs[0])
	}
	sort.Strings(keys)
	var out []Difference
	for _, k := range keys {
		f := by[k]
		sort.Strings(f.names)
		sum := sha256.Sum256([]byte(strings.Join(f.names, "\x00")))
		noun := "skills are"
		if len(f.names) == 1 {
			noun = "skill is"
		}
		out = append(out, Difference{
			Key: "unmanaged:" + k + ":" + hex.EncodeToString(sum[:4]), Kind: KindUnmanaged, Severity: SevDim,
			Title:    fmt.Sprintf("%d %s only on %s and not managed by agent-net", len(f.names), noun, k),
			Fix:      "import them and put them on every machine",
			Machines: strings.Split(k, ", "),
			Action:   Action{Kind: ActImportAll, Names: f.names, Copies: f.copies},
		})
	}
	return out
}

func mcpDiffs(g group) []Difference {
	var fed *protocol.ToolItem
	var native []protocol.ToolItem
	for i := range g.variants {
		if g.variants[i].Federated {
			fed = &g.variants[i]
		} else if len(g.variants[i].Locations) > 0 {
			native = append(native, g.variants[i])
		}
	}
	var machines []string
	importable := false
	for _, v := range native {
		for _, l := range v.Locations {
			machines = appendOnce(machines, l.Machine)
			if l.Import != nil && l.Import.Class == protocol.MCPImportable {
				importable = true
			}
		}
	}
	sort.Strings(machines)
	switch {
	case fed == nil && importable:
		return []Difference{{
			Key: "mcp-native:" + g.name, Kind: KindMCPNative, Severity: SevDim, Name: g.name, Machines: machines,
			Title:  fmt.Sprintf("MCP server %s is set up by hand on %s", g.name, strings.Join(machines, ", ")),
			Fix:    "send it through the gateway (the hand-made entry stays)",
			Action: Action{Kind: ActMCPImport, Name: g.name},
		}}
	case fed != nil && len(native) > 0 && (fed.NativeStillPresent || len(fed.Conflicts) > 0):
		return []Difference{{
			Key: "mcp-leftover:" + g.name, Kind: KindMCPLeftOld, Severity: SevWarn, Name: g.name, Machines: machines,
			Title:  fmt.Sprintf("MCP server %s is on the gateway and still set up by hand on %s", g.name, strings.Join(machines, ", ")),
			Fix:    "remove the hand-made entry",
			Action: Action{Kind: ActMCPNativeRemove, Name: g.name},
		}}
	}
	return nil
}

// Counts returns how many differences need a look (not info, not accepted).
func Counts(ds []Difference, accepted map[string]bool) int {
	n := 0
	for _, d := range ds {
		if !d.Info && !accepted[d.Key] {
			n++
		}
	}
	return n
}
