package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// OpenCode lists a folder's agents only after it has loaded the folder's
// config, which can be a second or more after the server first answers. The
// start must wait for the cto agent rather than fail on the first listing.
func TestCTOOpenCodeAgentLoadsAfterFirstAnswer(t *testing.T) {
	var lists atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent" {
			http.NotFound(w, r)
			return
		}
		name := "build"
		if lists.Add(1) > 3 {
			name = "cto"
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"name": name}}})
	}))
	defer srv.Close()
	d := &Daemon{log: log.New(io.Discard, "", 0)}
	d.cto.base = srv.URL
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.checkOpenCodeCTOAgent(ctx, []byte(`{"version":"2.3.0"}`)); err != nil {
		t.Fatalf("the cto agent was listed on call 4, but the start failed: %v (calls %d)", err, lists.Load())
	}
}

// The CTO has full power (decision 0157): built-in tools and subagents do not
// stop the start. Only a missing cto agent does.
func TestCTOOpenCodeEffectiveTools(t *testing.T) {
	cases := []struct {
		name, version string
		rules         []ctoPermission
		extra, want   string
	}{
		{"private", "2.2.0", ctoPermissions(), "", ""},
		{"future version", "9.0.0", ctoPermissions(), "", ""},
		{"default allow", "2.2.0", []ctoPermission{{"*", "*", "allow"}}, "", ""},
		{"read allow", "2.2.0", append(ctoPermissions(), ctoPermission{"read", "*", "allow"}), "", ""},
		{"general subagent", "2.2.0", ctoPermissions(), "general", ""},
		{"no cto agent", "2.2.0", nil, "", "missing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/openapi.json" {
					http.NotFound(w, r)
					return
				}
				name := "cto"
				if tc.want == "missing" {
					name = "build"
				}
				agents := []any{map[string]any{"name": name, "permissions": tc.rules}}
				if tc.extra != "" {
					agents = append(agents, map[string]any{"name": tc.extra})
				}
				json.NewEncoder(w).Encode(map[string]any{"data": agents})
			}))
			defer srv.Close()
			var logs bytes.Buffer
			d := &Daemon{log: log.New(&logs, "", 0)}
			d.cto.base = srv.URL
			info, _ := json.Marshal(map[string]string{"version": tc.version})
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			err := d.checkOpenCodeCTOAgent(ctx, info)
			if !strings.Contains(logs.String(), tc.version) {
				t.Fatalf("version was not logged: %q", logs.String())
			}
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

// OpenCode asks before a read of a secrets file, and no one answers for the CTO.
// Its own rules allow those reads, so the CTO does not stall (decision 0171).
func TestCTOPermissionsAllowSecretFileReads(t *testing.T) {
	allowed := map[string]bool{"*.env": false, "*.env.*": false}
	for _, rule := range ctoPermissions() {
		if rule.Action == "read" && rule.Effect == "allow" {
			if _, ok := allowed[rule.Resource]; ok {
				allowed[rule.Resource] = true
			}
		}
	}
	for resource, ok := range allowed {
		if !ok {
			t.Fatalf("the CTO's rules do not allow read %s", resource)
		}
	}
}
