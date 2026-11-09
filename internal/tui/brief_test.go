package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// A brief is one informational row on the Desk. It is labelled with its date,
// shows its body, and offers dismiss only: no approve or refuse.
func TestDeskBriefIsOneDismissableInformationalItem(t *testing.T) {
	m := deskModel(t, 120, 40)
	m.asks = append(m.asks, protocol.Ask{ID: "i_brief", Kind: protocol.AskKindBrief, State: protocol.AskStateOpen,
		Next: protocol.AskNextOwner, FromKind: protocol.AskFromGateway, From: "Daily brief 2026-11-09",
		Question: "Handled two fixes.\nOne decision waits.", CreatedAt: t0.Add(-10 * time.Minute)})
	m.inboxIndex = m.askRowCount() - 1
	view := ansi.Strip(m.testView())
	for _, want := range []string{"Daily brief 2026-11-09", "Handled two fixes."} {
		if !strings.Contains(view, want) {
			t.Errorf("the brief is not on the Desk: missing %q\n%s", want, view)
		}
	}
	for _, not := range []string{"wants approval"} {
		if strings.Contains(view, not) {
			t.Errorf("the brief looks like an approval ask: %q\n%s", not, view)
		}
	}
	rows := m.inboxRows()
	row := rows[len(rows)-1]
	if row.kind != "brief" {
		t.Fatalf("brief row kind = %q", row.kind)
	}
	if line := ansi.Strip(m.inboxRowLine(row, 20, 100, false)); strings.Contains(line, "[approval]") || strings.Contains(line, "[question]") {
		t.Fatalf("brief row = %q", line)
	}
	hints := m.inboxItemHints(row)
	if len(hints) != 1 || hints[0].label != "dismiss" {
		t.Fatalf("brief actions = %+v, want dismiss only", hints)
	}
	if got := m.inboxPanelTitle(row); got != "Daily brief 2026-11-09" {
		t.Fatalf("panel title = %q", got)
	}
	if got := m.inboxPanelText(row); !strings.Contains(got, "One decision waits.") {
		t.Fatalf("panel text = %q", got)
	}
}
