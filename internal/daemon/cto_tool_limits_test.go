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

func TestCTOOpenCodeEffectiveTools(t *testing.T) {
	cases := []struct {
		name, version string
		rules         []ctoPermission
		extra, want   string
	}{
		{"private", "2.2.0", ctoPermissions(), "", ""},
		{"mac version", "2.2.1", ctoPermissions(), "", ""},
		{"future version", "9.0.0", ctoPermissions(), "", ""},
		{"default primary agent", "2.2.1", ctoPermissions(), "build", ""},
		{"default allow", "2.2.0", []ctoPermission{{"*", "*", "allow"}}, "", "built-in tools"},
		{"read allow", "2.2.0", append(ctoPermissions(), ctoPermission{"read", "*", "allow"}), "", "read"},
		{"future tool", "2.2.0", append(ctoPermissions(), ctoPermission{"future", "*", "allow"}), "", "future"},
		{"delegation", "2.2.0", ctoPermissions(), "general", "general"},
		{"missing rules", "2.2.0", nil, "", "deny"},
	}
	for i, rule := range ctoPermissions() {
		if rule.Effect != "deny" {
			continue
		}
		missing := append([]ctoPermission{}, ctoPermissions()[:i]...)
		missing = append(missing, ctoPermissions()[i+1:]...)
		cases = append(cases, struct {
			name, version string
			rules         []ctoPermission
			extra, want   string
		}{"missing " + rule.Action, "2.2.1", missing, "", rule.Action})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				agents := []any{map[string]any{"name": "cto", "permissions": tc.rules}}
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
			err := d.checkOpenCodeCTOTools(context.Background(), info)
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
