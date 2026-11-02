package funnel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCleanupOwnedRoute(t *testing.T) {
	status := `{"BackendState":"Running","Self":{"DNSName":"test.example.ts.net.","Capabilities":["funnel"]}}`
	for _, tc := range []struct {
		name, config     string
		fail             bool
		wantOff, wantErr bool
	}{
		{"ours", `{"TCP":{"8443":{"HTTPS":true}},"Web":{"test.example.ts.net:8443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7422"}}}},"AllowFunnel":{"test.example.ts.net:8443":true}}`, false, true, false},
		{"repurposed", `{"TCP":{"8443":{"HTTPS":true}},"Web":{"test.example.ts.net:8443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:9999"}}}}}`, false, false, false},
		{"shared port", `{"TCP":{"8443":{"HTTPS":true}},"Web":{"test.example.ts.net:8443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7422"},"/other":{"Proxy":"http://127.0.0.1:9999"}}}}}`, false, false, false},
		{"tcp forward", `{"TCP":{"8443":{"TCPForward":"127.0.0.1:9000"}},"Web":{"test.example.ts.net:8443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7422"}}}}}`, false, false, false},
		{"failure", `{"Web":{"test.example.ts.net:8443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7422"}}}}}`, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := SaveRecord(dir, Record{Port: 8443, Target: "http://127.0.0.1:7422"}); err != nil {
				t.Fatal(err)
			}
			results := []result{{stdout: status}, {stdout: tc.config}}
			if tc.wantOff {
				v := result{}
				if tc.fail {
					v.err = errors.New("failed")
				}
				results = append(results, v)
			}
			r := &fakeRunner{t: t, results: results}
			removed, err := Cleanup(context.Background(), r, dir)
			if (err != nil) != tc.wantErr || removed != (tc.wantOff && !tc.fail) {
				t.Fatalf("removed=%v err=%v calls=%v", removed, err, r.calls)
			}
			_, statErr := os.Stat(filepath.Join(dir, "funnel.json"))
			if tc.fail && statErr != nil {
				t.Fatalf("record lost: %v", statErr)
			}
			if !tc.fail && !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("record remains: %v", statErr)
			}
		})
	}
}
func TestStatusRouteTargets(t *testing.T) {
	r := &fakeRunner{t: t, results: []result{{stdout: fixture(t, "status")}, {stdout: fixture(t, "serve")}}}
	info, err := Status(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Routes) != 2 || info.Routes[0].Port != 443 || info.Routes[0].Target != "http://127.0.0.1:7411" || info.Routes[1].Port != 8443 || info.Routes[1].Target != "http://127.0.0.1:7422" {
		t.Fatalf("routes: %+v", info.Routes)
	}
}
