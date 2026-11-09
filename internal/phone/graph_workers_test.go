package phone

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

// richOperator is the console fake with the graph read, quota read and worker
// log a full gateway sends. The Desk and Sessions fixtures stay as they are.
type richOperator struct{ *consoleOperator }

func newRichOperator() *richOperator { return &richOperator{consoleOperator: newConsoleOperator()} }

func richBool(v bool) *bool { return &v }

func richRef(kind, id string) *protocol.GraphRef { return &protocol.GraphRef{Kind: kind, ID: id} }

func (f *richOperator) Snapshot(ctx context.Context) (operator.Snapshot, error) {
	snap, err := f.consoleOperator.Snapshot(ctx)
	if err != nil {
		return snap, err
	}
	now := time.Now()
	snap.WorkerProfiles = []protocol.WorkerProfile{
		{Name: "sol high", Harness: "codex", Model: "gpt-6.1-sol", Effort: "high", Available: richBool(true)},
		{Name: "deepseek flash", Harness: "opencode", Model: "deepseek-v4.1-flash", Available: richBool(true)},
		{Name: "luna max", Harness: "opencode", Model: "opencode-go/luna", Effort: "max", Available: richBool(true)},
	}
	snap.Scores = []protocol.ModelScore{
		{Match: "gpt-6.1-sol", Effort: "high", Score: 46.1, Source: "test"},
		{Match: "deepseek-v4.1-flash", Score: 43.2, Source: "test"},
	}
	snap.Nodes = append([]protocol.NodeView(nil), snap.Nodes...)
	for i := range snap.Nodes {
		if snap.Nodes[i].NodeID == "n_secretnode1" {
			snap.Nodes[i].Tracks = []protocol.TrackRecord{
				{Harness: "codex", Model: "gpt-6.1-sol:high", Runs: 31, Complete: 28},
				{Harness: "opencode", Model: "deepseek-v4.1-flash", Runs: 18, Complete: 15},
			}
		}
	}
	snap.WorkerRotation = protocol.WorkerRotation{Levels: map[string]*protocol.WorkerRotationLevel{
		"normal": {Current: map[string]float64{"codex\x00gpt-6.1-sol:high": 1, "opencode\x00deepseek-v4.1-flash": 1}},
		"hard":   {Current: map[string]float64{"codex\x00gpt-6.1-sol:high": 1}},
	}}
	snap.ProtectedProviders = protocol.ProtectedProviders{Providers: []string{"claude"}}
	snap.WorkerLog = []protocol.WorkerLogEntry{
		{SessionID: "s_worker", NodeID: "n_secretnode1", Hostname: "alpha-box", Project: "proj-busy", Harness: "codex", Model: "gpt-6.1-sol:high",
			Title: "Fix the flaky test", Status: "running", StartedAt: now.Add(-11 * time.Minute).Unix(), RunSeconds: 660},
	}
	snap.Agents = append(snap.Agents,
		protocol.AgentView{SessionID: "s_worker", AgentID: "a_worker", NodeID: "n_secretnode1", Hostname: "alpha-box", Harness: "codex", Model: "gpt-6.1-sol:high",
			Online: true, Status: "busy", Profile: protocol.Profile{Name: "worker 2", Task: "Fix the flaky test"},
			Worker: &protocol.WorkerInfo{Owner: "s_wait", Project: "proj-busy", Task: "Fix the flaky test", Tokens: 412000, StartedAt: now.Add(-11 * time.Minute).Unix()}},
		protocol.AgentView{SessionID: "s_cto", NodeID: "n_secretnode1", Hostname: "alpha-box", Harness: "cto", Online: true, Status: "busy", Profile: protocol.Profile{Name: "CTO"}},
		protocol.AgentView{SessionID: "s_chief", NodeID: "n_secretnode1", Hostname: "alpha-box", Harness: "codex", Model: "gpt-6.1-sol", Online: true, Status: "idle", Profile: protocol.Profile{Name: "chief of staff"}},
	)
	return snap, nil
}

func (f *richOperator) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if raw, ok := snapshotFixtureRead(ctx, f, method); ok {
		f.mu.Lock()
		f.reads[method]++
		f.mu.Unlock()
		return raw, nil
	}
	switch method {
	case protocol.OpGraphView, protocol.OpUsage, protocol.OpChiefGet:
		f.mu.Lock()
		f.reads[method]++
		f.mu.Unlock()
	}
	switch method {
	case protocol.OpGraphView:
		return json.Marshal(richGraphView())
	case protocol.OpUsage:
		return json.Marshal(richUsage())
	case protocol.OpChiefGet:
		return json.Marshal(protocol.ChiefStatus{SessionID: "s_chief", Name: "chief of staff", Online: true, Model: "gpt-6.1-sol"})
	}
	return f.consoleOperator.Call(ctx, method, params)
}

func richGraphView() protocol.GraphView {
	team := richRef("team", "forge-team")
	busy, wait, idle := richRef("agent", "a_busy"), richRef("agent", "a_wait"), richRef("agent", "a_idle")
	other := richRef("repo", "proj-other")
	place := &protocol.NodePlace{Team: "forge-team", TeamLead: "lead-agent", Mode: "charter_first"}
	return protocol.GraphView{
		Nodes: []protocol.Node{
			{Ref: team, Name: "forge-team", Kind: "team", Lead: richRef("agent", "a_lead"), AuthorityMode: "charter_first", Active: richBool(true),
				Charter: protocol.Charter{Purpose: "Builds the forge."}, Place: place},
			{Ref: busy, Name: "busy-agent", Kind: "agent", Place: place},
			{Ref: wait, Name: "waiting-agent", Kind: "agent", Place: place},
			{Ref: idle, Name: "idle-agent", Kind: "agent"},
			{Ref: other, Name: "other-project", Kind: "repo"},
			{Name: protocol.CTONodeName, Kind: "cto"},
		},
		Edges: []protocol.Edge{
			{FromRef: busy, ToRef: team, From: "a_busy", To: "forge-team", Kind: "member_of"},
			{FromRef: wait, ToRef: team, From: "a_wait", To: "forge-team", Kind: "member_of"},
			{FromRef: busy, ToRef: other, From: "a_busy", To: "proj-other", Kind: "depends", Rule: "normal"},
			{From: "/delivery/caller", FromRef: richRef("special", "/delivery/caller"), To: "busy-agent", ToRef: busy, Kind: "accepts"},
			{From: "merger-node", To: "busy-agent", ToRef: busy, Kind: "tests"},
		},
	}
}

func richUsage() protocol.UsageResult {
	now := time.Now()
	return protocol.UsageResult{Gateway: &protocol.UsageAnswer{Nodes: []protocol.UsageNode{
		{NodeID: "n_secretnode1", Hostname: "alpha-box", AgeSeconds: 3, Usage: protocol.UsageReport{
			Machine: protocol.MachineUsage{Cores: 8, CPU: 34, MemUsed: 8 << 30, MemTotal: 16 << 30, Samples: 5},
			Quotas: &protocol.Quotas{GeneratedAt: now.Unix(), Providers: []protocol.QuotaProvider{
				{Provider: "claude", Plan: "max", Windows: []protocol.QuotaWindow{{ID: "5h", Label: "5h", PercentUsed: 62, Pace: "ahead", ResetsAt: now.Add(2*time.Hour + 10*time.Minute).Unix()}}},
				{Provider: "codex", Plan: "pro", Windows: []protocol.QuotaWindow{{ID: "week", Label: "week", PercentUsed: 26, Pace: "behind", ResetsAt: now.Add(72 * time.Hour).Unix()}}},
			}},
		}},
	}}}
}

func richPage(t *testing.T, path string) string {
	t.Helper()
	w := httptest.NewRecorder()
	New(newRichOperator()).ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	if w.Code != 200 {
		t.Fatalf("GET %s: %d", path, w.Code)
	}
	return w.Body.String()
}

func TestGraphRendersTeamsNodesBothEdgesAndLegend(t *testing.T) {
	body := richPage(t, "/graph")
	mustContain(t, "graph", body,
		"<svg", `class="team"`, "team forge-team", "lead lead-agent", "charter first",
		"busy-agent", "waiting-agent", "other-project", "answers to you",
		"owner", "CTO", "chief of staff", "?s=agent%3aa_busy",
		`class="edge dep"`, `class="edge msg"`, "depends on", "message in the last hour")
	// The staff sits above the team boxes, and the state is on each node.
	mustContain(t, "staff", body, `class="node you"`, `class="node busy"`, `class="node off"`)
	mustNotContain(t, "graph", body, "n_secretnode", "Edit place", "<form", "<textarea")
}

func TestGraphSelectionFillsTheDetailCard(t *testing.T) {
	body := richPage(t, "/graph?s=agent:a_busy")
	i := len(body)
	if j := indexOf(body, `id="detail"`); j >= 0 {
		i = j
	} else {
		t.Fatal("no detail card")
	}
	detail := body[i:]
	mustContain(t, "detail", detail,
		"busy-agent", "busy", "codex", "gpt-6.1", "team forge-team", "lead lead-agent", "charter first",
		"alpha-box", "test: merger-node", "accept: caller", "Charter purpose of the busy agent.")
	// A node that is not in a team says where it reports.
	loose := richPage(t, "/graph?s=proj-other")
	mustContain(t, "loose detail", loose, "other-project", "answers to you")
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestWorkersRendersSubscriptionsPicksAndRunning(t *testing.T) {
	body := richPage(t, "/workers")
	mustContain(t, "subscriptions", body, "Subscriptions", "claude", "max", "5h", "38%", "protected", "ahead", "resets in",
		"codex", "pro", "week", "74%", "behind")
	mustContain(t, "picks", body, "Model picks", "gpt-6.1-sol", "codex", "46.1", "deepseek-v4.1-flash", "43.2",
		">T<", ">S<", ">N<", ">H<", ">V<", `class="lv on"`, "31", "90%", "unused")
	mustContain(t, "running", body, "Running now", "worker 2", "Fix the flaky test", "11m", "412k", "Cost")
	mustNotContain(t, "workers", body, "n_secretnode", "Protect a provider", "Start a worker", ">Stop<", "<form", "<textarea")
}

func TestGraphAndWorkersAreReadOnly(t *testing.T) {
	f := newRichOperator()
	for _, path := range []string{"/graph", "/graph?s=agent:a_busy", "/workers"} {
		body := richPage(t, path)
		mustNotContain(t, path, body, "<form", "<textarea", "Edit place", ">Protect<", ">Start<", ">Stop<")
	}
	for _, path := range []string{"/workers/start", "/workers/stop", "/worker/start", "/graph/edit", "/graph/edge", "/graph/node"} {
		if code := post(t, f.consoleOperator, path, nil); code != 404 && code != 405 {
			t.Errorf("POST %s: %d, want 404 or 405", path, code)
		}
	}
	for _, kind := range []string{"worker_start", "worker_stop", "protect", "unprotect", "place_edit", "graph_edit", "start", "stop"} {
		if code := post(t, f.consoleOperator, "/action", url.Values{"kind": {kind}, "id": {"x"}, "action": {"y"}}); code != 400 {
			t.Errorf("kind %s: %d, want 400", kind, code)
		}
	}
}

func TestGraphAndWorkersReRenderOnEvents(t *testing.T) {
	for _, tc := range []struct {
		path string
		want []string
	}{
		{"/events?screen=graph&s=agent:a_busy", []string{"event: update", "busy-agent", "team forge-team", `id="detail"`, `hx-swap-oob`}},
		{"/events?screen=workers", []string{"event: update", "Subscriptions", "Model picks", "worker 2", `hx-swap-oob`}},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		w := &twoFlushes{ResponseRecorder: httptest.NewRecorder(), cancel: cancel, count: 1}
		New(newRichOperator()).ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil).WithContext(ctx))
		cancel()
		mustContain(t, tc.path, w.Body.String(), tc.want...)
	}
}

func TestGraphAndWorkersLoadOnlyWhatTheyShow(t *testing.T) {
	f := newRichOperator()
	richServe := func(path string) {
		w := httptest.NewRecorder()
		New(f).ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	}
	richServe("/graph")
	f.mu.Lock()
	for _, m := range []string{protocol.OpUsage, protocol.OpChangesOwnerFind, protocol.OpConversationMessages, protocol.OpChiefSorts, protocol.OpDeskLog} {
		if f.reads[m] != 0 {
			t.Errorf("the Graph screen read %s", m)
		}
	}
	for _, m := range []string{protocol.OpGraphView, protocol.OpConversations, protocol.OpChiefGet} {
		if f.reads[m] == 0 {
			t.Errorf("the Graph screen did not read %s", m)
		}
	}
	f.reads = map[string]int{}
	f.mu.Unlock()
	richServe("/workers")
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range []string{protocol.OpGraphView, protocol.OpConversations, protocol.OpChangesOwnerFind, protocol.OpChiefSorts, protocol.OpDeskLog} {
		if f.reads[m] != 0 {
			t.Errorf("the Workers screen read %s", m)
		}
	}
	if f.reads[protocol.OpUsage] == 0 {
		t.Error("the Workers screen did not read the quota report")
	}
}

func TestGraphLayoutKeepsBoxesAndCellsApart(t *testing.T) {
	v := &graphView{
		Staff: []*graphNodeView{{Key: "owner"}, {Key: "cto"}, {Key: "chief"}},
		Boxes: []graphBoxView{
			{Title: "team a", Nodes: []*graphNodeView{{Key: "a1"}, {Key: "a2"}, {Key: "a3"}}},
			{Title: "answers to you", Nodes: []*graphNodeView{{Key: "b1"}, {Key: "b2"}}},
		},
	}
	layoutGraph(v)
	for i := range v.Boxes {
		b := v.Boxes[i]
		if b.X < 0 || b.Y < 0 || b.W <= graphCellW || b.H <= graphHeader {
			t.Fatalf("box %d has no room: %+v", i, b)
		}
		for _, n := range b.Nodes {
			if n.X-10 < b.X || n.X+10 > b.X+b.W || n.Y-12 < b.Y+graphHeader || n.Y+16 > b.Y+b.H {
				t.Fatalf("node %s sits outside box %d: %+v", n.Key, i, n)
			}
		}
		for j := i + 1; j < len(v.Boxes); j++ {
			o := v.Boxes[j]
			if b.X+b.W > o.X && o.X+o.W > b.X && b.Y+b.H > o.Y && o.Y+o.H > b.Y {
				t.Fatalf("boxes %d and %d overlap", i, j)
			}
		}
	}
	// Cells in a column are one full cell apart; no two circles share a spot.
	seen := map[[2]int]bool{}
	for _, b := range v.Boxes {
		for _, n := range b.Nodes {
			p := [2]int{n.X, n.Y}
			if seen[p] {
				t.Fatalf("two nodes at %v", p)
			}
			seen[p] = true
		}
	}
}

func TestGraphEdgesStopAtTheNodeCircle(t *testing.T) {
	pos := map[string][2]int{"a": {100, 100}, "b": {300, 100}}
	if got := graphEdge("a", "b", pos, false, "").D; !strings.Contains(got, "M107 100") || !strings.Contains(got, "293 100") {
		t.Fatalf("horizontal edge does not stop at r=7: %s", got)
	}
	pos = map[string][2]int{"a": {100, 100}, "b": {100, 300}}
	if got := graphEdge("a", "b", pos, true, "").D; !strings.Contains(got, "M100 107") || !strings.Contains(got, "100 293") {
		t.Fatalf("vertical edge does not stop at r=7: %s", got)
	}
}

func TestGraphTextHasAHaloAndTheTitlesPaintOverEdges(t *testing.T) {
	css, err := fs.ReadFile(files, "static/console.css")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, "graph halo", string(css),
		"paint-order: stroke", "stroke-width: 4px", "stroke-linejoin: round",
		"stroke: var(--panel)", ".graph .team text, .graph .boxnodes text { stroke: var(--sunk); }")
	body := richPage(t, "/graph")
	edge := strings.Index(body, `class="edge dep"`)
	title := strings.Index(body, ">team forge-team<")
	if edge < 0 || title < 0 || title < edge {
		t.Fatalf("the team title must paint after the edges: edge %d title %d", edge, title)
	}
	mustContain(t, "box nodes", body, `<g class="boxnodes">`)
}
