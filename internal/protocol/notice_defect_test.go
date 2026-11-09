package protocol

import (
	"strings"
	"testing"
)

// The notice checklist tells the caller what to do with a defect it finds and
// with rework caused by its own brief.
func TestNoticeChecklistStatesDefectRule(t *testing.T) {
	got := noticeChecklist("", false)
	for _, want := range []string{
		"Found a defect in earlier work?",
		"fixes=<change>",
		"defect_found_later",
		"brief_defect",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the checklist does not contain %q:\n%s", want, got)
		}
	}
}

// A hand-back hint reaches the caller in the notice.
func TestWorkerNoticeIncludesDefectHint(t *testing.T) {
	hint := "This touches files from ch_1 by s_other (merged 2026-11-08). If it fixes that work, record it: agent_worker_rate defect_found_later on s_other, or submit with fixes=ch_1."
	n := WorkerNotice{Status: StatusDone, Session: "s_me", Harness: "opencode", Project: "shop", Host: "mac", DefectHint: hint}
	got := FormatWorkerNotice(n, "")
	if !strings.Contains(got, hint) {
		t.Errorf("the notice does not carry the defect hint:\n%s", got)
	}
}
