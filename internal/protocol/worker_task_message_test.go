package protocol

import (
	"encoding/json"
	"testing"
)

func TestWorkerTaskMessageCompatibility(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{`{"body":"[agent-net worker brief] old","message_type":"request"}`, true},
		{`{"body":"[agent-net worker brief] quote","message_type":"request","worker_task":false}`, false},
		{`{"body":"task without prefix","message_type":"request","worker_task":true}`, true},
		{`{"body":"[agent-net worker brief] reply","message_type":"reply"}`, false},
	} {
		var m Message
		if err := json.Unmarshal([]byte(tc.raw), &m); err != nil {
			t.Fatal(err)
		}
		if got := IsWorkerTask(m); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.raw, got, tc.want)
		}
	}
}
