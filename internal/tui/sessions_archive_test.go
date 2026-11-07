package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

type archiveSource struct {
	Source
	calls []protocol.SessionsArchiveRequest
}

func (s *archiveSource) Call(_ context.Context, method string, args any) (json.RawMessage, error) {
	b, _ := json.Marshal(args)
	var q protocol.SessionsArchiveRequest
	_ = json.Unmarshal(b, &q)
	s.calls = append(s.calls, q)
	return json.Marshal(protocol.SessionsArchiveResult{Count: 1, Sessions: []protocol.SessionArchiveEntry{{SessionID: "s_junk", Name: "judge-junk", Folder: "/tmp/judge-junk", Harness: "pi"}}})
}

func TestSessionsArchivePreviewConfirmAndRestore(t *testing.T) {
	m, _ := workerModel(80, 24)
	src := &archiveSource{Source: m.src}
	m.src = src
	m.showOffline = true
	m, cmd := m.startSessionsArchive()
	if cmd != nil {
		t.Fatal("archive acted before selection")
	}
	m, cmd = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("no dry run")
	}
	m2, _ := m.Update(cmd())
	m = m2.(Model)
	if !strings.Contains(m.testView(), "judge-junk") || !strings.Contains(m.testView(), "Archive 1 offline session") {
		t.Fatalf("missing dry run: %s", m.testView())
	}
	if len(src.calls) != 1 || !src.calls[0].DryRun {
		t.Fatal("dry run mutated")
	}
	m, cmd = press(m, tea.KeyPressMsg{Text: "y", Code: 'y'})
	if cmd != nil {
		t.Fatal("preview accepted without opening confirmation")
	}
	m, cmd = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(m.testView(), "Confirm archive") || cmd != nil {
		t.Fatal("no confirmation")
	}
	m, cmd = press(m, tea.KeyPressMsg{Text: "y", Code: 'y'})
	if cmd == nil {
		t.Fatal("no apply")
	}
	_ = cmd()
	if len(src.calls) != 2 || src.calls[1].DryRun || len(src.calls[1].Expected) != 1 || src.calls[1].IDs[0] != "s_junk" {
		t.Fatalf("apply not pinned: %+v", src.calls)
	}
	m.archive = nil
	m, _ = m.startSessionsArchive()
	m, cmd = press(m, tea.KeyPressMsg{Text: "r", Code: 'r'})
	if cmd == nil {
		t.Fatal("no archived list")
	}
	m2, _ = m.Update(cmd())
	m = m2.(Model)
	if !strings.Contains(m.testView(), "Restore 1 archived session") {
		t.Fatal("no restore preview")
	}
	m, cmd = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m, cmd = press(m, tea.KeyPressMsg{Code: 'y', Text: "y"})
	_ = cmd()
	if !src.calls[len(src.calls)-1].Restore {
		t.Fatal("restore did not use restore operation")
	}
}

func archiveScreenModel(w, h int) Model {
	m, _ := workerModel(w, h)
	m, _ = m.startSessionsArchive()
	entries := []protocol.SessionArchiveEntry{}
	folders := []string{"/tmp/judges/run-02", "/tmp/judges/run-01", "/tmp/harness/pi-01", "/tmp/harness/codex-02", "/work/shop", "/work/docs", "/var/tmp/tests/run-04", "/var/tmp/tests/run-03", "/home/user/code/shop/.worktrees/ended-worker", "/tmp/scratch"}
	names := []string{"judge-beta", "judge-alpha", "pi-test", "codex-test", "shop-review", "docs-agent", "test-four", "test-three", "shop-worker", "scratch"}
	for i := range 10 {
		project := ""
		if i == 4 || i == 8 {
			project = "shop"
		}
		if i == 5 {
			project = "docs"
		}
		entries = append(entries, protocol.SessionArchiveEntry{SessionID: fmt.Sprintf("s_%02d", i), Name: names[i], Project: project, Harness: []string{"codex", "pi", "claude"}[i%3], Machine: []string{"build", "laptop"}[i%2], LastSeen: t0.Add(-time.Duration(i+1) * 24 * time.Hour).Unix(), Folder: folders[i]})
	}
	m, _ = m.sessionsArchiveResult(sessionsArchiveMsg{result: protocol.SessionsArchiveResult{Count: len(entries), Sessions: entries}})
	return m
}

func TestSessionsArchiveScreens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := archiveScreenModel(size[0], size[1])
		for _, stage := range []string{"preview", "confirm", "restore"} {
			m.archive.stage = "preview"
			m.archive.query.Restore = false
			if stage == "confirm" {
				m.archive.stage = "confirm"
			}
			if stage == "restore" {
				m.archive.query.Restore = true
			}
			view := ansi.Strip(m.testView())
			for _, want := range []string{"1 Sessions", "╭─", "╭─CTO", "NAME", "PROJECT", "HARNESS", "MACHINE", "LAST SEEN", "FOLDER", "no project", "ago"} {
				if !strings.Contains(view, want) {
					t.Errorf("%dx%d %s missing %q:\n%s", size[0], size[1], stage, want, view)
				}
			}
			if strings.Contains(view, "session(s)") {
				t.Fatal("placeholder plural")
			}
			rows := strings.Split(view, "\n")
			if len(rows) != size[1] {
				t.Fatalf("height %d", len(rows))
			}
			for _, row := range rows {
				if ansi.StringWidth(row) > size[0] {
					t.Fatalf("row exceeds width: %q", row)
				}
			}
			golden(t, fmt.Sprintf("sessions_archive_%s_%dx%d", stage, size[0], size[1]), m.testView())
		}
	}
}

func TestSessionsArchiveSortAndPlural(t *testing.T) {
	m := archiveScreenModel(80, 24)
	for i := 1; i < len(m.archive.entries); i++ {
		if path.Dir(m.archive.entries[i-1].Folder) > path.Dir(m.archive.entries[i].Folder) || (path.Dir(m.archive.entries[i-1].Folder) == path.Dir(m.archive.entries[i].Folder) && m.archive.entries[i-1].LastSeen > m.archive.entries[i].LastSeen) {
			t.Fatal("folders not sorted")
		}
	}
	m.archive.entries = m.archive.entries[:1]
	if view := m.testView(); !strings.Contains(view, "Archive 1 offline session") || strings.Contains(view, "1 offline sessions") {
		t.Fatal(view)
	}
}

func TestRemoveOfflineSession(t *testing.T) {
	m, _ := workerModel(80, 24)
	src := &archiveSource{Source: m.src}
	m.src = src
	a := &m.snap.Agents[0]
	a.Online = false
	a.Worker = nil
	m.selected = a.SessionID
	m, _ = m.startConfirm()
	if !strings.Contains(m.testView(), "from agent-net for good?") {
		t.Fatalf("missing removal confirmation: %s", m.testView())
	}
	m, cmd := press(m, tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil {
		t.Fatal("missing archive call")
	}
	msg := cmd()
	m2, _ := m.Update(msg)
	m = m2.(Model)
	if len(src.calls) != 1 || len(src.calls[0].IDs) != 1 || src.calls[0].IDs[0] != a.SessionID {
		t.Fatalf("wrong call: %+v", src.calls)
	}
	for _, row := range m.snap.Agents {
		if row.SessionID == a.SessionID {
			t.Fatal("row remains")
		}
	}
}

func TestRemoveOfflineInView(t *testing.T) {
	m, _ := workerModel(80, 24)
	src := &archiveSource{Source: m.src}
	m.src = src
	m.showOffline = true
	for i := range m.snap.Agents {
		m.snap.Agents[i].Online = false
	}
	visible := m.visible()
	if len(visible) == 0 {
		t.Fatal("no visible sessions")
	}
	m, cmd := press(m, tea.KeyPressMsg{Code: 'X', Text: "X"})
	if cmd != nil || !strings.Contains(m.testView(), "offline sessions shown?") {
		t.Fatal("missing bulk confirmation")
	}
	m, cmd = press(m, tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil {
		t.Fatal("missing archive call")
	}
	msg := cmd()
	m2, _ := m.Update(msg)
	m = m2.(Model)
	if len(src.calls) != 1 || len(src.calls[0].IDs) != len(visible) {
		t.Fatalf("wrong IDs: %+v", src.calls)
	}
	for _, a := range visible {
		for _, left := range m.snap.Agents {
			if a.SessionID == left.SessionID {
				t.Fatal("row remains")
			}
		}
	}
}

func TestRemoveConfirmationScreens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		for _, bulk := range []bool{false, true} {
			m, _ := workerModel(size[0], size[1])
			m.showOffline = true
			m.snap.Agents[0].Online = false
			m.snap.Agents[0].Worker = nil
			m.selected = m.snap.Agents[0].SessionID
			name := "single"
			if bulk {
				name = "all"
				m, _ = m.startRemoveOffline()
			} else {
				m, _ = m.startConfirm()
			}
			golden(t, fmt.Sprintf("remove_%s_%dx%d", name, size[0], size[1]), m.testView())
		}
	}
}

func TestOnlineStillConfirmsStop(t *testing.T) {
	m, _ := workerModel(80, 24)
	m.snap.Agents[0].Online = true
	m.snap.Agents[0].Worker = nil
	m.selected = m.snap.Agents[0].SessionID
	m, _ = m.startConfirm()
	if !strings.Contains(m.testView(), "Conversation kept.") || len(m.removeIDs) != 0 {
		t.Fatal("online session did not ask to stop")
	}
	_, cmd := press(m, tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil {
		t.Fatal("missing stop command")
	}
}
