package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/protocol"
)

// Paths in the Project field of the New session form
// (docs/design/start-sessions.md, build plan part 10).

// pathModel opens the form with a home folder the test controls, so ~ paths
// are stable and never touch the real home folder.
func pathModel(t *testing.T, w, h int) (Model, *fakeSource, string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude-config"))
	m, src := workerModel(w, h)
	m.home = home
	m = selectID(m, "s_0000000000000002") // strategies, idle, project folder free
	m, _ = press(m, sessionKey())
	return m, src, home
}

func setProjectValue(m *Model, value string) {
	m.sform.in[sProject].SetValue(value)
	m.refreshSessionPathFolder()
}

func gitFolder(t *testing.T, home, rel string) string {
	t.Helper()
	dir := filepath.Join(home, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func plainFolder(t *testing.T, home, rel string) string {
	t.Helper()
	dir := filepath.Join(home, filepath.FromSlash(rel))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestNewSessionTypedPathStartsThere(t *testing.T) {
	m, src, home := pathModel(t, 120, 34)
	dir := gitFolder(t, home, "code/garden")
	setProjectValue(&m, "~/code/garden")
	m.sform.in[sHarness].SetValue("claude")

	view := m.testView()
	if !strings.Contains(view, "not a known project yet: it will be registered") {
		t.Fatalf("the form does not say the folder will be registered:\n%s", view)
	}
	if strings.Contains(view, "on Mac, "+dir) {
		t.Errorf("a typed path repeats its folder after the machine:\n%s", view)
	}

	src.sessionStart = startedSession("garden", "claude")
	before := len(src.sessionStarts)
	m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter gave no command")
	}
	m, _ = press2(m, cmd())
	if len(src.sessionStarts) != before+1 {
		t.Fatalf("session_start asked %+v", src.sessionStarts)
	}
	req := src.sessionStarts[len(src.sessionStarts)-1]
	if req.Path != dir || req.Project != "" {
		t.Fatalf("a typed path sent %+v, want path %q and no project", req, dir)
	}
	if req.Create || req.TrustClaude {
		t.Fatalf("an existing folder asked for create or trust: %+v", req)
	}
}

// Open question 5: a bare name that is no project goes under the folder that
// holds most registered projects on the machine.
func TestNewSessionBareNameGoesUnderTheProjectsFolder(t *testing.T) {
	m, src, home := pathModel(t, 120, 34)
	for i := range m.snap.Projects {
		m.snap.Projects[i].Path = filepath.Join(home, "code", m.snap.Projects[i].Name)
	}
	setProjectValue(&m, "garden-planner")
	m.sform.in[sHarness].SetValue("pi")
	want := filepath.Join(home, "code", "garden-planner")

	if view := m.testView(); !strings.Contains(view, "new folder: ~/code/garden-planner") {
		t.Fatalf("a bare name does not show where the folder goes:\n%s", view)
	}
	src.sessionStart = startedSession("garden-planner", "pi")
	m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil || m.sessionPending == nil {
		t.Fatalf("the create question did not appear: cmd %v pending %+v", cmd != nil, m.sessionPending)
	}
	m, cmd = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("yes gave no command")
	}
	m, _ = press2(m, cmd())
	if len(src.sessionStarts) == 0 {
		t.Fatal("session_start was not asked")
	}
	if req := src.sessionStarts[0]; req.Path != want || !req.Create {
		t.Fatalf("bare name sent %+v, want %q with create", req, want)
	}
}

func TestNewSessionMissingFolderAsksBeforeCreating(t *testing.T) {
	for _, no := range []tea.KeyPressMsg{{Code: tea.KeyEsc}, {Text: "n", Code: []rune("n")[0]}} {
		t.Run(no.String(), func(t *testing.T) {
			m, src, home := pathModel(t, 120, 34)
			target := filepath.Join(home, "code", "garden-planner")
			setProjectValue(&m, "~/code/garden-planner")
			m.sform.in[sHarness].SetValue("pi")

			m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
			if cmd != nil || len(src.sessionStarts) != 0 {
				t.Fatalf("the missing folder was sent before the owner answered: cmd %v asks %+v", cmd != nil, src.sessionStarts)
			}
			if m.mode != modeSessionForm || m.sessionPending == nil || m.sessionPending.kind != askCreateFolder {
				t.Fatalf("mode %v pending %+v, want the create question", m.mode, m.sessionPending)
			}
			view := m.testView()
			for _, want := range []string{"Create ~/code/garden-planner on Mac and run git init?", confirmHint} {
				if !strings.Contains(view, want) {
					t.Errorf("the create question does not show %q:\n%s", want, view)
				}
			}
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatalf("the folder exists before the owner said yes: %v", err)
			}

			m, cmd = press(m, no)
			if cmd != nil || m.sessionPending != nil || len(src.sessionStarts) != 0 {
				t.Fatalf("no still sent a start: cmd %v pending %+v asks %+v", cmd != nil, m.sessionPending, src.sessionStarts)
			}
			if m.mode != modeSessionForm || !strings.Contains(m.notice.text, "Not created") {
				t.Fatalf("no closed the form: mode %v notice %q", m.mode, m.notice.text)
			}
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatalf("no created the folder: %v", err)
			}
		})
	}
}

func TestNewSessionMissingFolderIsCreatedOnlyAfterYes(t *testing.T) {
	m, src, home := pathModel(t, 120, 34)
	target := filepath.Join(home, "code", "garden-planner")
	setProjectValue(&m, "~/code/garden-planner")
	m.sform.in[sHarness].SetValue("claude")

	m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // ask
	m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("yes gave no command")
	}
	if m.mode != modeNormal || m.sessionPending != nil {
		t.Fatalf("yes kept the question: mode %v pending %+v", m.mode, m.sessionPending)
	}
	src.sessionStart = startedSession("garden-planner", "claude")
	m, _ = press2(m, cmd())
	if len(src.sessionStarts) != 1 {
		t.Fatalf("session_start asked %+v", src.sessionStarts)
	}
	if req := src.sessionStarts[0]; req.Path != target || !req.Create {
		t.Fatalf("yes sent %+v, want %q with create", req, target)
	}
}

func TestNewSessionPathOutsideHomeIsRefused(t *testing.T) {
	outside := t.TempDir()
	for _, tc := range []struct {
		name, value, want string
	}{
		{"outside", "/etc/agent-net-garden", "outside your home folder"},
		{"relative", "code/garden", "absolute or start with ~"},
		{"link", "~/escape", "outside your home folder"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, src, home := pathModel(t, 80, 24)
			if tc.name == "link" {
				if err := os.Symlink(outside, filepath.Join(home, "escape")); err != nil {
					t.Skipf("symlink: %v", err)
				}
			}
			setProjectValue(&m, tc.value)
			m.sform.in[sHarness].SetValue("pi")
			if view := m.testView(); !strings.Contains(strings.ToLower(view), tc.want) {
				t.Fatalf("the form does not refuse the path:\n%s", view)
			}
			m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
			if cmd != nil || len(src.sessionStarts) != 0 {
				t.Fatalf("a refused path was sent: cmd %v asks %+v", cmd != nil, src.sessionStarts)
			}
			if m.mode != modeSessionForm {
				t.Fatalf("the form closed on a refused path: %v", m.mode)
			}
		})
	}
}

func TestNewSessionClaudeTrustNoteAndQuestion(t *testing.T) {
	t.Run("note", func(t *testing.T) {
		m, _, home := pathModel(t, 120, 34)
		gitFolder(t, home, "code/garden")
		setProjectValue(&m, "~/code/garden")
		m.sform.in[sHarness].SetValue("claude")
		if view := m.testView(); !strings.Contains(view, "Claude Code has not trusted this folder: it will ask") {
			t.Fatalf("the form has no trust note:\n%s", view)
		}
	})

	t.Run("no", func(t *testing.T) {
		m, src, home := pathModel(t, 120, 34)
		dir := gitFolder(t, home, "code/garden")
		setProjectValue(&m, "~/code/garden")
		m.sform.in[sHarness].SetValue("claude")
		src.sessionStart = protocol.StartedSession{ID: "start_1", Status: "needs_trust",
			NodeID: "n_mac", Hostname: "Mac", Harness: "claude", Project: "garden", Path: dir, StartedAt: t0.Unix()}

		m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
		m, _ = press2(m, cmd())
		if m.sessionPending == nil || m.sessionPending.kind != askTrustClaude {
			t.Fatalf("no trust question: pending %+v", m.sessionPending)
		}
		view := m.testView()
		for _, want := range []string{"Trust ~/code/garden for Claude Code?", confirmHint} {
			if !strings.Contains(view, want) {
				t.Errorf("the trust question does not show %q:\n%s", want, view)
			}
		}
		m, cmd = press(m, tea.KeyPressMsg{Code: tea.KeyEsc})
		if cmd != nil || m.sessionPending != nil || len(src.sessionStarts) != 1 {
			t.Fatalf("no still started: cmd %v pending %+v asks %+v", cmd != nil, m.sessionPending, src.sessionStarts)
		}
		if !strings.Contains(m.notice.text, "does not trust this folder") {
			t.Fatalf("no trust notice %q", m.notice.text)
		}
	})

	t.Run("yes", func(t *testing.T) {
		m, src, home := pathModel(t, 120, 34)
		dir := gitFolder(t, home, "code/garden")
		setProjectValue(&m, "~/code/garden")
		m.sform.in[sHarness].SetValue("claude")
		src.sessionStart = protocol.StartedSession{ID: "start_1", Status: "needs_trust",
			NodeID: "n_mac", Hostname: "Mac", Harness: "claude", Project: "garden", Path: dir, StartedAt: t0.Unix()}

		m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
		m, _ = press2(m, cmd())
		m, cmd = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd == nil {
			t.Fatal("yes gave no command")
		}
		src.sessionStart = startedSession("garden", "claude")
		m, _ = press2(m, cmd())
		if len(src.sessionStarts) != 2 {
			t.Fatalf("asks %+v, want the start and the trusted retry", src.sessionStarts)
		}
		if !src.sessionStarts[1].TrustClaude || src.sessionStarts[1].Path != dir {
			t.Fatalf("yes sent %+v", src.sessionStarts[1])
		}
	})
}

func TestNewSessionRemoteMissingPathAsksAfterTheMachineChecks(t *testing.T) {
	m, src, _ := pathModel(t, 120, 34)
	setProjectValue(&m, "~/code/far@build-vm")
	m.sform.in[sHarness].SetValue("pi")
	src.sessionStart = protocol.StartedSession{ID: "start_r", Status: "needs_create",
		NodeID: "n_vm", Hostname: "build-vm", Harness: "pi", Project: "far", Path: "/srv/n/code/far"}

	m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter gave no command")
	}
	m, _ = press2(m, cmd())
	if len(src.sessionStarts) != 1 {
		t.Fatalf("asks %+v", src.sessionStarts)
	}
	if req := src.sessionStarts[0]; req.NodeID != "n_vm" || req.Path != "~/code/far" || req.Create {
		t.Fatalf("remote path sent %+v", req)
	}
	if m.sessionPending == nil || m.sessionPending.kind != askCreateFolder {
		t.Fatalf("the remote machine's missing folder did not ask: %+v", m.sessionPending)
	}
	if view := m.testView(); !strings.Contains(view, "Create /srv/n/code/far on build-vm and run git init?") {
		t.Fatalf("the remote create question is missing:\n%s", view)
	}

	m, cmd = press(m, tea.KeyPressMsg{Text: "y", Code: []rune("y")[0]})
	if cmd == nil {
		t.Fatal("yes gave no command")
	}
	m, _ = press2(m, cmd())
	if len(src.sessionStarts) != 2 {
		t.Fatalf("asks %+v", src.sessionStarts)
	}
	if req := src.sessionStarts[1]; req.NodeID != "n_vm" || req.Path != "~/code/far" || !req.Create {
		t.Fatalf("remote yes sent %+v", req)
	}
}

func TestNewSessionPathGoldens(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		name := func(s string) string { return "view_" + itoa(size.w) + "x" + itoa(size.h) + "_" + s }

		m, _, home := pathModel(t, size.w, size.h)
		gitFolder(t, home, "code/garden")
		plainFolder(t, home, "code/notes")

		setProjectValue(&m, "~/code/garden-planner")
		m.sform.in[sHarness].SetValue("pi")
		golden(t, name("new_session_path_new_folder"), m.testView())

		setProjectValue(&m, "~/code/garden")
		golden(t, name("new_session_path_register"), m.testView())

		setProjectValue(&m, "~/code/notes")
		golden(t, name("new_session_path_not_git"), m.testView())

		setProjectValue(&m, "/etc/agent-net-garden")
		golden(t, name("new_session_path_outside_home"), m.testView())

		setProjectValue(&m, "~/code/garden-planner")
		m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
		golden(t, name("new_session_create_confirm"), m.testView())
	}
}

func TestNewSessionPathTrustGoldens(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		name := func(s string) string { return "view_" + itoa(size.w) + "x" + itoa(size.h) + "_" + s }

		m, src, home := pathModel(t, size.w, size.h)
		m = quietFixtures(m)
		dir := gitFolder(t, home, "code/garden")
		setProjectValue(&m, "~/code/garden")
		m.sform.in[sHarness].SetValue("claude")
		golden(t, name("new_session_trust_note"), m.testView())

		src.sessionStart = protocol.StartedSession{ID: "start_1", Status: "needs_trust",
			NodeID: "n_mac", Hostname: "Mac", Harness: "claude", Project: "garden", Path: dir, StartedAt: t0.Unix()}
		m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
		m, _ = press2(m, cmd())
		golden(t, name("new_session_trust_confirm"), m.testView())
	}
}
