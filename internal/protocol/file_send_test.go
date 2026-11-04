package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestFileSendToolJSONRoundTrip(t *testing.T) {
	for _, v := range []any{
		FileSendRequest{To: "s_bob", Paths: []string{"/tmp/report.txt"}, Note: "please read"},
		FileSendResult{MessageID: "m_test", Files: []FileSendDestination{{Name: "report.txt", Path: "inbox/s_bob/m_test/report.txt"}}, Error: "transfer failed"},
	} {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		dst := reflect.New(reflect.TypeOf(v))
		if err := json.Unmarshal(raw, dst.Interface()); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(v, dst.Elem().Interface()) {
			t.Fatalf("round trip %s: got %+v, want %+v", raw, dst.Elem().Interface(), v)
		}
	}
}
