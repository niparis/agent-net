package protocol

import (
	"strings"
	"testing"
)

func TestVisualProofNotice(t *testing.T) {
	n := WorkerNotice{VisualEvidence: VisualEvidence{UserVisible: "yes", VisualProof: []VisualProof{{File: "frame.txt", Caption: "scroll: last row visible"}}}}
	body := FormatWorkerNotice(n, "done")
	for _, want := range []string{"user_visible: yes", "frame.txt", "scroll: last row visible"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s", want)
		}
	}
}

func TestVisualNoticeOrder(t *testing.T) {
	n := WorkerNotice{Status: "done", Worktree: "/tmp/worktree", Change: "ch_example", DeliveryError: "example warning", VisualEvidence: VisualEvidence{VisualProof: []VisualProof{{File: "frame.txt", Caption: "open"}}}}
	body := FormatWorkerNotice(n, "done")
	if !strings.HasPrefix(body, "[agent-net worker notice]\nstatus: done\n") {
		t.Fatalf("status must be first notice fact: %s", body)
	}
	if strings.Contains(body, "user_visible:") {
		t.Fatal("empty user_visible printed")
	}
	if strings.Index(body, "visual proof:") < strings.Index(body, "delivery record:") || strings.Index(body, "visual proof:") < strings.Index(body, "change:") {
		t.Fatal("proof precedes delivery facts")
	}
}
