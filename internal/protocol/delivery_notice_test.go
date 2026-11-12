package protocol

import (
	"strings"
	"testing"
)

// The hand-back notice names the delivery change it recorded, or why the
// record failed, in one line each, under the head.
func TestWorkerNoticeNamesTheChange(t *testing.T) {
	n := WorkerNotice{Status: StatusDone, Session: "s_1", Harness: "claude", Model: "sonnet", Project: "shop", Host: "mac",
		Worktree: "/w", Branch: "agent-net/w_1", Head: "abc1234", Change: "ch_1"}
	out := FormatWorkerNotice(n, "done")
	if !strings.Contains(out, "head: abc1234\nchange: ch_1\nchanges: none\n") || strings.Contains(out, "delivery record:") {
		t.Errorf("notice with a change:\n%s", out)
	}
	n.Change, n.DeliveryError = "", "the store is full"
	out = FormatWorkerNotice(n, "done")
	if !strings.Contains(out, "head: abc1234\ndelivery record: the store is full\n") || strings.Contains(out, "\nchange: ") {
		t.Errorf("notice with a delivery error:\n%s", out)
	}
	n.DeliveryError = ""
	if out := FormatWorkerNotice(n, "done"); strings.Contains(out, "\nchange: ") || strings.Contains(out, "delivery record:") {
		t.Errorf("notice with no record:\n%s", out)
	}
}

// A hand-back that recorded a branch other than the start branch names it
// under the change line.
func TestWorkerNoticeNamesTheRecordedBranch(t *testing.T) {
	n := WorkerNotice{Status: StatusDone, Session: "s_1", Harness: "claude", Model: "sonnet", Project: "shop", Host: "mac",
		Worktree: "/w", Branch: "agent-net/w_1", Head: "abc1234", Change: "ch_1", DeliveryBranch: "chore/pin"}
	out := FormatWorkerNotice(n, "done")
	if !strings.Contains(out, "\nchange: ch_1\nrecorded branch: chore/pin\n") {
		t.Errorf("notice with a recorded branch:\n%s", out)
	}
	n.DeliveryBranch = ""
	if out := FormatWorkerNotice(n, "done"); strings.Contains(out, "recorded branch:") {
		t.Errorf("notice without a recorded branch:\n%s", out)
	}
}
