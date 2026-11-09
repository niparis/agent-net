package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// The worker log detail shows how many brief defects are charged to the
// worker's caller. It must not read as a defect of the worker itself.
func TestWorkerLogDetailsShowsBriefDefects(t *testing.T) {
	detail := ansi.Strip(strings.Join(workerLogDetails(protocol.WorkerLogEntry{
		Status: "done", Caller: "agent-net", CallerHost: "mac", BriefDefects: 2,
	}, 120), "\n"))
	if !strings.Contains(detail, "2 brief defects") {
		t.Fatalf("the brief defect count is missing:\n%s", detail)
	}
	// The label must not widen the column of the other facts.
	if !strings.Contains(detail, "Brief       2 brief defects") || !strings.Contains(detail, "Caller      agent-net (mac)") {
		t.Fatalf("the label column changed width:\n%s", detail)
	}
	clean := strings.Join(workerLogDetails(protocol.WorkerLogEntry{Status: "done", Caller: "agent-net"}, 120), "\n")
	if strings.Contains(clean, "brief defect") {
		t.Fatalf("a zero count should not show:\n%s", clean)
	}
}
