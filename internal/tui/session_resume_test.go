package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestResumeConfirmAgeAndCancel(t *testing.T) {
	for _, tc := range []struct {
		age     time.Duration
		confirm bool
	}{{30 * time.Minute, false}, {time.Hour, false}, {3 * time.Hour, true}} {
		m, _ := workerModel(120, 34)
		m.snap.Agents[0].Online = false
		m.snap.Agents[0].LastSeen = t0.Add(-tc.age).Unix()
		m.showOffline = true
		m = selectID(m, "s_0000000000000001")
		m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
		if got := strings.Contains(m.testView(), "and enter it?"); got != tc.confirm {
			t.Fatalf("age %v: confirm %v, view %s", tc.age, got, m.testView())
		}
		if tc.confirm {
			if cmd != nil {
				t.Fatal("started before confirm")
			}
			m, cmd = press(m, tea.KeyPressMsg{Code: tea.KeyEsc})
			if cmd != nil || strings.Contains(m.testView(), "and enter it?") {
				t.Fatal("escape did not cancel")
			}
		} else if cmd == nil {
			t.Fatal("recent resume did not start")
		}
	}
}

func TestResumeEntersRegisteredSession(t *testing.T) {
	m, src := workerModel(120, 34)
	m.snap.Agents[0].Online = false
	m.showOffline = true
	m = selectID(m, "s_0000000000000001")
	src.sessionStart = startedSession("tenebrae", "claude")
	m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m, cmd = press2(m, cmd())
	if len(src.sessionResumes) != 1 || src.sessionResumes[0].SessionID != "s_0000000000000001" || cmd == nil {
		t.Fatalf("resume %+v", src.sessionResumes)
	}
	_, _ = press2(m, cmd())
	if len(src.enters) != 1 || src.enters[0].SessionID != "s_owner_new" {
		t.Fatalf("enter %+v", src.enters)
	}
}

func TestResumeMissingFolderLine(t *testing.T) {
	m, src := workerModel(120, 34)
	m.snap.Agents[0].Online = false
	m.showOffline = true
	m = selectID(m, "s_0000000000000001")
	src.sessionErr = errors.New("session folder is gone: /gone")
	m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m, _ = press2(m, cmd())
	if !strings.Contains(m.testView(), "session folder is gone: /gone") {
		t.Fatal(m.testView())
	}
}

func TestResumeConfirmGoldens(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		m, _ := workerModel(size.w, size.h)
		m.snap.Agents[0].Online = false
		m.snap.Agents[0].LastSeen = t0.Add(-3 * time.Hour).Unix()
		m.snap.Agents[0].Profile.Name = "tenebrae"
		m.showOffline = true
		m = selectID(m, "s_0000000000000001")
		before := strings.Split(m.testView(), "\n")
		m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
		after := strings.Split(m.testView(), "\n")
		if len(before) != len(after) || strings.Join(before[:len(before)-1], "\n") != strings.Join(after[:len(after)-1], "\n") {
			t.Fatal("confirm shifted layout")
		}
		golden(t, "view_"+itoa(size.w)+"x"+itoa(size.h)+"_resume_confirm", m.testView())
	}
}

func TestResumeUnsupportedRowsAndExistingEnterPlans(t *testing.T) {
	for _, tc := range []struct {
		harness, node string
		unsupported   bool
		resume        bool
	}{
		{"pi", "n_mac", false, true}, {"pi", "n_vm", false, true}, {"claude", "n_vm", false, true},
		{"opencode", "n_mac", false, true}, {"codex", "n_mac", false, true}, {"opencode", "n_vm", false, true}, {"codex", "n_vm", false, true}, {"other", "n_mac", true, false},
	} {
		t.Run(tc.harness+tc.node, func(t *testing.T) {
			m, src := workerModel(120, 34)
			a := &m.snap.Agents[0]
			a.Online = false
			a.Harness = tc.harness
			a.NodeID = tc.node
			a.LastSeen = t0.Unix()
			m.showOffline = true
			m = selectID(m, a.SessionID)
			src.sessionStart = startedSession("tenebrae", tc.harness)
			m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
			if tc.unsupported {
				if cmd != nil || !strings.Contains(m.notice.text, "Open its conversation in the harness directly") {
					t.Fatalf("notice %+v", m.notice)
				}
				return
			}
			if cmd == nil {
				t.Fatal("missing resume or enter plan")
			}
			m, _ = press2(m, cmd())
			if tc.resume {
				if len(src.sessionResumes) != 1 || src.sessionResumes[0].SessionID != a.SessionID || src.sessionResumes[0].NodeID != a.NodeID || len(src.enters) != 0 {
					t.Fatalf("resume %+v enter %+v", src.sessionResumes, src.enters)
				}
				return
			}
			if len(src.enters) != 1 || len(src.sessionResumes) != 0 {
				t.Fatalf("enter %+v resume %+v", src.enters, src.sessionResumes)
			}
		})
	}
}

func TestResumeHerdrDoesNotEnterTwice(t *testing.T) {
	m, src := workerModel(120, 34)
	m.snap.Agents[0].Online = false
	m.showOffline = true
	m = selectID(m, "s_0000000000000001")
	src.sessionStart = startedSession("tenebrae", "claude")
	src.sessionStart.EnterPlan = &protocol.EnterPlan{OpenedHerdr: true, Note: "opened tenebrae in herdr"}
	m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m, cmd = press2(m, cmd())
	if cmd != nil || len(src.enters) != 0 || m.notice.text != "opened tenebrae in herdr" {
		t.Fatalf("notice %q, enters %+v, command %v", m.notice.text, src.enters, cmd != nil)
	}
}

func TestResumeOpenCodeEntersRegisteredSession(t *testing.T) {
	m, src := workerModel(120, 34)
	m.snap.Agents[0].Online = false
	m.snap.Agents[0].Harness = "opencode"
	m.showOffline = true
	m = selectID(m, "s_0000000000000001")
	src.sessionStart = startedSession("tenebrae", "claude")
	m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m, cmd = press2(m, cmd())
	if len(src.sessionResumes) != 1 || src.sessionResumes[0].SessionID != "s_0000000000000001" || cmd == nil {
		t.Fatalf("resume %+v", src.sessionResumes)
	}
	_, _ = press2(m, cmd())
	if len(src.enters) != 1 || src.enters[0].SessionID != "s_owner_new" {
		t.Fatalf("enter %+v", src.enters)
	}
}

func TestResumeViewerBeforeRegistration(t *testing.T) {
	m, _ := workerModel(120, 34)
	m, cmd := m.onResume(resumeMsg{res: protocol.StartedSession{Status: "started", EnterPlan: &protocol.EnterPlan{OpenedHerdr: true, Note: "Opened viewer"}}})
	if cmd != nil || m.notice.err || m.notice.text != "Opened viewer" {
		t.Fatalf("viewer rejected: %+v", m.notice)
	}
}
