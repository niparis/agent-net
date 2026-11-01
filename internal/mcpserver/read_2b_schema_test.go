package mcpserver

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
)

// Every new read tool is listed with the catalog's exact name, description and
// input schema, and every result follows the catalog output schema (see
// roCall). The old tools keep their own definitions.
func TestReadToolsUseTheCatalogAndValidateTheirOutput(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "n_me")
	f := roReadFixture()
	cs := connect(t, f.call)
	ctx := context.Background()

	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]json.RawMessage{}
	outputByName := map[string]json.RawMessage{}
	for _, tool := range list.Tools {
		raw, _ := json.Marshal(tool.InputSchema)
		byName[tool.Name] = raw
		out, _ := json.Marshal(tool.OutputSchema)
		outputByName[tool.Name] = out
	}
	calls := map[string]map[string]any{
		"agent_messages_find":        {},
		"agent_message_get":          {"message": "m_59"},
		"agent_workers_find":         {},
		"agent_graph_read":           {},
		"agent_mcp_servers_find":     {},
		"agent_worker_profiles_find": {},
	}
	for name, args := range calls {
		spec := roCatalogTool(t, name)
		listed, ok := byName[name]
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		var want, got any
		json.Unmarshal(spec.InputSchema, &want)
		json.Unmarshal(listed, &got)
		wantRaw, _ := json.Marshal(want)
		gotRaw, _ := json.Marshal(got)
		if string(wantRaw) != string(gotRaw) {
			t.Errorf("%s input schema differs from the catalog\ncatalog: %s\nlisted:  %s", name, wantRaw, gotRaw)
		}
		var wantOut, gotOut any
		json.Unmarshal(spec.OutputSchema, &wantOut)
		json.Unmarshal(outputByName[name], &gotOut)
		wantOutRaw, _ := json.Marshal(wantOut)
		gotOutRaw, _ := json.Marshal(gotOut)
		if string(wantOutRaw) != string(gotOutRaw) {
			t.Errorf("%s output schema differs from the catalog\ncatalog: %s\nlisted:  %s", name, wantOutRaw, gotOutRaw)
		}
		for _, tool := range list.Tools {
			if tool.Name == name && tool.Description != spec.Description {
				t.Errorf("%s description differs from the catalog", name)
			}
		}
		roCall(t, cs, name, args)
	}
}

// The new tools are added beside the old ones. The old names, descriptions and
// handlers do not change in this task.
func TestOldReadToolsStillWork(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "n_me")
	f := roReadFixture()
	cs := connect(t, f.call)
	for _, name := range []string{"agent_messages", "agent_message_status", "agent_graph", "agent_mcp_list", "agent_worker_profiles"} {
		list, err := cs.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, tool := range list.Tools {
			if tool.Name == name {
				found = true
			}
		}
		if !found {
			t.Errorf("old tool %s disappeared", name)
		}
	}
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
