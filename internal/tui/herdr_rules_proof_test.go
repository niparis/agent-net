package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// TestHerdrRulesProofScenario writes the text frames of decision 0154 for
// scripts/herdr-rules-proof.sh: what the owner sees when the TUI runs inside
// his own herdr. The plans are the ones the daemon answers now (see
// internal/daemon/herdr_rules_test.go). Each step starts from a fresh model.
func TestHerdrRulesProofScenario(t *testing.T) {
	const ownerSocket = "/run/user/1000/herdr-owner.sock"
	attach := protocol.EnterPlan{Argv: []string{"claude", "attach", "job_42"}, Dir: "/work/tenebrae", Note: "Press ctrl+] to come back here; tenebrae (Mac) keeps running."}
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		inOwnerHerdr := func() (Model, *fakeSource) {
			m, src := workerModel(size.w, size.h)
			m.herdrSocket, m.herdrPaneID = ownerSocket, "w1:p0"
			return selectID(m, workerID), src
		}

		// Step 1: Enter hands the TUI's own pane to the session.
		m, src := inOwnerHerdr()
		src.enterPlan = attach
		got, _, _, done := capture(&m)
		m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
		m, cmd = press2(m, cmd())
		if cmd == nil || !reflect.DeepEqual(*got, attach.Argv) {
			t.Fatalf("%dx%d: Enter ran %v, want %v", size.w, size.h, *got, attach.Argv)
		}
		if len(src.enters) != 1 || src.enters[0].HerdrSocket != ownerSocket {
			t.Fatalf("%dx%d: enter requests %+v", size.w, size.h, src.enters)
		}
		writeHerdrProof(t, size.w, size.h, "enter", "press Enter on tenebrae (Mac) in the TUI that runs in the owner's herdr",
			"the TUI hands its pane to `"+strings.Join(*got, " ")+"` under the bar \"tenebrae (Mac) - ctrl+] to come back\"; no herdr tab is created",
			fmt.Sprintf("handed over: %s\nbar: tenebrae (Mac) - ctrl+] to come back\n", strings.Join(*got, " ")))

		// Step 2: ctrl+] ends the handover and the list is back.
		m, src = inOwnerHerdr()
		src.enterPlan = attach
		_, _, _, done = capture(&m)
		m, cmd = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
		m, cmd = press2(m, cmd())
		if cmd == nil {
			t.Fatalf("%dx%d: no handover", size.w, size.h)
		}
		m, _ = press2(m, (*done)(nil))
		frame := ansi.Strip(m.testView())
		if !strings.Contains(frame, "tenebrae") {
			t.Fatalf("%dx%d: not back on the list:\n%s", size.w, size.h, frame)
		}
		writeHerdrProof(t, size.w, size.h, "return", "press ctrl+] in the handed-over pane", "the session list is back and the session keeps running", frame)

		// Step 3: a Pi start runs in agent-net's own herdr; the notice says so.
		m, src = inOwnerHerdr()
		src.sessionStart = startedSession("tenebrae", "pi")
		src.sessionStart.EnterPlan = &protocol.EnterPlan{OpenedHerdr: true, HerdrPane: "w1:p1", HerdrSession: "agent-net", Note: "Started tenebrae in agent-net's herdr. Enter opens it."}
		m, _ = press(m, sessionKey())
		m = typeText(m, "Inspect it")
		m, cmd = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
		m, _ = press2(m, cmd())
		frame = ansi.Strip(m.testView())
		if !strings.Contains(frame, "agent-net's herdr") || len(src.enters) != 0 {
			t.Fatalf("%dx%d: notice %q, enters %+v:\n%s", size.w, size.h, m.notice.text, src.enters, frame)
		}
		writeHerdrProof(t, size.w, size.h, "pi-start", "start a Pi session from the new-session form, with a first message",
			"the notice names agent-net's herdr; nothing opens in the owner's herdr", frame)
	}
}

func writeHerdrProof(t *testing.T, w, h int, step, action, expected, frame string) {
	t.Helper()
	dir := os.Getenv("AGENT_NET_PROOF_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	capture := fmt.Sprintf("build: %s\nsize: %dx%d\nstep: %s\naction: %s\nexpected: %s\nerrors: none\n\n%s\n", os.Getenv("AGENT_NET_PROOF_BUILD"), w, h, step, action, expected, frame)
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("herdr-%s-%dx%d.txt", step, w, h)), []byte(capture), 0600); err != nil {
		t.Fatal(err)
	}
}
