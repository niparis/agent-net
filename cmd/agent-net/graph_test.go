package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type graphFake struct {
	method string
	params any
}

func (f *graphFake) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	f.method = method
	f.params = params
	if method == "graph_get" {
		return json.RawMessage(`{"nodes":[{"name":"p","kind":"component","charter":{"purpose":"test"}}],"edges":[]}`), nil
	}
	if method == "node_settings_get" || method == "node_settings_set" {
		return json.RawMessage(`{"node":"p","overrides":{},"defaults":{"level":"mature","tests_before_merge":"suite-new-tests","merge_authority":"delegated","worker_sandbox":"sandboxed","judge_consumers":"strict","deploy_rule":"ask"},"effective":{"level":"mature","tests_before_merge":"suite-new-tests","merge_authority":"delegated","worker_sandbox":"sandboxed","judge_consumers":"strict","deploy_rule":"ask"}}`), nil
	}
	return json.RawMessage(`[]`), nil
}
func TestGraphCommands(t *testing.T) {
	cases := []struct {
		cmd    string
		args   []string
		method string
	}{
		{"charter", []string{"show", "p"}, "graph_get"}, {"charter", []string{"set", "p", "purpose", "hello", "world"}, "charter_set"}, {"charter", []string{"set", "p", "purpose"}, "charter_set"}, {"charter", []string{"history", "p"}, "charter_history"}, {"graph", nil, "graph_get"}, {"graph", []string{"undo"}, "graph_undo"}, {"graph", []string{"undo", "12"}, "graph_undo"}, {"node", []string{"add-architect", "a"}, "node_add"}, {"node", []string{"remove", "a"}, "node_remove"}, {"node", []string{"settings", "p"}, "node_settings_get"}, {"node", []string{"settings", "p", "merge_authority", "ask"}, "node_settings_set"}, {"edge", []string{"set", "a", "depends", "p"}, "edge_set"}, {"edge", []string{"remove", "a", "depends", "p"}, "edge_remove"},
	}
	for _, tc := range cases {
		t.Run(tc.cmd+"/"+tc.method, func(t *testing.T) {
			f := &graphFake{}
			if err := runGraphCommand(context.Background(), f, &bytes.Buffer{}, tc.cmd, tc.args); err != nil || f.method != tc.method {
				t.Fatalf("method %s err %v", f.method, err)
			}
		})
	}
}

func TestCharterSetWarnsAboutDetailedValueAndStillSucceeds(t *testing.T) {
	f := &graphFake{}
	var out bytes.Buffer
	err := runGraphCommand(context.Background(), f, &out, "charter", []string{"set", "p", "needs", "Read", "/home/project/private/config"})
	if err != nil {
		t.Fatalf("charter set failed: %v", err)
	}
	if !strings.Contains(out.String(), "too long/too detailed for a charter: keep it to names only; put detail in AGENTS.md") {
		t.Fatalf("charter set did not print the warning: %q", out.String())
	}
}
