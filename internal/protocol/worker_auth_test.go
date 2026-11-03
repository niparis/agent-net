package protocol

import (
	"encoding/json"
	"testing"
)

func TestWorkerModelAuthFailureDetail(t *testing.T) {
	for _, tc := range []struct {
		detail string
		want   bool
	}{{"Request failed: 401", true}, {"HTTP 403: forbidden", true}, {"NOT LOGGED IN", true}, {"request failed: 1401", false}, {"4030 tokens", false}, {"insufficient funds", false}, {"rate limit 429", false}} {
		var stopped WorkerStopped
		raw, _ := json.Marshal(map[string]any{"failure": tc.detail})
		if err := json.Unmarshal(raw, &stopped); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(stopped)
		var fields map[string]any
		json.Unmarshal(encoded, &fields)
		if fields["failure"] != tc.detail {
			t.Fatalf("failure lost: %s", encoded)
		}
		if got := ModelAuthFailure(tc.detail); got != tc.want {
			t.Errorf("%q: %v, want %v", tc.detail, got, tc.want)
		}
	}
}
