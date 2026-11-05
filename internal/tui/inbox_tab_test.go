package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/niparis/agent-net/internal/protocol"
)

// inboxOpsSource records the calls the Inbox tab makes. It keeps the same
// method names and parameters the old Needs you rows and inbox overlay used.
type inboxOpsSource struct {
	fakeSource
	calls   []inboxOpsCall
	held    protocol.HeldAnswer
	outID   string
	outAct  string
	callErr error
}

type inboxOpsCall struct {
	method string
	params any
}

func (s *inboxOpsSource) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	s.calls = append(s.calls, inboxOpsCall{method: method, params: params})
	if s.callErr != nil {
		return nil, s.callErr
	}
	if method == protocol.OpProposalList {
		return json.RawMessage(`{"proposals":[]}`), nil
	}
	return json.RawMessage(`{}`), nil
}

func (s *inboxOpsSource) AnswerHeld(_ context.Context, q protocol.HeldAnswer) error {
	s.held = q
	return nil
}

func (s *inboxOpsSource) ResolveOutcome(_ context.Context, id, action string) error {
	s.outID, s.outAct = id, action
	return nil
}

func (s *inboxOpsSource) method(name string) *inboxOpsCall {
	for i := range s.calls {
		if s.calls[i].method == name {
			return &s.calls[i]
		}
	}
	return nil
}

// inboxAllKindsModel has one waiting row of each kind the Inbox shows.
func inboxAllKindsModel(w, h int) (Model, *inboxOpsSource) {
	snap := fakeSnapshot()
	snap.Held = []protocol.Message{{
		ID: "m_held", FromName: "alice", To: "s_0000000000000002",
		Body: "please help\nthis second line is hidden", CreatedAt: t0.Add(-5 * time.Minute),
	}}
	snap.Outcomes = []protocol.OutcomeInboxEntry{{
		EdgeOutcome: protocol.EdgeOutcome{
			From: "ops", To: "app", ReplyID: "r_outcome",
			ReplyOutcome: protocol.ReplyOutcome{Outcome: "reject", Reason: "not now"},
			At:           t0.Add(-6 * time.Minute).UnixNano(),
		},
		Request: "deploy app",
	}}
	snap.Learned = []protocol.LearnedSuggestion{{
		ID: 7, Node: "ops", Field: "refuses", Kind: "add", Phrase: "deploys",
		Status: "pending", Severity: "major", CreatedAt: t0.Add(-7 * time.Minute).UnixNano(),
	}}
	src := &inboxOpsSource{fakeSource: fakeSource{snap: snap, events: fakeEvents()}}
	src.usage, src.counts, src.tops = fakeUsage(), fakeCounts(), fakeTops()
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: w, Height: h})
	m = settle(refresh(m, src))
	m.proposals = []protocol.CTOProposal{{
		ID: "p1", NodeID: "n_mac", Tool: "session_change", Action: "session_start",
		Args:      json.RawMessage(`{"project":"shop","node_id":"n_mac","harness":"pi"}`),
		CreatedAt: t0.Add(-8 * time.Minute), Status: "open",
	}}
	m.inboxItems = []protocol.InboxItem{
		{ID: "i_app", Kind: "approval", SessionID: "s_0000000000000004", Text: "deploy abc", Status: "open", CreatedAt: t0.Add(-9 * time.Minute)},
		{ID: "i_question", Kind: "question", SessionID: "s_0000000000000004", Text: "which branch?", Status: "open", CreatedAt: t0.Add(-10 * time.Minute)},
		{ID: "i_notice", Kind: "check", SessionID: "s_owner", Node: "apps",
			Text:   "Connected: cursor (bot, Cursor, www.cursor.com). If this was not you, revoke it in Settings.",
			Status: "open", CreatedAt: t0.Add(-11 * time.Minute)},
	}
	return m, src
}

func openInbox(m Model) Model { return keys(m, "7") }

func inboxRowIndex(m Model, kind string) int {
	for i, row := range m.inboxRows() {
		if row.kind == kind {
			return i
		}
	}
	return -1
}

func TestInboxTabOpensWithSeven(t *testing.T) {
	m := openInbox(newModel(100, 30))
	if m.tab != inboxTab {
		t.Fatalf("7 opened tab %d, want Inbox", m.tab)
	}
	if !strings.Contains(ansi.Strip(m.testView()), "Inbox") {
		t.Fatal("the Inbox view is missing")
	}
}

func TestInboxTabLabelShowsTheCount(t *testing.T) {
	m := newModel(120, 34)
	title := ansi.Strip(m.titleLine(120))
	if !strings.Contains(title, "7 Inbox") || strings.Contains(title, "7 Inbox (") {
		t.Fatalf("zero count label: %q", title)
	}

	m, _ = inboxAllKindsModel(120, 34)
	title = ansi.Strip(m.titleLine(120))
	want := fmt.Sprintf("7 Inbox (%d)", m.waitingCount())
	if !strings.Contains(title, want) {
		t.Fatalf("label %q does not hold %q", title, want)
	}
}

func TestInboxTabLabelUsesTheWarningColour(t *testing.T) {
	old := testColorProfile
	setTestColorProfile(termenv.TrueColor)
	defer setTestColorProfile(old)
	m, _ := inboxAllKindsModel(120, 34)
	title := m.titleLine(120)
	want := sNeeds.Render(fmt.Sprintf("7 Inbox (%d)", m.waitingCount()))
	if !strings.Contains(title, want) {
		t.Fatalf("count label is not in the warning colour: %q", ansi.Strip(title))
	}
}

func TestInboxListsEveryKindWithItsActions(t *testing.T) {
	m, _ := inboxAllKindsModel(120, 34)
	m = openInbox(m)
	rows := m.inboxRows()
	for i := 1; i < len(rows); i++ {
		if rows[i-1].kind == rows[i].kind && rows[i-1].since < rows[i].since {
			t.Fatalf("%s rows are not newest first: %d then %d", rows[i].kind, rows[i-1].since, rows[i].since)
		}
	}
	view := ansi.Strip(m.testView())
	for _, heading := range []string{"Needs you · 3", "Proposals · 2", "For your information · 2"} {
		if !strings.Contains(view, heading) {
			t.Errorf("missing box heading %q:\n%s", heading, view)
		}
	}
	for _, old := range []string{"Held requests", "Outcomes", "Learned suggestions", "CTO proposals", "Approvals and questions", "Notices"} {
		if strings.Contains(view, old) {
			t.Errorf("old group heading %q is still shown:\n%s", old, view)
		}
	}
	if strings.Contains(view, "second line is hidden") {
		t.Fatal("a row wrapped its text")
	}

	cases := []struct {
		kind string
		keys []string
	}{
		{"held", []string{"enter open session", "a approve", "r refuse"}},
		{"outcome", []string{"s let stand", "O overrule"}},
		{"learned", []string{"a accept", "r reject"}},
		{"proposal", []string{"a approve", "r refuse"}},
		{"approval", []string{"a approve", "r refuse", "x dismiss", "enter details"}},
		{"question", []string{"t answer", "x dismiss", "enter details"}},
		{"notice", []string{"x dismiss"}},
	}
	for _, tc := range cases {
		i := inboxRowIndex(m, tc.kind)
		if i < 0 {
			t.Fatalf("no %s row", tc.kind)
		}
		m.inboxIndex = i
		line := ansi.Strip(lastLine(m.testView()))
		for _, want := range tc.keys {
			if !strings.Contains(line, want) {
				t.Errorf("%s key line %q lacks %q", tc.kind, line, want)
			}
		}
	}
}

func TestInboxActionsCallTheSameOperations(t *testing.T) {
	t.Run("held approve", func(t *testing.T) {
		m, src := inboxAllKindsModel(100, 30)
		m = openInbox(m)
		m.inboxIndex = inboxRowIndex(m, "held")
		next, cmd := press(m, tea.KeyPressMsg{Text: "a", Code: []rune("a")[0]})
		if cmd == nil {
			t.Fatal("a did not answer the held request")
		}
		send(next, cmd())
		if src.held.MessageID != "m_held" || src.held.Action != "a" {
			t.Fatalf("held answer %+v", src.held)
		}
	})
	t.Run("learned accept", func(t *testing.T) {
		m, src := inboxAllKindsModel(100, 30)
		m = openInbox(m)
		m.inboxIndex = inboxRowIndex(m, "learned")
		next, cmd := press(m, tea.KeyPressMsg{Text: "a", Code: []rune("a")[0]})
		if cmd == nil {
			t.Fatal("a did not accept the suggestion")
		}
		send(next, cmd())
		call := src.method("learned_answer")
		if call == nil || !strings.Contains(fmt.Sprint(call.params), "accept") {
			t.Fatalf("learned call %+v", call)
		}
	})
	t.Run("outcome overrule", func(t *testing.T) {
		m, src := inboxAllKindsModel(100, 30)
		m = openInbox(m)
		m.inboxIndex = inboxRowIndex(m, "outcome")
		next, cmd := press(m, tea.KeyPressMsg{Text: "O", Code: []rune("O")[0]})
		if cmd == nil {
			t.Fatal("O did not resolve the outcome")
		}
		send(next, cmd())
		if src.outID != "r_outcome" || src.outAct != "overrule" {
			t.Fatalf("outcome call %s %s", src.outID, src.outAct)
		}
	})
	t.Run("proposal approve", func(t *testing.T) {
		m, src := inboxAllKindsModel(100, 30)
		m = openInbox(m)
		m.inboxIndex = inboxRowIndex(m, "proposal")
		next, cmd := press(m, tea.KeyPressMsg{Text: "a", Code: []rune("a")[0]})
		if cmd == nil {
			t.Fatal("a did not answer the proposal")
		}
		send(next, cmd())
		call := src.method(protocol.OpProposalAnswer)
		if call == nil || !strings.Contains(fmt.Sprintf("%+v", call.params), "Action:a") {
			t.Fatalf("proposal call %+v", call)
		}
	})
	t.Run("proposal approve all", func(t *testing.T) {
		m, _ := inboxAllKindsModel(100, 30)
		m = openInbox(m)
		m.inboxIndex = inboxRowIndex(m, "proposal")
		next, _ := press(m, tea.KeyPressMsg{Text: "A", Code: []rune("A")[0]})
		if !next.proposalConfirm {
			t.Fatal("A did not open the approve-all confirmation")
		}
	})
	t.Run("approval approve", func(t *testing.T) {
		m, src := inboxAllKindsModel(100, 30)
		m = openInbox(m)
		m.inboxIndex = inboxRowIndex(m, "approval")
		next, cmd := press(m, tea.KeyPressMsg{Text: "a", Code: []rune("a")[0]})
		if cmd == nil {
			t.Fatal("a did not answer the inbox item")
		}
		send(next, cmd())
		call := src.method("inbox_answer")
		if call == nil || !strings.Contains(fmt.Sprint(call.params), "i_app") || !strings.Contains(fmt.Sprint(call.params), "approve") {
			t.Fatalf("inbox call %+v", call)
		}
	})
	t.Run("question typed answer", func(t *testing.T) {
		m, src := inboxAllKindsModel(100, 30)
		m = openInbox(m)
		m.inboxIndex = inboxRowIndex(m, "question")
		m = keys(m, "t")
		if !m.inboxEditing {
			t.Fatal("t did not start an answer")
		}
		for _, r := range "feat" {
			m = send(m, tea.KeyPressMsg{Text: string(r), Code: r})
		}
		next, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd == nil {
			t.Fatal("enter did not send the answer")
		}
		send(next, cmd())
		call := src.method("inbox_answer")
		if call == nil || !strings.Contains(fmt.Sprint(call.params), "i_question") || !strings.Contains(fmt.Sprint(call.params), "feat") {
			t.Fatalf("inbox answer %+v", call)
		}
	})
}

func TestInboxDismissesANoticeWithX(t *testing.T) {
	m, src := inboxAllKindsModel(100, 30)
	m = openInbox(m)
	m.inboxIndex = inboxRowIndex(m, "notice")
	next, cmd := press(m, tea.KeyPressMsg{Text: "x", Code: []rune("x")[0]})
	if cmd == nil {
		t.Fatal("x did not dismiss the notice")
	}
	next = send(next, cmd())
	call := src.method("inbox_answer")
	if call == nil || !strings.Contains(fmt.Sprint(call.params), "i_notice") || !strings.Contains(fmt.Sprint(call.params), "dismiss") {
		t.Fatalf("dismiss call %+v", call)
	}
	if got := next.waitingCount(); got != 6 {
		t.Fatalf("%d waiting after the dismiss", got)
	}
}

func TestInboxOwnerStepAsksForEvidenceFields(t *testing.T) {
	m, src := inboxAllKindsModel(100, 30)
	for i := range m.inboxItems {
		if m.inboxItems[i].ID == "i_app" {
			m.inboxItems[i].Delivery = &protocol.InboxDelivery{ChangeID: "ch_1", StepRef: protocol.StepRef{Step: protocol.StepMerge}, ReportFields: []string{protocol.FieldMergedCommit}}
		}
	}
	m = openInbox(m)
	m.inboxIndex = inboxRowIndex(m, "approval")
	next, _ := press(m, tea.KeyPressMsg{Text: "a", Code: []rune("a")[0]})
	if !next.inboxEditing {
		t.Fatal("owner merge did not ask for the merged commit")
	}
	next.inboxInput.SetValue(`{"merged_commit":"` + strings.Repeat("a", 40) + `"}`)
	next, cmd := press(next, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("evidence was not sent")
	}
	send(next, cmd())
	call := src.method("inbox_answer")
	if call == nil || !strings.Contains(fmt.Sprint(call.params), "approve {") {
		t.Fatalf("answer with evidence: %+v", call)
	}
}

func TestInboxEnterOnHeldRequestEntersTheSession(t *testing.T) {
	m, src := inboxAllKindsModel(100, 30)
	m = openInbox(m)
	m.inboxIndex = inboxRowIndex(m, "held")
	_, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter did nothing on the held request")
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c != nil {
				c()
			}
		}
	}
	if len(src.enters) != 1 || src.enters[0].SessionID != "s_0000000000000002" {
		t.Fatalf("enter calls %+v", src.enters)
	}
}

func TestInboxEmptyState(t *testing.T) {
	m := openInbox(newModel(80, 24))
	view := ansi.Strip(m.testView())
	if !strings.Contains(view, "Nothing is waiting for you.") {
		t.Fatalf("empty state missing:\n%s", view)
	}
	for _, heading := range []string{"Needs you", "Proposals", "For your information", "Held requests", "Outcomes", "Learned suggestions", "CTO proposals", "Approvals and questions", "Notices"} {
		if strings.Contains(view, heading) {
			t.Fatalf("empty state shows box %q", heading)
		}
	}
}

func TestSessionsShowsTheInboxSummaryAndNoNeedsRows(t *testing.T) {
	m, _ := inboxAllKindsModel(120, 34)
	m = keys(m, "1")
	view := ansi.Strip(m.testView())
	want := fmt.Sprintf("%d waiting in Inbox (7)", m.waitingCount())
	if !strings.Contains(view, want) {
		t.Fatalf("Sessions lacks %q:\n%s", want, view)
	}
	if strings.Contains(view, "Needs you") {
		t.Fatalf("Sessions still shows the Needs you section:\n%s", view)
	}
	if strings.Contains(view, "deploy abc") {
		// The waiting text belongs to the Inbox tab, not the Sessions table.
		t.Fatalf("a waiting row leaked into Sessions:\n%s", view)
	}

	empty := keys(newModel(120, 34), "1")
	if strings.Contains(ansi.Strip(empty.testView()), "waiting in Inbox") {
		t.Fatalf("empty Sessions shows a summary:\n%s", empty.testView())
	}
}

func TestIKeyDoesNothing(t *testing.T) {
	m := newModel(80, 24)
	before := m.testView()
	next, _ := press(m, tea.KeyPressMsg{Text: "i", Code: []rune("i")[0]})
	if next.tab != sessionsTab || next.inboxDetail || next.inboxEditing {
		t.Fatalf("i opened inbox state: tab=%d detail=%v editing=%v", next.tab, next.inboxDetail, next.inboxEditing)
	}
	if next.testView() != before {
		t.Fatalf("i changed the screen:\n%s", next.testView())
	}
}

func TestInboxGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m, _ := inboxAllKindsModel(size[0], size[1])
		m = openInbox(m)
		golden(t, fmt.Sprintf("inbox_items_%dx%d", size[0], size[1]), m.testView())
	}
}

func TestInboxEmptyGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := openInbox(newModel(size[0], size[1]))
		golden(t, fmt.Sprintf("inbox_empty_%dx%d", size[0], size[1]), m.testView())
	}
}

func TestSessionsSummaryGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m, _ := inboxAllKindsModel(size[0], size[1])
		m = keys(m, "1")
		golden(t, fmt.Sprintf("sessions_inbox_summary_%dx%d", size[0], size[1]), m.testView())
	}
}

func TestInboxTabBarGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m, _ := inboxAllKindsModel(size[0], size[1])
		golden(t, fmt.Sprintf("inbox_tab_bar_%dx%d", size[0], size[1]), m.testView())
	}
}

// The Inbox puts every item in one of three boxes and moves the selection
// through all rows, box to box.
func TestInboxGroupsItemsIntoThreeBoxes(t *testing.T) {
	m, _ := inboxAllKindsModel(120, 34)
	m = openInbox(m)
	rows := m.inboxRows()
	if len(rows) != 7 {
		t.Fatalf("%d rows, want 7", len(rows))
	}
	want := []struct {
		box   inboxBox
		kinds []string
	}{
		{inboxBoxAnswer, []string{"question", "approval", "held"}},
		{inboxBoxProposals, []string{"proposal", "learned"}},
		{inboxBoxInfo, []string{"outcome", "notice"}},
	}
	i := 0
	for _, b := range want {
		for _, kind := range b.kinds {
			if i >= len(rows) {
				t.Fatalf("row %d is missing (%s)", i, kind)
			}
			if rows[i].kind != kind || rows[i].group.box() != b.box {
				t.Fatalf("row %d is %s in box %d, want %s in box %d", i, rows[i].kind, rows[i].group.box(), kind, b.box)
			}
			i++
		}
	}
}

// Each kind says in plain words what it is about, and cuts on a word boundary.
func TestInboxRowsUsePlainWords(t *testing.T) {
	m, _ := inboxAllKindsModel(120, 34)
	m = openInbox(m)
	rows := m.inboxRows()
	cases := map[string][2]string{
		"held":     {"alice", "held request → strategies"},
		"question": {"ci-runner", "which branch?"},
		"approval": {"ci-runner", "deploy abc"},
		"proposal": {"CTO", "start a Pi session in shop, on Mac"},
		"learned":  {"ops (learned)", `add "deploys" to what it refuses`},
		"outcome":  {"ops → app", `deploy app refused: "not now"`},
		"notice":   {"cursor", "connected as a bot (www.cursor.com)"},
	}
	for _, row := range rows {
		want, ok := cases[row.kind]
		if !ok {
			t.Fatalf("unexpected row kind %q", row.kind)
		}
		if row.name != want[0] {
			t.Errorf("%s who: got %q, want %q", row.kind, row.name, want[0])
		}
		if row.text != want[1] {
			t.Errorf("%s summary: got %q, want %q", row.kind, row.text, want[1])
		}
	}

	// A narrow row cuts on a word, not in the middle of one.
	if got := cutWords("start a Pi session in shop, on Mac", 12); strings.HasSuffix(got, "se…") {
		t.Fatalf("cutWords split a word: %q", got)
	}
}

// The panel shows the selected item's full text, where it comes from, how long
// ago, and the keys that act on it.
func TestInboxPanelShowsTheSelectedItem(t *testing.T) {
	m, _ := inboxAllKindsModel(120, 34)
	m = openInbox(m)
	view := ansi.Strip(m.testView())
	for _, want := range []string{"ci-runner asks", "which branch?", "session ci-runner", "agent-net on", "build-vm", "10 minutes ago", "t answer", "enter details"} {
		if !strings.Contains(view, want) {
			t.Errorf("question panel lacks %q:\n%s", want, view)
		}
	}

	m.inboxIndex = inboxRowIndex(m, "outcome")
	view = ansi.Strip(m.testView())
	for _, want := range []string{"ops → app", "deploy app refused: \"not now\"", "6 minutes ago", "s let stand", "O overrule"} {
		if !strings.Contains(view, want) {
			t.Errorf("outcome panel lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "ci-runner asks") {
		t.Errorf("the panel did not follow the selection:\n%s", view)
	}
}

// The bottom key bar lists only the keys that act on the selected row, then
// the movement and help keys.
func TestInboxKeyBarFollowsTheSelection(t *testing.T) {
	m, _ := inboxAllKindsModel(120, 34)
	m = openInbox(m)
	cases := []struct {
		kind string
		want []string
		not  []string
	}{
		{"held", []string{"enter open session", "a approve", "r refuse"}, []string{"s let stand", "x dismiss"}},
		{"outcome", []string{"s let stand", "O overrule"}, []string{"a approve", "t answer"}},
		{"learned", []string{"a accept", "r reject"}, []string{"enter details", "x dismiss"}},
		{"proposal", []string{"a approve", "r refuse"}, []string{"t answer", "enter details", "A approve all"}},
		{"approval", []string{"a approve", "r refuse", "x dismiss", "enter details"}, []string{"s let stand", "t answer"}},
		{"question", []string{"t answer", "x dismiss", "enter details"}, []string{"a approve", "r refuse"}},
		{"notice", []string{"x dismiss"}, []string{"a approve", "t answer", "enter details"}},
	}
	for _, tc := range cases {
		m.inboxIndex = inboxRowIndex(m, tc.kind)
		line := ansi.Strip(lastLine(m.testView()))
		for _, want := range tc.want {
			if !strings.Contains(line, want) {
				t.Errorf("%s key bar %q lacks %q", tc.kind, line, want)
			}
		}
		for _, not := range tc.not {
			if strings.Contains(line, not) {
				t.Errorf("%s key bar %q holds %q", tc.kind, line, not)
			}
		}
		if !strings.Contains(line, "↑↓ select") || !strings.Contains(line, "? help") {
			t.Errorf("%s key bar %q lacks the movement or help key", tc.kind, line)
		}
	}
}

// Each kind lists exactly the keys that act on the selected item, in both the
// panel and the bottom key bar.
func TestInboxItemKeysPerKind(t *testing.T) {
	m, _ := inboxAllKindsModel(120, 34)
	m.snap.Agents[0].Waiting = &protocol.Waiting{
		Kind: protocol.WaitingQuestion, Text: "which branch?", Since: t0.Add(-4 * time.Minute).Unix(),
	}
	m = openInbox(m)
	cases := []struct{ kind, item, bar string }{
		{"held", "enter open session  a approve  r refuse", "enter open session  a approve  r refuse  ↑↓ select  ? help"},
		{"waiting", "enter open session", "enter open session  ↑↓ select  ? help"},
		{"question", "t answer  x dismiss  enter details", "t answer  x dismiss  enter details  ↑↓ select  ? help"},
		{"approval", "a approve  r refuse  x dismiss  enter details", "a approve  r refuse  x dismiss  enter details  ↑↓ select  ? help"},
		{"proposal", "a approve  r refuse", "a approve  r refuse  ↑↓ select  ? help"},
		{"learned", "a accept  r reject", "a accept  r reject  ↑↓ select  ? help"},
		{"outcome", "s let stand  O overrule", "s let stand  O overrule  ↑↓ select  ? help"},
		{"notice", "x dismiss", "x dismiss  ↑↓ select  ? help"},
	}
	for _, tc := range cases {
		m.inboxIndex = inboxRowIndex(m, tc.kind)
		row, ok := m.inboxSelected(m.inboxRows())
		if !ok || row.kind != tc.kind {
			t.Fatalf("no %s row", tc.kind)
		}
		if got := ansi.Strip(keyHints(hintParts(m.inboxItemHints(row))...)); got != tc.item {
			t.Errorf("%s item keys: got %q, want %q", tc.kind, got, tc.item)
		}
		if got := ansi.Strip(lastLine(m.testView())); got != tc.bar {
			t.Errorf("%s key bar: got %q, want %q", tc.kind, got, tc.bar)
		}
		panel := ansi.Strip(strings.Join(m.inboxPanel(48, 12), "\n"))
		for _, part := range strings.Split(tc.item, "  ") {
			if !strings.Contains(panel, part) {
				t.Errorf("%s panel lacks %q:\n%s", tc.kind, part, panel)
			}
		}
	}
}

// A machine is shown by name, never by its node ID.
func TestInboxNeverShowsANodeID(t *testing.T) {
	m, _ := inboxAllKindsModel(120, 34)
	m.proposals[0].NodeID = "n_mac"
	m.proposals[0].Args = json.RawMessage(`{"project":"shop","node_id":"n_mac","harness":"pi"}`)
	m = openInbox(m)
	m.inboxIndex = inboxRowIndex(m, "proposal")
	view := ansi.Strip(m.testView())
	if strings.Contains(view, "n_mac") {
		t.Fatalf("the Inbox shows a node ID:\n%s", view)
	}
	if !strings.Contains(view, "on Mac") {
		t.Fatalf("the proposal does not name the machine:\n%s", view)
	}
}

// On a narrow screen the panel sits under the boxes, at most eight lines.
func TestInboxPanelSitsBelowTheBoxesOnANarrowScreen(t *testing.T) {
	m, _ := inboxAllKindsModel(80, 24)
	m = openInbox(m)
	view := ansi.Strip(m.testView())
	lines := strings.Split(view, "\n")
	panelAt := -1
	for i, line := range lines {
		if strings.Contains(line, "ci-runner asks") {
			panelAt = i
		}
	}
	if panelAt < 0 {
		t.Fatalf("no panel under the boxes:\n%s", view)
	}
	lastBox := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "╰") && i < panelAt {
			lastBox = i
		}
	}
	if lastBox < 0 || panelAt <= lastBox {
		t.Fatalf("the panel is not under the boxes (box ends %d, panel at %d):\n%s", lastBox, panelAt, view)
	}
	if !strings.Contains(view, "which branch?") || !strings.Contains(view, "t answer") {
		t.Fatalf("the narrow panel lacks the item text or keys:\n%s", view)
	}
}

// On a wide screen every box is closed, keeps its age column, and leaves one
// space before the panel.
func TestInboxBoxesAreClosedAndAgedOnAWideScreen(t *testing.T) {
	m, _ := inboxAllKindsModel(120, 34)
	m = openInbox(m)
	view := ansi.Strip(m.testView())
	if strings.Contains(view, "───╭─ci-runner asks") {
		t.Fatalf("a box runs into the panel:\n%s", view)
	}
	if !strings.Contains(view, "╮ ╭─ci-runner asks") {
		t.Fatalf("the box does not close before the panel:\n%s", view)
	}
	if !strings.Contains(view, "10m │") {
		t.Fatalf("the age is not right-aligned inside the box:\n%s", view)
	}
}

// At 100 columns or more the panel sits to the right of the boxes.
func TestInboxPanelSitsOnTheRightOnAWideScreen(t *testing.T) {
	m, _ := inboxAllKindsModel(120, 34)
	m = openInbox(m)
	lines := strings.Split(ansi.Strip(m.testView()), "\n")
	first := lines[1]
	box, panel := strings.Index(first, "Needs you"), strings.Index(first, "ci-runner asks")
	if box < 0 || panel < 0 || panel <= box {
		t.Fatalf("the panel is not to the right of the boxes: %q", first)
	}
}

// The owner clears old approval items with x. The row goes as soon as the
// gateway closes it, with the item ID the list showed.
func TestInboxDismissesAnApprovalWithX(t *testing.T) {
	m, src := inboxAllKindsModel(100, 30)
	m = openInbox(m)
	m.inboxIndex = inboxRowIndex(m, "approval")
	before := m.waitingCount()
	next, cmd := press(m, tea.KeyPressMsg{Text: "x", Code: []rune("x")[0]})
	if cmd == nil {
		t.Fatal("x did not dismiss the approval")
	}
	next = send(next, cmd())
	call := src.method("inbox_answer")
	if call == nil || !strings.Contains(fmt.Sprint(call.params), "i_app") || !strings.Contains(fmt.Sprint(call.params), "dismiss") {
		t.Fatalf("dismiss call %+v", call)
	}
	if got := next.waitingCount(); got != before-1 {
		t.Fatalf("%d waiting after the dismiss, want %d", got, before-1)
	}
}

// A list row can outlive the gateway item, for example a notice the gateway
// already closed. Dismissing it drops the row and says it is gone instead of
// leaving it stuck with the raw error.
func TestInboxDismissOfAGoneItemDropsItAndSaysAlreadyGone(t *testing.T) {
	m, src := inboxAllKindsModel(100, 30)
	src.callErr = fmt.Errorf("inbox item not found")
	m = openInbox(m)
	m.inboxIndex = inboxRowIndex(m, "approval")
	m.inboxError = "inbox item not found"
	before := m.waitingCount()
	next, cmd := press(m, tea.KeyPressMsg{Text: "x", Code: []rune("x")[0]})
	if cmd == nil {
		t.Fatal("x did not try the dismiss")
	}
	next = send(next, cmd())
	if next.inboxError != "" {
		t.Fatalf("the error banner is still stuck: %q", next.inboxError)
	}
	if !strings.Contains(next.inboxNote, "already gone") {
		t.Fatalf("no already gone note: %q", next.inboxNote)
	}
	if got := next.waitingCount(); got != before-1 {
		t.Fatalf("%d waiting after the gone dismiss, want %d", got, before-1)
	}
	for _, item := range next.inboxItems {
		if item.ID == "i_app" {
			t.Fatal("the gone item is still in the list")
		}
	}
}

func TestInboxAlreadyAnsweredIsNotGone(t *testing.T) {
	if inboxGone(fmt.Errorf("inbox item already answered")) {
		t.Fatal("already answered is not not-found")
	}
}
