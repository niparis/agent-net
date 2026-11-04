package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestWorkerLogTodayTitleSpace(t *testing.T) {
	r := protocol.WorkerLogEntry{StartedAt: time.Now().Unix(), Model: "a-very-long-model-name", Difficulty: "medium", Caller: "caller-name", Title: "Review worker implementation", Status: "done"}
	line := ansi.Strip(workerLogLine(r, 76, false, time.Local))
	if !strings.Contains(line, "Review worker implementation") {
		t.Fatalf("title truncated: %q", line)
	}
	if strings.Contains(line, time.Now().Format("01-02")) {
		t.Fatalf("today has date: %q", line)
	}
}

func TestInboxCountKeepsTitleSeparator(t *testing.T) {
	m, _ := inboxAllKindsModel(80, 24)
	line := ansi.Strip(m.titleLine(80))
	if !strings.Contains(line, "Inbox (") || !strings.Contains(line, " │ ") {
		t.Fatalf("missing count or separator: %q", line)
	}
}
