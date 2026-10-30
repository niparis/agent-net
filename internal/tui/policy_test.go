package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
)

func keyP() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")} }

// settlePolicy runs the command of an Update (a batch of previews) and feeds
// the preview answers back.
func runPreviews(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	switch msg := cmd().(type) {
	case policyPreviewMsg:
		return send(m, msg)
	case tea.BatchMsg:
		for _, c := range msg {
			if c != nil {
				if pm, ok := c().(policyPreviewMsg); ok {
					m = send(m, pm)
				}
			}
		}
	}
	return m
}

// resolver is a fake gateway: it resolves "deepseek 4.1" and "sol" and refuses
// anything else.
func resolver(saved *[]protocol.SessionPolicyRequest) func(protocol.SessionPolicyRequest) (protocol.SessionPolicyResult, error) {
	return func(q protocol.SessionPolicyRequest) (protocol.SessionPolicyResult, error) {
		v := &protocol.DelegationView{DelegationPolicy: q.Policy, Scope: q.Scope, ModelList: true}
		switch q.Policy.CoderModel {
		case "":
		case "deepseek 4.1":
			v.CoderResolved = "deepseek/deepseek-v4.1-flash"
		default:
			v.CoderError = "no model in the list matches " + q.Policy.CoderModel
		}
		if q.Policy.MainModel == "sol" {
			v.MainResolved = "sol/sol-6.1"
		}
		if !q.DryRun {
			*saved = append(*saved, q)
		}
		return protocol.SessionPolicyResult{SessionID: q.SessionID, Saved: !q.DryRun, Effective: v}, nil
	}
}

func TestPolicyFormResolvesBeforeSavingAndSaves(t *testing.T) {
	m, src := workerModel(120, 40)
	var saved []protocol.SessionPolicyRequest
	src.policyFn = resolver(&saved)
	const target = "s_0000000000000003" // the OpenCode session of the fixture
	m = selectID(m, target)
	m, cmd := press(m, keyP())
	if m.mode != modePolicy {
		t.Fatalf("p must open the form, mode %v", m.mode)
	}
	m = runPreviews(t, m, cmd)
	// Delegate: right x2 = native.
	for i := 0; i < 2; i++ {
		m, cmd = press(m, tea.KeyMsg{Type: tea.KeyRight})
		m = runPreviews(t, m, cmd)
	}
	if m.pol.delegateValue() != protocol.DelegateNative {
		t.Fatalf("delegate %q", m.pol.delegateValue())
	}
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyEnter}) // to Coder
	m = runPreviews(t, m, cmd)
	m = typeText(m, "deepseek 4.1")
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyEnter}) // to Main
	m = runPreviews(t, m, cmd)
	m = typeText(m, "sol")
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyEnter}) // to Workers
	m = runPreviews(t, m, cmd)
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyEnter}) // to Scope
	m = runPreviews(t, m, cmd)
	screen := strings.Join(lines(m.View()), "\n")
	for _, want := range []string{"Delegation policy", "native", "-> deepseek/deepseek-v4.1-flash", "-> sol/sol-6.1", "session"} {
		if !strings.Contains(screen, want) {
			t.Errorf("form lacks %q:\n%s", want, screen)
		}
	}
	if len(saved) != 0 {
		t.Fatal("nothing may be saved before the last Enter")
	}
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	msg := cmd()
	if _, ok := msg.(policySavedMsg); !ok {
		t.Fatalf("save gave %T", msg)
	}
	m = send(m, msg)
	if len(saved) != 1 || m.mode != modeNormal || m.notice.err {
		t.Fatalf("saved %v mode %v notice %+v", saved, m.mode, m.notice)
	}
	want := protocol.SessionPolicyRequest{SessionID: target, Scope: protocol.ScopeSession,
		Policy: protocol.DelegationPolicy{Delegate: protocol.DelegateNative, CoderModel: "deepseek 4.1", MainModel: "sol"}}
	if saved[0] != want {
		t.Errorf("saved %+v, want %+v", saved[0], want)
	}
}

func TestPolicyFormShowsResolutionErrorsAndRefusesAnEmptyCoder(t *testing.T) {
	m, src := workerModel(120, 40)
	var saved []protocol.SessionPolicyRequest
	src.policyFn = resolver(&saved)
	m = selectID(m, "s_0000000000000003")
	m, cmd := press(m, keyP())
	m = runPreviews(t, m, cmd)
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyRight})
	m = runPreviews(t, m, cmd)
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyRight})
	m = runPreviews(t, m, cmd)
	// Jump to Scope with no coder: save is refused with a plain line.
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.pol.err != "Name the coder model." || len(saved) != 0 {
		t.Fatalf("err %q saved %v", m.pol.err, saved)
	}
	m = typeText(m, "nothing like it")
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyDown})
	m = runPreviews(t, m, cmd)
	if s := strings.Join(lines(m.View()), "\n"); !strings.Contains(s, "! no model in the list matches nothing like it") {
		t.Errorf("resolution error not shown:\n%s", s)
	}
}

func TestPolicyGatewayRefusalKeepsTheFormOpen(t *testing.T) {
	m, src := workerModel(120, 40)
	src.policyFn = func(q protocol.SessionPolicyRequest) (protocol.SessionPolicyResult, error) {
		if q.DryRun {
			return protocol.SessionPolicyResult{}, nil
		}
		return protocol.SessionPolicyResult{}, errors.New("coder model: no model matches")
	}
	m = selectID(m, "s_0000000000000003")
	m, _ = press(m, keyP())
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRight}) // off needs no coder
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyDown})
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = send(m, cmd())
	if m.mode != modePolicy || !strings.Contains(m.pol.err, "no model matches") {
		t.Fatalf("mode %v err %q", m.mode, m.pol.err)
	}
	// esc closes without saving.
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != modeNormal || m.notice.text != "Policy unchanged." {
		t.Errorf("esc: %v %q", m.mode, m.notice.text)
	}
}

func TestPolicyShowsInTheTableAndDetailAndHelp(t *testing.T) {
	m, src := workerModel(140, 40)
	const target = "s_0000000000000003"
	for i := range src.snap.Agents {
		if src.snap.Agents[i].SessionID == target {
			src.snap.Agents[i].Delegation = &protocol.DelegationView{
				DelegationPolicy: protocol.DelegationPolicy{Delegate: protocol.DelegateNative, CoderModel: "deepseek 4.1", MainModel: "sol"},
				Scope:            protocol.ScopeProject, CoderResolved: "deepseek/deepseek-v4.1-flash", MainResolved: "sol/sol-6.1", ModelList: true}
		}
	}
	m = refresh(m, src)
	m = selectID(m, target)
	screen := strings.Join(lines(m.View()), "\n")
	for _, want := range []string{"POLICY", "sub", "native -> deepseek/deepseek-v4.1-flash (project)", "sol/sol-6.1", "p: change"} {
		if !strings.Contains(screen, want) {
			t.Errorf("screen lacks %q:\n%s", want, screen)
		}
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if !strings.Contains(strings.Join(lines(m.View()), "\n"), "delegation policy") {
		t.Error("help lacks the p key")
	}
}

func TestPolicyNotesForHarnessesThatCannotDoPart(t *testing.T) {
	m, _ := workerModel(120, 40)
	view := &protocol.DelegationView{DelegationPolicy: protocol.DelegationPolicy{Delegate: protocol.DelegateNative, CoderModel: "x", MainModel: "y"}}
	cases := map[string]string{
		"pi":     "Pi has no subagents: native uses workers",
		"claude": "switch the model yourself once",
		"codex":  "the policy is not applied",
	}
	for harness, want := range cases {
		a := protocol.AgentView{Harness: harness, NodeID: "n_none"}
		got := strings.Join(m.policySupport(a, view), " | ")
		if !strings.Contains(got, want) {
			t.Errorf("%s: %q lacks %q", harness, got, want)
		}
	}
}

func TestPolicyKeyWithNothingSelected(t *testing.T) {
	e := New(&fakeSource{snap: Snapshot{Connected: true}})
	e = send(e, tea.WindowSizeMsg{Width: 100, Height: 24})
	e, _ = press(e, keyP())
	if e.mode != modeNormal || !e.notice.err {
		t.Errorf("empty selection: %v %+v", e.mode, e.notice)
	}
}

func TestPolicyFormGolden(t *testing.T) {
	m, src := workerModel(120, 34)
	var saved []protocol.SessionPolicyRequest
	src.policyFn = resolver(&saved)
	m = selectID(m, "s_0000000000000003")
	m, cmd := press(m, keyP())
	m = runPreviews(t, m, cmd)
	for i := 0; i < 2; i++ {
		m, cmd = press(m, tea.KeyMsg{Type: tea.KeyRight})
		m = runPreviews(t, m, cmd)
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = typeText(m, "deepseek 4.1")
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = runPreviews(t, m, cmd)
	golden(t, "view_120x34_policy_form", m.View())
}

// The Workers line has the three levels and a default. Choosing one moves the
// scope to project, and the level is saved with the policy.
func TestPolicyFormWorkersLine(t *testing.T) {
	m, src := workerModel(120, 40)
	var saved []protocol.SessionPolicyRequest
	src.policyFn = resolver(&saved)
	m = selectID(m, "s_0000000000000003")
	m, cmd := press(m, keyP())
	m = runPreviews(t, m, cmd)
	for i := 0; i < 3; i++ {
		m, _ = press(m, tea.KeyMsg{Type: tea.KeyDown}) // Delegate, Coder, Main -> Workers
	}
	if m.pol.cur != pfWorkers {
		t.Fatalf("cursor on %v, want Workers", m.pol.cur)
	}
	if s := strings.Join(lines(m.View()), "\n"); !strings.Contains(s, "Workers") || !strings.Contains(s, "sandboxed (default)") {
		t.Errorf("the Workers line shows no default:\n%s", s)
	}
	var seen []string
	for i := 0; i < 4; i++ {
		m, cmd = press(m, tea.KeyMsg{Type: tea.KeyRight})
		m = runPreviews(t, m, cmd)
		seen = append(seen, workersChoices[m.pol.workers])
	}
	if strings.Join(seen, ",") != "sandboxed,no-network,read-only," {
		t.Errorf("choices: %v", seen)
	}
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyRight}) // sandboxed
	m = runPreviews(t, m, cmd)
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyRight}) // no-network
	m = runPreviews(t, m, cmd)
	if m.pol.scope != protocol.ScopeProject {
		t.Errorf("scope %q: a workers level is per project", m.pol.scope)
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEnter}) // to Scope
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	msg, ok := cmd().(policySavedMsg)
	if !ok {
		t.Fatalf("save gave %T", msg)
	}
	m = send(m, msg)
	if len(saved) != 1 || saved[0].Policy.Workers != protocol.WorkersNoNetwork || saved[0].Scope != protocol.ScopeProject {
		t.Errorf("saved %+v", saved)
	}
}

// A level at session scope is refused in the form, before the gateway.
func TestPolicyFormRefusesWorkersAtSessionScope(t *testing.T) {
	m, src := workerModel(120, 40)
	var saved []protocol.SessionPolicyRequest
	src.policyFn = resolver(&saved)
	m = selectID(m, "s_0000000000000003")
	m, _ = press(m, keyP())
	m.pol.workers = 3
	m.pol.scope = protocol.ScopeSession
	m.pol.cur = pfScope
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.pol.err, "per project") || len(saved) != 0 {
		t.Errorf("err %q saved %v", m.pol.err, saved)
	}
}

// The detail pane of a worker shows its permission level, and says when the
// harness does not apply it yet.
func TestDetailShowsWorkerPermissions(t *testing.T) {
	m, src := workerModel(140, 50)
	var worker protocol.AgentView
	for _, a := range src.snap.Agents {
		if a.Worker != nil {
			worker = a
		}
	}
	if worker.SessionID == "" {
		t.Fatal("the fixture has no worker")
	}
	for _, harness := range []string{"codex", "opencode"} {
		for i := range src.snap.Agents {
			if src.snap.Agents[i].SessionID == worker.SessionID {
				src.snap.Agents[i].Harness = harness
				src.snap.Agents[i].Worker.Permissions = protocol.WorkersReadOnly
			}
		}
		m = refresh(m, src)
		m = selectID(m, worker.SessionID)
		s := strings.Join(lines(m.View()), "\n")
		if !strings.Contains(s, "sandbox") || !strings.Contains(s, "read-only") {
			t.Errorf("%s: no permissions line:\n%s", harness, s)
		}
		if got := strings.Contains(s, "not enforced yet"); got != (harness != "codex") {
			t.Errorf("%s: note shown = %v", harness, got)
		}
	}
}
