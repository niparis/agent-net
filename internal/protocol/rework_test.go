package protocol

import "testing"

func TestReworkCounts(t *testing.T) {
	for _, tc := range []struct {
		handback, partial, idle bool
		cont, fix               int
	}{
		{false, false, false, 0, 0}, {false, true, false, 0, 0},
		{false, false, true, 1, 0}, {true, false, false, 0, 1},
		{true, true, false, 1, 0}, {true, false, true, 0, 1}, {true, true, true, 1, 0},
	} {
		cont, fix := ReworkCounts(tc.handback, tc.partial, tc.idle)
		if cont != tc.cont || fix != tc.fix {
			t.Errorf("%+v: got %d %d", tc, cont, fix)
		}
	}
}

// The marker counts on the first or the last nonempty line. When both lines
// carry one and they differ, the last wins.
func TestWorkerReportCompleteFirstOrLastLine(t *testing.T) {
	for _, c := range []struct {
		name, report string
		complete, ok bool
	}{
		{"last line no", "Did half.\ncomplete: no", false, true},
		{"last line yes", "Did it.\ncomplete: yes", true, true},
		{"first line no", "complete: no\nI stopped early because the build broke.", false, true},
		{"first line yes", "complete: yes\nAll done.", true, true},
		{"first line no after blank lines", "\n\n  COMPLETE: NO  \n\nWhy I stopped.\n", false, true},
		{"both no", "complete: no\nwhy\ncomplete: no", false, true},
		{"first no, last yes: last wins", "complete: no\nwhy\ncomplete: yes", true, true},
		{"first yes, last no: last wins", "complete: yes\nwhy\ncomplete: no", false, true},
		{"marker only in the middle", "intro\ncomplete: no\nmore text", true, false},
		{"single line", "complete: no", false, true},
		{"no marker", "No marker here.", true, false},
		{"unknown value", "complete: maybe\nwhy", true, false},
		{"empty", "", true, false},
	} {
		complete, ok := WorkerReportComplete(c.report)
		if complete != c.complete || ok != c.ok {
			t.Errorf("%s: %q: got %v %v, want %v %v", c.name, c.report, complete, ok, c.complete, c.ok)
		}
	}
}
