package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// A v0.3.5 daemon sends no "primary" or "others"; a v0.3.5 gateway or client
// ignores them. Both directions must work, and the old fields keep their names.
func TestHarnessInfoWireCompat(t *testing.T) {
	var old HarnessInfo
	if err := json.Unmarshal([]byte(`{"name":"pi","path":"/p","version":"1","models":["a/b"]}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.Name != "pi" || old.Primary || old.Others != nil {
		t.Fatalf("old payload: %+v", old)
	}
	plain, _ := json.Marshal(HarnessInfo{Name: "pi", Path: "/p", Version: "1"})
	if strings.Contains(string(plain), "primary") || strings.Contains(string(plain), "others") {
		t.Fatalf("empty new fields must be omitted: %s", plain)
	}
	raw, _ := json.Marshal(HarnessInfo{Name: "pi", Path: "/p", Version: "1", Primary: true, Others: []HarnessInstall{{Path: "/q", Version: "0.9"}}})
	var legacy struct {
		Name    string `json:"name"`
		Path    string `json:"path"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &legacy); err != nil || legacy.Path != "/p" || legacy.Version != "1" {
		t.Fatalf("legacy reader: %+v %v", legacy, err)
	}
	var back HarnessInfo
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !back.Primary || len(back.Others) != 1 || back.Others[0].Path != "/q" {
		t.Fatalf("round trip: %+v", back)
	}
}
