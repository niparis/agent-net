package phone

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

// screenInfo is one screen of the rail. Ready is false for a screen that only
// says it comes in the next update.
type screenInfo struct {
	ID, Name, Path, Key, Sub string
	Icon                     template.HTML
	Ready                    bool
}

// The icons are fixed markup of this package, never data.
var screens = []screenInfo{
	{ID: "sessions", Name: "Sessions", Path: "/sessions", Key: "1", Sub: "Every agent session on every machine", Ready: true,
		Icon: `<rect x="2" y="2.5" width="12" height="4" rx="1"/><rect x="2" y="9.5" width="12" height="4" rx="1"/>`},
	{ID: "graph", Name: "Graph", Path: "/graph", Key: "2", Sub: "Teams, leads and who talks to whom",
		Icon: `<circle cx="4" cy="4" r="2"/><circle cx="12" cy="5" r="2"/><circle cx="7" cy="12" r="2"/><path d="M5.6 5 6.5 10M6 4.3l4 .5M10.6 6.4 8.4 10.6"/>`},
	{ID: "changes", Name: "Changes", Path: "/changes", Key: "3", Sub: "Worker changes on their way from brief to verified",
		Icon: `<circle cx="4" cy="4" r="1.8"/><circle cx="4" cy="12" r="1.8"/><circle cx="12" cy="8" r="1.8"/><path d="M4 5.8v4.4M5.6 4.6c4 0 4.6 1.4 4.6 3.4"/>`},
	{ID: "messages", Name: "Messages", Path: "/messages", Key: "4", Sub: "Conversations between your agents",
		Icon: `<path d="M2 3h12v8H7l-3 2.5V11H2z"/>`},
}

func screenByID(id string) screenInfo {
	for _, s := range screens {
		if s.ID == id {
			return s
		}
	}
	return screens[0]
}

// page is everything one render needs. A screen's own view is set for it only.
type page struct {
	Screen       screenInfo
	Screens      []screenInfo
	Error        string
	Connected    bool
	Live         string
	EventsURL    string
	CTOOpen      bool
	CTOTitle     string
	CTOBack      string
	CTOEventsURL string
	CTO          *ctoView
	Sessions     *sessionsView
	Changes      *changesView
	Messages     *messagesView
	Graph        *graphView
}

// query is what a page names beyond its screen: the selected row, the Changes
// filter, whether the CTO panel is open, its older-history offset, and whether
// only the panel should be read for its own stream.
type query struct {
	Selected, Filter string
	CTO, OnlyCTO     bool
	H                int
}

// events is the stream address of a screen with its query.
func (q query) events(screen string) string {
	v := url.Values{"screen": {screen}}
	if q.Selected != "" {
		v.Set("s", q.Selected)
	}
	if q.Filter != "" {
		v.Set("f", q.Filter)
	}
	return "/events?" + v.Encode()
}

// ctoHref is a page of the console with the CTO panel open, at an older-history
// offset when h is above zero.
func (q query) ctoHref(s screenInfo, h int) string {
	v := url.Values{"cto": {"1"}}
	if q.Selected != "" {
		v.Set("s", q.Selected)
	}
	if q.Filter != "" && s.ID == "changes" {
		v.Set("f", q.Filter)
	}
	if h > 0 {
		v.Set("h", strconv.Itoa(h))
	}
	return s.Path + "?" + v.Encode()
}

// ctoEvents is the panel's own section of the events stream. It names the
// screen so the panel's older and newer links keep the owner where he is.
func (q query) ctoEvents(s screenInfo) string {
	v := url.Values{"section": {"cto"}, "screen": {s.ID}}
	if q.Selected != "" {
		v.Set("s", q.Selected)
	}
	if q.Filter != "" && s.ID == "changes" {
		v.Set("f", q.Filter)
	}
	if q.H > 0 {
		v.Set("h", strconv.Itoa(q.H))
	}
	return "/events?" + v.Encode()
}

// ---- Change rows ----

type changeRow struct {
	ID, Title, Project, Status, Age string
	Revision, Risk                  int
	Marks                           []string
	Aria                            string
	NeedsYou, Selected              bool
}

// ---- Sessions ----

type machineView struct {
	Name, Version, OS string
	Online            bool
	HasUsage          bool
	CPU               int
	Cores             int
	Mem               string
	MemPct            int
	Sessions          int
}

type sessionRow struct {
	ID, Name, Class, Machine, Project, Model, Task, Active string
	Selected                                               bool
	Worker                                                 bool
}

type sessionGroup struct {
	Label string
	Class string
	Rows  []sessionRow
}

type convoRow struct {
	Title, Last, Age string
	Count            int
}

type sessionDetail struct {
	Name, Class, Status, Task, Machine, Project, Model, Place, Charter string
	Convos                                                             []convoRow
}

type sessionsView struct {
	Machines []machineView
	Groups   []sessionGroup
	Shown    int
	Detail   *sessionDetail
}

// load reads what one screen shows. It never writes.
func (a *app) load(ctx context.Context, screen string, q query) page {
	p := page{Screen: screenByID(screen), Screens: screens}
	if p.Screen.ID == "changes" {
		q.Filter = changeFilterID(q.Filter)
	} else {
		q.Filter = ""
	}
	p.EventsURL = q.events(p.Screen.ID)
	p.CTOOpen = q.CTO
	p.CTOTitle = protocol.DefaultCTOTitle
	p.CTOBack = q.ctoHref(p.Screen, q.H)
	p.CTOEventsURL = q.ctoEvents(p.Screen)
	if q.CTO && a.src != nil {
		p.CTO = a.cto(ctx, q.H)
		if p.CTO.Title != "" {
			p.CTOTitle = p.CTO.Title
		}
		if p.CTO.Older > 0 {
			p.CTO.OlderHref = q.ctoHref(p.Screen, p.CTO.Older)
		}
		if q.H > 0 {
			p.CTO.LatestHref = q.ctoHref(p.Screen, 0)
		}
	}
	// The panel has its own section of the stream and reads only itself.
	if q.OnlyCTO {
		return p
	}
	if a.src == nil {
		p.Error = "Daemon not connected."
		return p
	}
	snap, err := a.screenSnapshot(ctx, p.Screen.ID)
	if err != nil {
		p.Error = err.Error()
		return p
	}
	p.Connected = snap.Connected
	if !snap.Connected {
		p.Error = "Daemon not connected."
		return p
	}
	var counts protocol.SessionCounts
	if raw, err := a.read(ctx, protocol.OpCounts, nil); err == nil {
		_ = json.Unmarshal(raw, &counts)
	}
	p.Live = fmt.Sprintf("live · %s · %s", plural(counts.MachinesOnline, "machine"), plural(counts.ConsoleSessions, "session"))
	switch p.Screen.ID {
	case "sessions":
		p.Sessions = a.sessions(ctx, snap, q.Selected)
	case "changes":
		p.Changes = a.changes(ctx, snap, q)
	case "messages":
		p.Messages = a.messages(ctx, snap, q.Selected)
	case "graph":
		p.Graph = a.graph(ctx, snap, q.Selected)
	}
	return p
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// ago is a short age: now, 4m, 3h, 2d.
func ago(t time.Time, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

var deliveryStepNames = []string{"accept", "review", "test", "approve", "merge", "test after merge", "release", "deploy", "verify"}

func stepOwnerLabel(o protocol.StepOwner) string {
	if o.Kind == protocol.OwnerNode {
		return firstNonEmpty(o.Node, "node")
	}
	return firstNonEmpty(o.Kind, "-")
}

// changeMarks gives the strip of a change: one mark for each step name it has.
// p passed, r running, w needs you, f failed, s left out, empty not yet.
func changeMarks(steps []protocol.StepStatus) (marks []string, aria string) {
	var words []string
	for _, name := range deliveryStepNames {
		var seen, active, passed int
		mark := ""
		for _, st := range steps {
			after := st.AfterMerge && st.Step == "test"
			if st.Step != strings.TrimSuffix(name, " after merge") || after != (name == "test after merge") {
				continue
			}
			seen++
			switch st.State {
			case protocol.StepStateSkipped:
				continue
			case protocol.StepStatePassed:
				passed++
			case protocol.StepStateFailed, protocol.StepStateClosed:
				mark = "f"
			case protocol.StepStateNeedsOwner:
				if mark != "f" {
					mark = "w"
				}
			case protocol.StepStateRunning:
				if mark == "" {
					mark = "r"
				}
			}
			active++
		}
		if seen == 0 {
			continue
		}
		switch {
		case mark != "":
		case active == 0:
			mark = "s"
		case passed == active:
			mark = "p"
		}
		marks = append(marks, mark)
		state := map[string]string{"p": "passed", "r": "running", "w": "needs you", "f": "failed", "s": "not needed", "": "not yet"}[mark]
		words = append(words, name+" "+state)
	}
	return marks, strings.Join(words, ", ")
}

// stepUnrecorded is the state of a step before merge that a merged change has
// no passing result for, as the TUI Delivery tab shows it.
const stepUnrecorded = "unrecorded"

// changeSteps is the steps of a change as the TUI Delivery tab reads them. Once
// a change is merged or done, a step before merge that never passed is not
// recorded: it is not a step the change still waits for.
func changeSteps(v protocol.ChangeView) []protocol.StepStatus {
	steps := append([]protocol.StepStatus(nil), v.Steps...)
	c := v.Change
	if c.State != protocol.ChangeDone && c.State != protocol.ChangeMerged && c.MergedCommit == "" {
		return steps
	}
	for i, st := range steps {
		switch st.Step {
		case "accept", "review", "test", "approve":
			if !st.AfterMerge && st.State != protocol.StepStatePassed && st.State != protocol.StepStateSkipped {
				steps[i].State, steps[i].Note = stepUnrecorded, "not recorded"
			}
		}
	}
	return steps
}

// currentStatus is the step a change is in: one that needs the owner or failed
// first, then the next ready one, then a running or waiting one.
func currentStatus(v protocol.ChangeView) (protocol.StepStatus, bool) {
	find := func(match func(protocol.StepStatus) bool) (protocol.StepStatus, bool) {
		for _, st := range v.Steps {
			if match(st) {
				return st, true
			}
		}
		return protocol.StepStatus{}, false
	}
	for _, want := range []string{protocol.StepStateNeedsOwner, protocol.StepStateFailed} {
		if st, ok := find(func(s protocol.StepStatus) bool { return s.State == want }); ok {
			return st, true
		}
	}
	if len(v.Next) > 0 {
		if st, ok := find(func(s protocol.StepStatus) bool { return s.StepRef == v.Next[0] }); ok {
			return st, true
		}
	}
	for _, want := range []string{protocol.StepStateRunning, protocol.StepStateWaiting} {
		if st, ok := find(func(s protocol.StepStatus) bool { return s.State == want }); ok {
			return st, true
		}
	}
	return protocol.StepStatus{}, false
}

func currentStep(v protocol.ChangeView) (name, state, owner string) {
	if st, ok := currentStatus(v); ok {
		return st.Step, st.State, stepOwnerLabel(st.Owner)
	}
	return "", "", ""
}

// changeStatusLine is the words of a row: the step, its state and its owner.
func changeStatusLine(v protocol.ChangeView) string {
	step, state, owner := currentStep(v)
	status := step
	switch state {
	case protocol.StepStateNeedsOwner:
		status += " needs you"
	case "":
	default:
		status += " " + state
	}
	if owner != "" && state != protocol.StepStateNeedsOwner {
		status += " · " + owner
	}
	return strings.TrimSpace(status)
}

// ---- Sessions ----

func sessionName(a protocol.AgentView) string {
	if n := firstNonEmpty(a.Profile.Name, a.AgentName); n != "" {
		return n
	}
	return strings.TrimSpace(a.Harness + " · " + firstNonEmpty(a.Repo.Project, a.Profile.Project, "session"))
}

func sessionProject(a protocol.AgentView) string {
	return firstNonEmpty(a.ProjectDisplayName, a.Repo.Project, a.Profile.Project, "-")
}

func sessionModel(a protocol.AgentView) string {
	if a.Model == "" {
		return a.Harness
	}
	return a.Harness + " · " + a.Model
}

// sessionState groups a session: needs you, working, idle or offline.
func sessionState(a protocol.AgentView) string {
	switch {
	case !a.Online:
		return "off"
	case a.Waiting != nil:
		return "you"
	case a.Status == "busy":
		return "busy"
	}
	return "idle"
}

func (a *app) sessions(ctx context.Context, snap operator.Snapshot, selected string) *sessionsView {
	now := time.Now()
	v := &sessionsView{}
	usage := map[string]protocol.UsageNode{}
	if raw, err := a.read(ctx, protocol.OpUsage, nil); err == nil {
		var res protocol.UsageResult
		if json.Unmarshal(raw, &res) == nil {
			if res.Gateway != nil {
				for _, n := range res.Gateway.Nodes {
					usage[n.NodeID] = n
				}
			}
			if res.Local != nil && res.NodeID != "" {
				usage[res.NodeID] = protocol.UsageNode{NodeID: res.NodeID, Usage: *res.Local}
			}
		}
	}
	perNode := map[string]int{}
	var agents []protocol.AgentView
	for _, ag := range snap.Agents {
		if protocol.IsOwner(ag.Harness) {
			continue
		}
		agents = append(agents, ag)
		perNode[ag.NodeID]++
	}
	for _, n := range snap.Nodes {
		m := machineView{Name: firstNonEmpty(n.Hostname, "machine"), Version: n.Version, OS: n.OS, Online: n.Online, Sessions: perNode[n.NodeID]}
		if u, ok := usage[n.NodeID]; ok && n.Online && u.AgeSeconds <= protocol.UsageStaleSeconds && u.Usage.Machine.Samples > 0 {
			mu := u.Usage.Machine
			m.HasUsage, m.CPU, m.Cores = true, int(mu.CPU+0.5), mu.Cores
			if mu.MemTotal > 0 {
				const gb = float64(1 << 30)
				m.Mem = fmt.Sprintf("%.1f/%.1fG", float64(mu.MemUsed)/gb, float64(mu.MemTotal)/gb)
				m.MemPct = int(float64(mu.MemUsed) / float64(mu.MemTotal) * 100)
			}
		}
		v.Machines = append(v.Machines, m)
	}
	sort.SliceStable(agents, func(i, j int) bool {
		if agents[i].LastActive != agents[j].LastActive {
			return agents[i].LastActive > agents[j].LastActive
		}
		return sessionName(agents[i]) < sessionName(agents[j])
	})
	groups := []sessionGroup{{Label: "Needs you", Class: "you"}, {Label: "Working", Class: "busy"}, {Label: "Idle", Class: "idle"}, {Label: "Offline", Class: "off"}}
	byClass := map[string]int{"you": 0, "busy": 1, "idle": 2, "off": 3}
	var chosen *protocol.AgentView
	for i := range agents {
		if agents[i].SessionID == selected {
			chosen = &agents[i]
		}
	}
	if chosen == nil {
		// Default to the first row on screen: the first of the first group.
		for _, class := range []string{"you", "busy", "idle", "off"} {
			for i := range agents {
				if sessionState(agents[i]) == class && chosen == nil {
					chosen = &agents[i]
				}
			}
		}
	}
	for _, ag := range agents {
		class := sessionState(ag)
		active := ""
		if ag.LastActive > 0 {
			active = ago(time.Unix(ag.LastActive, 0), now)
		}
		row := sessionRow{ID: ag.SessionID, Name: sessionName(ag), Class: class, Machine: firstNonEmpty(ag.Hostname, "-"), Project: sessionProject(ag),
			Model: sessionModel(ag), Task: ag.Profile.TaskLine(), Active: active, Selected: chosen != nil && chosen.SessionID == ag.SessionID,
			Worker: ag.Type == "worker"}
		g := &groups[byClass[class]]
		g.Rows = append(g.Rows, row)
		v.Shown++
	}
	for _, g := range groups {
		if len(g.Rows) > 0 {
			v.Groups = append(v.Groups, g)
		}
	}
	if chosen != nil {
		names := map[string]string{}
		for _, ag := range snap.Agents {
			names[ag.SessionID] = sessionName(ag)
		}
		v.Detail = a.sessionDetail(ctx, *chosen, now, names)
	}
	return v
}

// sessionPlace says where a session sits in the Sessions tree. The gateway
// computes the parent by one rule (decision 0193); the console only names it.
func sessionPlace(ag protocol.AgentView, names map[string]string) string {
	switch {
	case ag.NoParent != "":
		return "no parent: " + ag.NoParent
	case ag.Parent != "":
		return "under " + firstNonEmpty(names[ag.Parent], ag.Parent)
	}
	return "answers to you"
}

func (a *app) sessionDetail(ctx context.Context, ag protocol.AgentView, now time.Time, names map[string]string) *sessionDetail {
	d := &sessionDetail{Name: sessionName(ag), Class: sessionState(ag), Task: ag.Profile.TaskLine(), Machine: firstNonEmpty(ag.Hostname, "-"),
		Project: sessionProject(ag), Model: sessionModel(ag), Charter: ag.Charter.Purpose}
	d.Status = map[string]string{"you": "needs you", "busy": "busy", "idle": "idle", "off": "offline"}[d.Class]
	d.Place = sessionPlace(ag, names)
	if ag.AgentID != "" {
		if raw, err := a.read(ctx, protocol.OpGraphView, protocol.QueryGraph{Places: true, All: true}); err == nil {
			var g protocol.GraphView
			if json.Unmarshal(raw, &g) == nil {
				for _, n := range g.Nodes {
					if n.Ref != nil && n.Ref.Kind == "agent" && n.Ref.ID == ag.AgentID && n.Place != nil && n.Place.Team != "" {
						d.Place += " · team " + n.Place.Team
						if n.Place.TeamLead != "" {
							if n.Place.TeamLead == d.Name {
								d.Place += " · lead"
							} else {
								d.Place += " · lead " + n.Place.TeamLead
							}
						}
						if n.Place.Mode != "" {
							d.Place += " · " + strings.ReplaceAll(n.Place.Mode, "_", " ")
						}
					}
				}
			}
		}
	}
	if raw, err := a.read(ctx, protocol.OpConversations, protocol.QueryConversations{Limit: 50}); err == nil {
		var page protocol.ConversationPage
		if json.Unmarshal(raw, &page) == nil {
			for _, c := range page.Conversations {
				if c.From != ag.SessionID && c.To != ag.SessionID && c.WorkerSession != ag.SessionID {
					continue
				}
				title := strings.TrimSpace(firstNonEmpty(c.FromName, c.From) + " ↔ " + firstNonEmpty(c.ToName, c.To))
				if c.Task != "" {
					title = c.Task
				}
				d.Convos = append(d.Convos, convoRow{Title: title, Last: c.LastLine, Age: ago(c.At, now), Count: c.Count})
				if len(d.Convos) == 5 {
					break
				}
			}
		}
	}
	return d
}
