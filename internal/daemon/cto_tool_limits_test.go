package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
			err := d.checkOpenCodeCTOAgent(context.Background(), info)
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
