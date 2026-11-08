package tui

import (
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

type encodingTestMsg struct{}

type encodingDriverModel struct {
	inner   Model
	events  []tea.Msg
	pending int // input messages still to see before the program may stop
}

func (d encodingDriverModel) Init() tea.Cmd {
	return nil
}

func (d encodingDriverModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case encodingTestMsg:
		return d, tea.Quit
	case tea.WindowSizeMsg, tea.ColorProfileMsg, tea.EnvMsg:
		return d, nil
	}
	d.events = append(d.events, msg)
	next, cmd := d.inner.Update(msg)
	d.inner = next.(Model)
	if d.inner.quitting {
		return d, tea.Quit
	}
	if isEncodingInputMsg(msg) {
		d.pending--
		if d.pending <= 0 {
			// Run the command this message produced before stopping, so its
			// side effects (a rule change, for example) are applied. Then ask
			// the program to stop.
			return d, tea.Sequence(cmd, func() tea.Msg { return encodingTestMsg{} })
		}
	}
	return d, cmd
}

func (d encodingDriverModel) View() tea.View {
	return d.inner.View()
}

// isEncodingInputMsg reports whether msg was decoded from terminal input. The
// driver stops only after it has seen every such message the raw input
// produces, so a slow input reader under -race cannot leave the paste or key
// unprocessed when the program quits.
func isEncodingInputMsg(msg tea.Msg) bool {
	switch msg.(type) {
	case tea.KeyPressMsg, tea.KeyReleaseMsg,
		tea.PasteStartMsg, tea.PasteMsg, tea.PasteEndMsg,
		tea.MouseClickMsg, tea.MouseReleaseMsg, tea.MouseWheelMsg, tea.MouseMotionMsg,
		tea.FocusMsg, tea.BlurMsg:
		return true
	}
	return false
}

// runEncodingProgram runs initial with raw terminal input and returns the final
// model once the program has handled it. inputMsgs is the number of input
// messages raw decodes into; it defaults to one.
func runEncodingProgram(t *testing.T, initial Model, raw string, inputMsgs ...int) (Model, []tea.Msg) {
	t.Helper()
	expect := 1
	if len(inputMsgs) > 0 {
		expect = inputMsgs[0]
	}
	pr, pw := io.Pipe()
	d := encodingDriverModel{inner: initial, pending: expect}
	p := tea.NewProgram(
		d,
		tea.WithInput(pr),
		tea.WithOutput(io.Discard),
		tea.WithoutRenderer(),
		tea.WithoutSignals(),
		tea.WithWindowSize(80, 24),
		tea.WithEnvironment([]string{"TERM=xterm-256color"}),
	)

	errc := make(chan error, 1)
	go func() {
		finalM, err := p.Run()
		if err == nil {
			d = finalM.(encodingDriverModel)
		}
		errc <- err
	}()

	go func() {
		if len(raw) > 0 {
			_, _ = pw.Write([]byte(raw))
		}
		_ = pw.Close()
		if len(raw) == 0 {
			p.Send(encodingTestMsg{})
		}
	}()

	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("program run error: %v", err)
		}
	case <-time.After(2 * time.Second):
		p.Kill()
		t.Fatal("program run timed out")
	}

	return d.inner, d.events
}

// 1. Shift+Tab in new-session form and worker form
func TestShiftTabEncodings(t *testing.T) {
	encodings := []struct {
		name string
		raw  string
	}{
		{"legacy", "\x1b[Z"},
		{"rxvt", "\x1b[1;2Z"},
		{"modifyOtherKeys", "\x1b[27;2;9~"},
		{"kitty", "\x1b[9;2u"},
	}

	for _, enc := range encodings {
		t.Run("new_session_form_"+enc.name, func(t *testing.T) {
			m := newModel(80, 24)
			m.mode = modeSessionForm
			m.sform = newSessionForm()
			m.sform.cur = sFirstMessage

			res, _ := runEncodingProgram(t, m, enc.raw)
			if res.sform.cur == sFirstMessage {
				t.Fatalf("%s: Shift+Tab did not move to previous field in new-session form", enc.name)
			}
		})

		t.Run("worker_form_"+enc.name, func(t *testing.T) {
			m := newModel(80, 24)
			m.mode = modePolicy
			m.pol = policyForm{cur: pfCoder}

			res, _ := runEncodingProgram(t, m, enc.raw)
			if res.pol.cur == pfCoder {
				t.Fatalf("%s: Shift+Tab did not move to previous field in worker policy form", enc.name)
			}
		})
	}
}

// 2. Space in Tools picker and session cell
func TestSpaceEncodings(t *testing.T) {
	encodings := []struct {
		name string
		raw  string
	}{
		{"space", " "},
		{"kitty", "\x1b[32u"},
	}

	for _, enc := range encodings {
		t.Run("tools_picker_"+enc.name, func(t *testing.T) {
			m := newModel(80, 24)
			m.tab = toolsTab
			m.tl.modal = &toolsModal{
				picker: &projPicker{
					all: []string{"proj1", "proj2"},
					sel: map[string]bool{},
				},
			}

			if m.tl.modal.picker.sel["proj1"] {
				t.Fatal("expected proj1 to start unselected")
			}

			res, _ := runEncodingProgram(t, m, enc.raw)
			if !res.tl.modal.picker.sel["proj1"] {
				t.Fatalf("%s: Space did not toggle selection in tools picker", enc.name)
			}
		})

		t.Run("session_cell_"+enc.name, func(t *testing.T) {
			src := &ruleSource{fakeSource: fakeSource{snap: fakeSnapshot()}}
			m := New(src)
			m.width, m.height = 80, 24
			m.tab = sessionsTab
			m.snap = src.snap
			m.selected = "s_0000000000000001"
			m.sessionCell = 1 // on Rule cell

			_, _ = runEncodingProgram(t, m, enc.raw)
			if len(src.edges) != 1 {
				t.Fatalf("%s: Space did not cycle session cell rule: edges = %+v", enc.name, src.edges)
			}
		})
	}
}

// 3. Enter, Esc, ctrl+c, ctrl+]
func TestControlKeysEncodings(t *testing.T) {
	// Enter: moves to next field in session form
	enterCases := []struct {
		name string
		raw  string
	}{
		{"legacy_cr", "\r"},
		{"legacy_lf", "\n"},
		{"modifyOtherKeys", "\x1b[27;1;13~"},
		{"kitty", "\x1b[13u"},
	}
	for _, tc := range enterCases {
		t.Run("enter_"+tc.name, func(t *testing.T) {
			m := newModel(80, 24)
			m.mode = modeSessionForm
			m.sform = newSessionForm()
			m.sform.cur = sProject
			m.sform.in[sProject].SetValue("myproj")

			res, events := runEncodingProgram(t, m, tc.raw)
			if res.sform.cur == sProject {
				t.Fatalf("%s: Enter did not advance to next field in session form (events: %+v)", tc.name, events)
			}
		})
	}

	// Esc: closes help
	escCases := []struct {
		name string
		raw  string
	}{
		{"legacy", "\x1b"},
		{"kitty", "\x1b[27u"},
	}
	for _, tc := range escCases {
		t.Run("esc_"+tc.name, func(t *testing.T) {
			m := newModel(80, 24)
			m.help = true

			res, _ := runEncodingProgram(t, m, tc.raw)
			if res.help {
				t.Fatalf("%s: Esc did not close help", tc.name)
			}
		})
	}

	// ctrl+c: quits
	ctrlCCases := []struct {
		name string
		raw  string
	}{
		{"legacy", "\x03"},
		{"modifyOtherKeys", "\x1b[27;5;99~"},
		{"kitty", "\x1b[99;5u"},
	}
	for _, tc := range ctrlCCases {
		t.Run("ctrl_c_"+tc.name, func(t *testing.T) {
			m := newModel(80, 24)
			res, _ := runEncodingProgram(t, m, tc.raw)
			if !res.quitting {
				t.Fatalf("%s: ctrl+c did not set quitting", tc.name)
			}
		})
	}

	// ctrl+]: returns focus from CTO to Sessions
	ctrlBracketCases := []struct {
		name string
		raw  string
	}{
		{"legacy", "\x1d"},
		{"modifyOtherKeys", "\x1b[27;5;93~"},
		{"kitty", "\x1b[93;5u"},
	}
	for _, tc := range ctrlBracketCases {
		t.Run("ctrl_bracket_"+tc.name, func(t *testing.T) {
			m := New(&fakeCTOSource{fakeSource: &fakeSource{snap: fakeSnapshot()}})
			m.width, m.height = 80, 24
			m.tab = sessionsTab
			m.focus = focusCTO

			res, _ := runEncodingProgram(t, m, tc.raw)
			if res.focus != focusSessions {
				t.Fatalf("%s: ctrl+] did not return focus to sessions: focus = %v", tc.name, res.focus)
			}
		})
	}
}

// 4. Bracketed paste into compose editor and worker task form
func TestBracketedPasteEncodings(t *testing.T) {
	pastePayload := "\x1b[200~line1\nline2 q x\x1b[201~"

	t.Run("compose_editor", func(t *testing.T) {
		m := selectID(newModel(80, 24), workerID)
		m.mode = modeCompose
		m.compose = newTextInput()
		m.compose.Focus()

		res, _ := runEncodingProgram(t, m, pastePayload, 3)
		if res.quitting {
			t.Fatal("paste triggered quit hotkey (q)")
		}
		val := res.compose.Value()
		if !strings.Contains(val, "line1") || !strings.Contains(val, "line2") || !strings.Contains(val, "q x") {
			t.Fatalf("unexpected compose paste value: %q", val)
		}
	})

	t.Run("worker_task_form", func(t *testing.T) {
		m := newModel(80, 24)
		m.mode = modeSessionForm
		m.sform = newSessionForm()
		m.sform.cur = sFirstMessage
		m.sform.in[sFirstMessage].Focus()

		res, _ := runEncodingProgram(t, m, pastePayload, 3)
		if res.quitting {
			t.Fatal("paste triggered quit hotkey (q)")
		}
		val := res.sform.in[sFirstMessage].Value()
		if !strings.Contains(val, "line1") || !strings.Contains(val, "line2") || !strings.Contains(val, "q x") {
			t.Fatalf("unexpected task paste value: %q", val)
		}
	})
}

// 5. Number keys 1-8 switch tabs in all three encodings
func TestNumberKeysEncodings(t *testing.T) {
	type keyCase struct {
		tab    int
		name   string
		legacy string
		mok    string
		kitty  string
	}

	cases := []keyCase{
		{sessionsTab, "1_sessions", "1", "\x1b[27;1;49~", "\x1b[49u"},
		{graphTab, "2_graph", "2", "\x1b[27;1;50~", "\x1b[50u"},
		{quotasTab, "3_quotas", "3", "\x1b[27;1;51~", "\x1b[51u"},
		{messagesTab, "4_messages", "4", "\x1b[27;1;52~", "\x1b[52u"},
		{toolsTab, "5_tools", "5", "\x1b[27;1;53~", "\x1b[53u"},
		{settingsTab, "6_settings", "6", "\x1b[27;1;54~", "\x1b[54u"},
		{inboxTab, "7_inbox", "7", "\x1b[27;1;55~", "\x1b[55u"},
		{deliveryTab, "8_delivery", "8", "\x1b[27;1;56~", "\x1b[56u"},
	}

	for _, tc := range cases {
		for encName, raw := range map[string]string{
			"legacy":          tc.legacy,
			"modifyOtherKeys": tc.mok,
			"kitty":           tc.kitty,
		} {
			t.Run(tc.name+"_"+encName, func(t *testing.T) {
				m := newModel(80, 24)
				// Start from a different tab so we can observe the switch
				if tc.tab == sessionsTab {
					m.tab = graphTab
				} else {
					m.tab = sessionsTab
				}

				res, _ := runEncodingProgram(t, m, raw)
				if res.tab != tc.tab {
					t.Fatalf("%s (%s): expected tab %v, got %v", tc.name, encName, tc.tab, res.tab)
				}
			})
		}
	}
}

// 6. Mouse: SGR click and wheel in Graph tab reach graph handler; leaving turns mouse capture off
func TestMouseEncodings(t *testing.T) {
	t.Run("graph_mouse_click_and_wheel", func(t *testing.T) {
		m := graphModel(80, 24)
		if !m.graphMouseActive() {
			t.Fatal("expected graphMouseActive to be true")
		}

		// Initial view has mouse capture enabled
		if m.View().MouseMode != tea.MouseModeCellMotion {
			t.Fatalf("expected MouseModeCellMotion, got %v", m.View().MouseMode)
		}

		initialZoom := m.graphState.Camera.Zoom

		// Send wheel up: \x1b[<64;20;10M
		res, _ := runEncodingProgram(t, m, "\x1b[<64;20;10M")
		if res.graphState.Camera.Zoom == initialZoom {
			t.Fatal("SGR wheel did not reach graph handler (zoom unchanged)")
		}

		// Send click inside graph: \x1b[<0;20;10M followed by release \x1b[<0;20;10m
		res2, _ := runEncodingProgram(t, m, "\x1b[<0;20;10M\x1b[<0;20;10m", 2)
		// The click was delivered and processed without crashing
		if res2.tab != graphTab {
			t.Fatal("graph tab changed unexpectedly")
		}
	})

	t.Run("leaving_graph_tab_disables_mouse", func(t *testing.T) {
		m := graphModel(80, 24)
		if m.View().MouseMode != tea.MouseModeCellMotion {
			t.Fatalf("expected MouseModeCellMotion, got %v", m.View().MouseMode)
		}

		// Switch to Sessions tab with key "1" (legacy, kitty, or modifyOtherKeys)
		res, _ := runEncodingProgram(t, m, "1")
		if res.tab != sessionsTab {
			t.Fatalf("expected sessionsTab, got %v", res.tab)
		}
		if res.View().MouseMode != tea.MouseModeNone {
			t.Fatalf("leaving graph tab should disable mouse capture, got %v", res.View().MouseMode)
		}
	})
}
