package main

import (
	"bytes"
	"context"
	"encoding/json"
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
	return json.RawMessage(`[]`), nil
}
func TestGraphCommands(t *testing.T) {
	cases := []struct {
		cmd    string
		args   []string
		method string
	}{
		{"charter", []string{"show", "p"}, "graph_get"}, {"charter", []string{"set", "p", "purpose", "hello", "world"}, "charter_set"}, {"charter", []string{"set", "p", "purpose"}, "charter_set"}, {"charter", []string{"history", "p"}, "charter_history"}, {"graph", nil, "graph_get"}, {"graph", []string{"undo"}, "graph_undo"}, {"graph", []string{"undo", "12"}, "graph_undo"}, {"node", []string{"add-architect", "a"}, "node_add"}, {"node", []string{"remove", "a"}, "node_remove"}, {"edge", []string{"set", "a", "depends", "p"}, "edge_set"}, {"edge", []string{"remove", "a", "depends", "p"}, "edge_remove"},
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
