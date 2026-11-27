package toolcatalog

import (
	"slices"
	"strings"
	"testing"
)

// RenamedNames lists every old name once, sorted, so a parity test can check
// the whole list without its own copy.
func TestRenamedNamesListsEveryOldName(t *testing.T) {
	got := RenamedNames()
	if len(got) != len(renamed) {
		t.Fatalf("RenamedNames has %d names, the table has %d", len(got), len(renamed))
	}
	if !slices.IsSorted(got) {
		t.Fatalf("RenamedNames is not sorted: %v", got)
	}
	if !slices.Contains(got, "overview") {
		t.Fatalf("RenamedNames misses an old CTO tool: %v", got)
	}
}

// Every old name points at tools that exist in the catalog, and none of the
// old names is still a catalog tool.
func TestRenamedPointsAtCatalogTools(t *testing.T) {
	catalog := map[string]bool{}
	for _, tool := range Tools {
		catalog[tool.Name] = true
	}
	if len(renamed) == 0 {
		t.Fatal("no old names listed")
	}
	for old, repl := range renamed {
		if catalog[old] {
			t.Errorf("old name %s is still a catalog tool", old)
		}
		if len(repl) == 0 {
			t.Errorf("old name %s has no replacement", old)
		}
		for _, name := range repl {
			if !catalog[name] {
				t.Errorf("old name %s points at %s, which is not a catalog tool", old, name)
			}
		}
	}
}

func TestRenamedErrorIsOneLine(t *testing.T) {
	for old, want := range map[string]string{
		"ui":                 "ui was renamed to cto_ui_show.",
		"message":            "message was renamed to agent_message_send or agent_message_reply.",
		"agent_list":         "agent_list was renamed to agent_sessions_find.",
		"agent_graph":        "agent_graph was renamed to agent_graph_read.",
		"workers":            "workers was renamed to agent_workers_find.",
		"agent_rate":         "agent_rate was renamed to agent_worker_rate.",
		"agent_nodes":        "agent_nodes was renamed to agent_machines_find or agent_machine_get.",
		"ask_owner":          "ask_owner was renamed to agent_message_send.",
		"graph":              "graph was renamed to agent_graph_read.",
		"agent_send":         "agent_send was renamed to agent_message_send.",
		"overview":           "overview was renamed to agent_sessions_find or agent_machines_find.",
		"agent_reply":        "agent_reply was renamed to agent_message_reply.",
		"agent_start":        "agent_start was renamed to agent_worker_start.",
		"agent_stop":         "agent_stop was renamed to agent_worker_stop.",
		"proposals":          "proposals was renamed to cto_owner_items_find.",
		"cto_proposals_find": "cto_proposals_find was renamed to cto_owner_items_find.",
		"learn":              "learn was renamed to agent_learn.",
		"agent_get":          "agent_get was renamed to agent_session_get.",
		"tools":              "tools was renamed to agent_mcp_servers_find.",
		"settings":           "settings was renamed to agent_settings_read or agent_worker_profiles_find.",
		"agent_ask_owner":    "agent_ask_owner was renamed to agent_message_send.",
	} {
		err := RenamedError(old)
		if err == nil {
			t.Fatalf("%s: no error", old)
		}
		if err.Error() != want {
			t.Errorf("%s: got %q, want %q", old, err.Error(), want)
		}
		if strings.Contains(err.Error(), "\n") {
			t.Errorf("%s: error has more than one line", old)
		}
	}
	for _, name := range []string{"agent_sessions_find", "cto_ui_show", "graph_change", "github__search"} {
		if err := RenamedError(name); err != nil {
			t.Errorf("%s is not an old name: %v", name, err)
		}
	}
}

// The owner ask tool is gone (decision 0196): a new agent sends a request to
// the chief, and an older model that remembers an old name is pointed there.
func TestOwnerAskIsNotACatalogTool(t *testing.T) {
	for _, tool := range Tools {
		if tool.Name == "agent_owner_ask" {
			t.Fatal("the catalog still lists agent_owner_ask")
		}
	}
	for _, old := range []string{"agent_ask_owner", "agent_request_approval", "agent_propose_idea", "ask_owner"} {
		err := RenamedError(old)
		if err == nil || !strings.Contains(err.Error(), "agent_message_send") {
			t.Errorf("%s: %v, want a redirect to agent_message_send", old, err)
		}
	}
}
