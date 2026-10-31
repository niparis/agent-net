package setup

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/daemon"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/quotaaxi"
)

var execSetupTUI = func(exe string) error {
	return syscall.Exec(exe, []string{exe, "tui"}, os.Environ())
}

type setupSummary struct {
	nodes          []protocol.NodeView
	nodesKnown     bool
	agents         []protocol.AgentView
	agentsKnown    bool
	projects       []protocol.ProjectView
	projectsKnown  bool
	graph          protocol.Graph
	graphKnown     bool
	protected      protocol.ProtectedProviders
	protectedKnown bool
	workersKnown   bool
	toolsKnown     bool
}

type setupPoint struct {
	label   string
	first   string
	details []string
}

func setupHarness(step string) string {
	switch step {
	case "claude code":
		return "claude"
	case "codex mcp":
		return "codex"
	case "pi extension":
		return "pi"
	case "opencode plugin":
		return "opencode"
	}
	return ""
}

func appendUnique(values []string, value string) []string {
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}

func (r *runner) finish(ctx context.Context) {
	cl := r.statusClient
	if cl != nil {
		defer func() {
			cl.Close()
			r.statusClient = nil
		}()
	}
	if r.failed != nil {
		r.failedEnd()
		return
	}
	summary := r.readSetupSummary(ctx, cl)
	host := r.statusInfo.Hostname
	if host == "" {
		host, _ = os.Hostname()
	}
	if host == "" {
		host = "this machine"
	}
	result := "agent-net is running on " + host
	if r.joining && summary.nodesKnown {
		others := len(summary.nodes) - 1
		if others == 1 {
			result += ", with 1 other machine"
		} else if others > 1 {
			result += fmt.Sprintf(", with %d other machines", others)
		}
	}
	fmt.Fprintln(r.Out, r.colors.good(result+"."))

	points := r.setupPoints(summary, host)
	if len(points) > 0 {
		fmt.Fprintln(r.Out)
		fmt.Fprintln(r.Out, r.colors.heading("What you have now"))
		for _, point := range points {
			r.writePoint(point)
		}
	}

	actions := r.firstActions(summary)
	if len(actions) > 0 {
		fmt.Fprintln(r.Out)
		fmt.Fprintln(r.Out, r.colors.attentionBold("Do this first"))
		for _, action := range actions {
			fmt.Fprintln(r.Out, action)
		}
	}
	fmt.Fprintln(r.Out)
	r.openQuestion()
}

func (r *runner) readSetupSummary(ctx context.Context, cl *client.Client) setupSummary {
	var summary setupSummary
	if cl == nil {
		return summary
	}
	summary.agents, summary.agentsKnown = waitSetupAgents(ctx, cl)
	var nodes protocol.NodesInfo
	summary.nodesKnown = setupCall(ctx, cl, "nodes", nil, &nodes)
	summary.nodes = nodes.Nodes
	var projects protocol.ProjectsResult
	summary.projectsKnown = setupCall(ctx, cl, "projects", protocol.QueryProjects{}, &projects)
	summary.projects = projects.Projects
	var graph protocol.GraphGot
	summary.graphKnown = setupCall(ctx, cl, protocol.OpGraphGet, nil, &graph)
	summary.graph = graph.Graph
	summary.protectedKnown = setupCall(ctx, cl, protocol.OpProtectedProvidersGet, nil, &summary.protected)
	var workers protocol.WorkersResult
	summary.workersKnown = setupCall(ctx, cl, "workers", protocol.WorkersQuery{}, &workers)
	var differences struct {
		ToLookAt int `json:"to_look_at"`
	}
	summary.toolsKnown = setupCall(ctx, cl, protocol.OpToolsDifferences, nil, &differences)
	return summary
}

func setupCall(ctx context.Context, cl *client.Client, method string, params any, out any) bool {
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	raw, err := cl.Call(callCtx, method, params)
	return err == nil && json.Unmarshal(raw, out) == nil
}

func waitSetupAgents(ctx context.Context, cl *client.Client) ([]protocol.AgentView, bool) {
	deadline := time.Now().Add(3 * time.Second)
	for {
		callCtx, cancel := context.WithTimeout(ctx, time.Second)
		raw, err := cl.Call(callCtx, "list", map[string]bool{"online_only": true})
		cancel()
		if err != nil {
			return nil, false
		}
		var result protocol.AgentsResult
		if json.Unmarshal(raw, &result) != nil {
			return nil, false
		}
		if len(result.Agents) > 0 {
			return result.Agents, true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return result.Agents, true
		}
		if remaining > 100*time.Millisecond {
			remaining = 100 * time.Millisecond
		}
		time.Sleep(remaining)
	}
}

func (r *runner) setupPoints(summary setupSummary, host string) []setupPoint {
	var points []setupPoint
	if summary.agentsKnown {
		points = append(points, r.seePoint(summary, host))
		if r.joining {
			if summary.nodesKnown && len(summary.nodes) > 1 {
				points = append(points, setupPoint{label: "2 Talk", first: "They can now ask your agents on " + strings.Join(r.otherHosts(summary), ", ") + ", and the reverse."})
			}
		} else if summary.nodesKnown {
			if len(summary.agents) == 0 {
				points = append(points, setupPoint{label: "2 Talk", first: "Your agents will be able to ask each other."})
			} else {
				points = append(points, setupPoint{label: "2 Talk", first: "Your agents can now ask each other. New sessions get the", details: []string{"tools; open ones get them after a restart."}})
			}
		}
	}
	if !r.joining && r.statusClient != nil {
		points = append(points, setupPoint{label: "3 CTO", first: "Your CTO runs the network for you. Ask it in plain words."})
		if summary.projectsKnown {
			points = append(points, r.mapPoint(summary))
		}
		if point, ok := r.workPoint(summary); ok {
			points = append(points, point)
		}
		if summary.toolsKnown {
			points = append(points, setupPoint{label: "6 Tools", first: "Skills and MCP servers are checked on every machine."})
		}
	} else if r.joining && r.statusClient != nil {
		if point, ok := r.workPoint(summary); ok {
			points = append(points, point)
		}
	}
	return points
}

func (r *runner) seePoint(summary setupSummary, host string) setupPoint {
	var sessions []protocol.AgentView
	for _, agent := range summary.agents {
		if agent.Online && agent.NodeID == r.statusInfo.NodeID {
			sessions = append(sessions, agent)
		}
	}
	if r.joining {
		if len(sessions) == 0 {
			return setupPoint{label: "1 See", first: "No agent sessions here yet. Start claude, codex, pi or opencode in", details: []string{"a project: it shows up here by itself."}}
		}
		text := sessionCounts(sessions)
		line := fmt.Sprintf("%d %s here: %s", len(sessions), plural(len(sessions), "session", "sessions"), text)
		if others := r.otherHosts(summary); len(others) > 0 {
			line += ". They show on " + strings.Join(others, ", ") + " too."
		} else {
			line += "."
		}
		return setupPoint{label: "1 See", first: line}
	}
	if len(sessions) == 0 && len(summary.agents) == 0 {
		return setupPoint{label: "1 See", first: "No agent sessions yet. Start claude, codex, pi or opencode in", details: []string{"a project: it shows up here by itself."}}
	}
	if len(sessions) == 0 {
		return setupPoint{label: "1 See", first: "No agent sessions on " + host + " yet. Start claude, codex, pi or opencode in", details: []string{"a project: it shows up here by itself."}}
	}
	line := fmt.Sprintf("%d %s on %s: %s", len(sessions), plural(len(sessions), "session", "sessions"), host, sessionCounts(sessions))
	busy, waiting := 0, 0
	for _, a := range sessions {
		if a.Status == "busy" {
			busy++
		}
		if a.Waiting != nil {
			waiting++
		}
	}
	if busy > 0 {
		line += fmt.Sprintf(". %d busy", busy)
	}
	if waiting > 0 {
		if busy > 0 {
			line += fmt.Sprintf(", %d waiting", waiting)
		} else {
			line += fmt.Sprintf(". %d waiting", waiting)
		}
	}
	line += "."
	point := setupPoint{label: "1 See", first: line}
	if summary.nodesKnown && len(summary.nodes) <= 1 {
		point.details = []string{"Your other machines appear when you add them."}
	}
	return point
}

func sessionCounts(sessions []protocol.AgentView) string {
	counts := map[string]int{}
	for _, session := range sessions {
		if session.Harness != "" {
			counts[session.Harness]++
		}
	}
	var parts []string
	for _, h := range []string{"claude", "codex", "pi", "opencode"} {
		if counts[h] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[h], h))
			delete(counts, h)
		}
	}
	var rest []string
	for h, n := range counts {
		rest = append(rest, fmt.Sprintf("%d %s", n, h))
	}
	sort.Strings(rest)
	parts = append(parts, rest...)
	return strings.Join(parts, ", ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (r *runner) otherHosts(summary setupSummary) []string {
	var hosts []string
	for _, node := range summary.nodes {
		if node.NodeID != r.statusInfo.NodeID && node.Hostname != "" {
			hosts = appendUnique(hosts, node.Hostname)
		}
	}
	sort.Strings(hosts)
	return hosts
}

func (r *runner) mapPoint(summary setupSummary) setupPoint {
	// The projects call lists a folder per machine, so it also carries worker
	// worktrees, Claude Code worktrees and temporary test folders under their
	// own names. The Map tab counts graph nodes, which are project identities:
	// worktrees share their main tree's identity and junk roots are hidden.
	// Use the same graph here, and keep the project list only for gateways
	// that cannot answer graph_get.
	projects := map[string]bool{}
	chartered := map[string]bool{}
	if summary.graphKnown {
		for _, node := range summary.graph.Nodes {
			if node.Name == "" || node.Name == "*" || node.Kind == "architect" {
				continue
			}
			projects[node.Name] = true
			if hasCharter(node.Charter) {
				chartered[node.Name] = true
			}
		}
	} else {
		for _, project := range summary.projects {
			if project.Name != "" {
				projects[project.Name] = true
			}
		}
	}
	if len(projects) == 0 {
		return setupPoint{label: "4 Map", first: "No projects yet. Each project you work in becomes one."}
	}
	if !summary.graphKnown {
		return setupPoint{label: "4 Map", first: fmt.Sprintf("%d projects seen.", len(projects))}
	}
	missing := len(projects) - len(chartered)
	switch {
	case missing == 0:
		return setupPoint{label: "4 Map", first: fmt.Sprintf("%d %s seen. All have charters.", len(projects), plural(len(projects), "project", "projects"))}
	case missing == len(projects):
		return setupPoint{label: "4 Map", first: fmt.Sprintf("%d projects seen. None has a charter yet: the CTO drafts", len(projects)), details: []string{"them with you."}}
	case missing == 1:
		return setupPoint{label: "4 Map", first: fmt.Sprintf("%d projects seen. One has no charter yet: the CTO drafts", len(projects)), details: []string{"it with you."}}
	default:
		return setupPoint{label: "4 Map", first: fmt.Sprintf("%d projects seen. %d have no charter yet: the CTO drafts", len(projects), missing), details: []string{"them with you."}}
	}
}

func hasCharter(c protocol.Charter) bool {
	return strings.TrimSpace(c.Purpose+c.Maturity+c.Accepts+c.Refuses+c.FeatureRequests+c.Consumers+c.Escalate+c.Needs+c.Notes) != ""
}

func (r *runner) workPoint(summary setupSummary) (setupPoint, bool) {
	if r.joining {
		if r.statusClient == nil {
			return setupPoint{}, false
		}
		harnesses := r.harnessNames(summary)
		text := r.statusInfo.Hostname
		if text == "" {
			text = "This machine"
		}
		if r.statusInfo.Launch {
			text += " takes workers."
		} else {
			text += " does not take workers."
		}
		if len(harnesses) > 0 {
			text += " Harnesses: " + strings.Join(harnesses, ", ") + "."
		}
		return setupPoint{label: "5 Work", first: text}, true
	}
	auth, err := quotaaxi.LoadAuth(daemon.ConfigDir())
	if err != nil || !summary.protectedKnown {
		return setupPoint{}, false
	}
	providers := make([]string, 0, len(auth.Providers))
	for _, provider := range auth.Providers {
		if provider.Status == "found" && provider.Provider != "" {
			providers = appendUnique(providers, provider.Provider)
		}
	}
	sort.Strings(providers)
	if len(providers) == 0 {
		if !summary.protectedKnown {
			return setupPoint{}, false
		}
		return setupPoint{label: "5 Work", first: "Nothing to protect yet."}, true
	}
	text := "Workers can use: " + strings.Join(providers, ", ") + "."
	if len(providers) > 1 {
		text = "Workers use " + strings.Join(providers, ", ") + "."
	}
	var protected []string
	var unprotected []string
	for _, provider := range providers {
		if !protectableProvider(provider) {
			continue
		}
		if providerProtected(provider, summary.protected) {
			protected = append(protected, provider)
		} else {
			unprotected = append(unprotected, provider)
		}
	}
	if len(protected) == 1 {
		text += " Your " + protected[0] + " plan is protected."
	} else if len(protected) > 1 {
		text += " Your " + strings.Join(protected, " and ") + " plans are protected."
	} else if len(unprotected) == 1 && unprotected[0] == "codex" && !summary.protected.Configured {
		text += " Nothing to protect yet."
	} else if len(unprotected) > 0 && summary.protectedKnown {
		if len(unprotected) == 1 {
			text += " Your " + unprotected[0] + " plan is not protected."
		} else {
			text += " Your " + strings.Join(unprotected, " and ") + " plans are not protected."
		}
	} else if summary.protectedKnown {
		text += " Nothing to protect yet."
	}
	return setupPoint{label: "5 Work", first: text}, true
}

func protectableProvider(provider string) bool {
	return provider == "claude" || provider == "codex" || provider == "anthropic" || provider == "openai"
}

func providerProtected(provider string, protected protocol.ProtectedProviders) bool {
	wanted := provider
	switch provider {
	case "claude":
		wanted = "anthropic"
	case "codex":
		wanted = "openai"
	}
	for _, p := range protected.Providers {
		if strings.EqualFold(p, wanted) || strings.EqualFold(p, provider) {
			return true
		}
	}
	return false
}

func (r *runner) harnessNames(summary setupSummary) []string {
	var names []string
	for _, h := range r.foundHarnesses {
		names = appendUnique(names, h)
	}
	if len(names) == 0 {
		for _, node := range summary.nodes {
			if node.NodeID == r.statusInfo.NodeID {
				for _, h := range node.Harnesses {
					names = appendUnique(names, h.Name)
				}
			}
		}
	}
	sortHarnessNames(names)
	return names
}

func sortHarnessNames(names []string) {
	order := map[string]int{"claude": 0, "codex": 1, "pi": 2, "opencode": 3}
	sort.Slice(names, func(i, j int) bool {
		a, oka := order[names[i]]
		b, okb := order[names[j]]
		if oka && okb {
			return a < b
		}
		if oka != okb {
			return oka
		}
		return names[i] < names[j]
	})
}

func (r *runner) firstActions(summary setupSummary) []string {
	if r.failed != nil {
		step := r.failedStep
		if step == "" {
			step = "setup"
		}
		command := "journalctl --user -u agent-net-daemon"
		if step == "gateway service" {
			command = "journalctl --user -u agent-net-gateway"
		}
		if runtime.GOOS == "darwin" {
			service := "daemon"
			if step == "gateway service" {
				service = "gateway"
			}
			command = "tail -n 100 ~/Library/Logs/agent-net-" + service + ".log"
		}
		if step != "daemon service" && step != "gateway service" {
			return []string{r.colors.attention("!") + " Fix the failed step, then run agent-net setup again."}
		}
		return []string{
			r.colors.attention("!") + " See why: " + r.colors.label(command),
			"  Then run " + r.colors.label("agent-net setup") + " again.",
		}
	}
	groups := map[string]int{}
	for _, session := range r.statusInfo.Sessions {
		if session.ToolsOld && session.Harness != "" {
			groups[session.Harness]++
		}
	}
	if len(groups) == 0 {
		for _, session := range summary.agents {
			if session.NodeID == r.statusInfo.NodeID && session.ToolsOld && session.Harness != "" {
				groups[session.Harness]++
			}
		}
	}
	if len(groups) == 0 {
		return nil
	}
	var harnesses []string
	count := 0
	for harness, n := range groups {
		harnesses = append(harnesses, harness)
		count += n
	}
	sortHarnessNames(harnesses)
	var action string
	if len(harnesses) == 1 {
		action = fmt.Sprintf("Restart %d %s %s so they get the agent-net tools.", count, harnesses[0], plural(count, "session", "sessions"))
	} else {
		action = fmt.Sprintf("Restart %d sessions (%s) so they get the agent-net tools.", count, strings.Join(harnesses, ", "))
	}
	return []string{r.colors.attention("!") + " " + action}
}

func (r *runner) failedEnd() {
	step := r.failedStep
	if step == "" {
		step = "setup"
	}
	fmt.Fprintf(r.Out, "\n%s\n", r.colors.failureBold("Setup did not finish: "+step+" failed."))
	fmt.Fprintln(r.Out)
	fmt.Fprintln(r.Out, r.colors.attentionBold("Do this first"))
	for _, action := range r.firstActions(setupSummary{}) {
		fmt.Fprintln(r.Out, action)
	}
}

func (r *runner) writePoint(point setupPoint) {
	label := r.colors.label(point.label)
	fmt.Fprint(r.Out, label, strings.Repeat(" ", max(1, 8-len(point.label))))
	fmt.Fprintln(r.Out, point.first)
	for _, detail := range point.details {
		fmt.Fprintln(r.Out, "        "+r.colors.faint(detail))
	}
}

func (r *runner) openQuestion() {
	if !r.interactive {
		fmt.Fprintln(r.Out, "Open agent-net: run "+r.colors.label("agent-net"))
		return
	}
	tty, err := openTTY()
	if err != nil {
		fmt.Fprintln(r.Out, "Open agent-net: run "+r.colors.label("agent-net"))
		return
	}
	defer tty.Close()
	question := "Open agent-net now? "
	acceptDefault := true
	if r.joining {
		question = "Open agent-net here? "
		acceptDefault = false
	}
	choice := "[Y/n]"
	if !acceptDefault {
		choice = "[y/N]"
	}
	fmt.Fprintln(r.Out, question+r.colors.label(choice))
	answer := readPromptLine(bufio.NewReader(tty))
	answer = strings.ToLower(strings.TrimSpace(answer))
	open := answer == "y" || answer == "yes" || (answer == "" && acceptDefault)
	if !open {
		return
	}
	if err := execSetupTUI(r.Exe); err != nil {
		r.hint("could not open the TUI: %v; run agent-net", err)
	}
}

func readPromptLine(reader *bufio.Reader) string {
	for {
		line, err := reader.ReadString('\n')
		if strings.HasPrefix(line, "\x1b") {
			if err != nil {
				return ""
			}
			continue
		}
		return line
	}
}
