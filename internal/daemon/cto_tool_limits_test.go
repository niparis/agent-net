package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCTOClaudeEffectiveTools(t *testing.T) {
	for _, tc := range []struct{ name, report, want string }{
		{"private", `{"type":"system","subtype":"init","claude_code_version":"2.1.316","tools":["mcp__agent-net-cto__graph_get"]}`, ""},
		{"builtin", `{"type":"system","subtype":"init","claude_code_version":"2.1.316","tools":["Read"]}`, "Read"},
		{"plugin", `{"type":"system","subtype":"init","claude_code_version":"2.1.316","tools":["mcp__other__read"]}`, "mcp__other__read"},
		{"unknown", `{"type":"system","subtype":"init","claude_code_version":"9.0.0","tools":[]}`, "unknown harness version"},
		{"missing", `{"type":"system","subtype":"init","claude_code_version":"2.1.316"}`, "no effective tool list"},
		{"no report", "backgrounded", "no init record"},
		{"null", `{"type":"system","subtype":"init","claude_code_version":"2.1.316","tools":null}`, "no effective tool list"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkClaudeCTOInit([]byte(tc.report))
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

func TestCTOOpenCodeEffectiveTools(t *testing.T) {
	for _, tc := range []struct {
		name, version string
		rules         []ctoPermission
		extra, want   string
	}{
		{"private", "2.2.0", ctoPermissions(), "", ""},
		{"unknown", "9.0.0", ctoPermissions(), "", "unknown harness version"},
		{"default allow", "2.2.0", []ctoPermission{{"*", "*", "allow"}}, "", "built-in tools"},
		{"read allow", "2.2.0", append(ctoPermissions(), ctoPermission{"read", "*", "allow"}), "", "read"},
		{"future tool", "2.2.0", append(ctoPermissions(), ctoPermission{"future", "*", "allow"}), "", "future"},
		{"delegation", "2.2.0", ctoPermissions(), "general", "general"},
		{"missing rules", "2.2.0", nil, "", "built-in tools"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				agents := []any{map[string]any{"name": "cto", "permissions": tc.rules}}
				if tc.extra != "" {
					agents = append(agents, map[string]any{"name": tc.extra})
				}
				json.NewEncoder(w).Encode(map[string]any{"data": agents})
			}))
			defer srv.Close()
			d := &Daemon{}
			d.cto.base = srv.URL
			info, _ := json.Marshal(map[string]string{"version": tc.version})
			err := d.checkOpenCodeCTOTools(context.Background(), info)
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
