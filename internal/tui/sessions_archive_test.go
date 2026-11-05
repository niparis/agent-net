package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
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
	golden(t, "sessions_archive_preview_80x24", m.testView())
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
	golden(t, "sessions_archive_confirm_80x24", m.testView())
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
