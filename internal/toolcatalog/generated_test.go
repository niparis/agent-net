package toolcatalog

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type generatedTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Roles       []Role          `json:"roles"`
	Method      string          `json:"method"`
}

func TestGeneratedPluginCatalogsAreCurrent(t *testing.T) {
	for _, dir := range []string{"../../plugins/pi", "../../plugins/opencode"} {
		path := filepath.Join(dir, "tools.gen.ts")
		want, err := RenderPluginTools()
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: generated catalog missing or unreadable: %v", path, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is out of date; run go generate ./internal/toolcatalog", path)
		}
		catalog := parseGenerated(t, path, got)
		for _, role := range []Role{RoleAgent, RoleWorker} {
			var wantNames, gotNames []string
			for _, tool := range ForRole(role) {
				if tool.Name != "agent_file_send" && (role == RoleAgent || hasRole(tool.Roles, RoleWorker)) {
					wantNames = append(wantNames, tool.Name)
				}
			}
			for _, tool := range catalog {
				if hasRole(tool.Roles, role) {
					gotNames = append(gotNames, tool.Name)
				}
			}
			if !reflect.DeepEqual(gotNames, wantNames) {
				t.Errorf("%s role %s tools = %v, want %v", path, role, gotNames, wantNames)
			}
		}
	}
}

func hasRole(roles []Role, role Role) bool {
	for _, got := range roles {
		if got == role {
			return true
		}
	}
	return false
}

func parseGenerated(t *testing.T, path string, data []byte) []generatedTool {
	t.Helper()
	const prefix = "export const TOOL_CATALOG = "
	text := string(data)
	start := bytes.Index(data, []byte(prefix))
	if start < 0 {
		t.Errorf("%s: missing generated TOOL_CATALOG", path)
		return nil
	}
	text = text[start+len(prefix):]
	end := bytes.Index([]byte(text), []byte(" as const;"))
	if end < 0 {
		t.Errorf("%s: malformed generated TOOL_CATALOG", path)
		return nil
	}
	var tools []generatedTool
	if err := json.Unmarshal([]byte(text[:end]), &tools); err != nil {
		t.Errorf("%s: decode generated TOOL_CATALOG: %v", path, err)
	}
	return tools
}
