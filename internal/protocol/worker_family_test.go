package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestWorkerStartNotFamilyRoundTrip(t *testing.T) {
	in := WorkerStartRequest{NotFamily: []string{"openai", "anthropic"}}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out WorkerStartRequest
	if err := json.Unmarshal(data, &out); err != nil || !reflect.DeepEqual(in.NotFamily, out.NotFamily) {
		t.Fatalf("round trip %s: %+v %v", data, out, err)
	}
}
