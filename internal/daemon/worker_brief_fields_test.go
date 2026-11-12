package daemon

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCheckedWorkerStartRequiresProblemAndDone(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   protocol.WorkerStartRequest
		ok   bool
		want string
	}{
		{name: "legacy task only", in: protocol.WorkerStartRequest{Project: "shop", Task: "fix the login"}, ok: true},
		{name: "problem and done", in: protocol.WorkerStartRequest{Project: "shop", Problem: "the login fails", Done: []string{"the login test passes"}}, ok: true},
		{name: "problem and notes", in: protocol.WorkerStartRequest{Project: "shop", Problem: "the login fails", Done: []string{"the login test passes"}, Task: "branch from main"}, ok: true},
		{name: "problem without done", in: protocol.WorkerStartRequest{Project: "shop", Problem: "the login fails"}, want: "done is required"},
		{name: "blank done", in: protocol.WorkerStartRequest{Project: "shop", Problem: "the login fails", Done: []string{"  "}}, want: "done is required"},
		{name: "done without problem", in: protocol.WorkerStartRequest{Project: "shop", Done: []string{"the login test passes"}}, want: "problem is required"},
		{name: "no brief", in: protocol.WorkerStartRequest{Project: "shop"}, want: "problem is required"},
		{name: "continue needs no brief", in: protocol.WorkerStartRequest{Continue: "s_old"}, ok: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := checkedWorkerStart(tc.in)
			if tc.ok {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v does not name %q", err, tc.want)
			}
		})
	}
}
