package protocol

import "testing"

func TestSessionType(t *testing.T) {
	for _, tc := range []struct {
		h, typ string
		worker *WorkerInfo
		want   string
	}{
		{h: "claude", want: "agent"}, {h: "claude", worker: &WorkerInfo{}, want: "worker"},
		{h: "cto", want: "cto"}, {h: "owner", want: "owner"}, {h: "bot", want: "bot"},
		{h: "claude", typ: "merger", want: "merger"},
	} {
		if got := SessionType(tc.h, tc.worker, tc.typ); got != tc.want {
			t.Fatalf("type = %q, want %q", got, tc.want)
		}
	}
}
