package protocol

import (
	"strings"
	"testing"
)

func TestWorkModeValues(t *testing.T) {
	for _, v := range []string{"", WorkModeWorkersCode, WorkModeOnRequest} {
		if _, err := CleanCharterValue("work_mode", v); err != nil {
			t.Fatalf("rejected %q: %v", v, err)
		}
	}
	for _, v := range []string{"always", "workers", "Workers_Code "} {
		if _, err := CleanCharterValue("work_mode", v); err == nil {
			t.Fatalf("accepted %q", v)
		}
	}
}

func TestWorkModeAllowedAtEveryLevel(t *testing.T) {
	for _, level := range []string{"global", "team", "repo", "agent"} {
		if !CharterAttributeAtLevel(level, "work_mode") {
			t.Fatalf("work_mode not allowed at %s", level)
		}
	}
	if CharterAttributeAtLevel("network", "work_mode") {
		t.Fatal("work_mode allowed at an unknown level")
	}
}

func TestNodeContextPrintsWorkModeOnlyWhenNotDefault(t *testing.T) {
	g := contextGraph()
	if got := NodeContext(g, "cogforge"); strings.Contains(got, "Work mode") {
		t.Fatalf("default mode printed a line:\n%s", got)
	}
	g.Nodes[0].Charter.WorkMode = WorkModeOnRequest
	if got := NodeContext(g, "cogforge"); strings.Contains(got, "Work mode") {
		t.Fatalf("on_request printed a line:\n%s", got)
	}
	g.Nodes[0].Charter.WorkMode = WorkModeWorkersCode
	sources := CharterSources{"work_mode": {{Level: "team", Scope: "forge", Value: WorkModeWorkersCode}}}
	g.Nodes[0].Charter.Sources = &sources
	got := NodeContext(g, "cogforge")
	want := "Work mode: workers write all code (team forge). Design and review; start agent-net workers for code; no owner ask needed."
	if !strings.Contains(got, want) {
		t.Fatalf("missing %q in:\n%s", want, got)
	}
	if strings.Contains(got, "work_mode:") {
		t.Fatalf("raw attribute line printed:\n%s", got)
	}
}

func TestNodeContextWorkModeAloneIsACharter(t *testing.T) {
	g := Graph{Nodes: []Node{{Name: "solo", Kind: "component", Charter: Charter{WorkMode: WorkModeWorkersCode}}}}
	got := NodeContext(g, "solo")
	if !strings.Contains(got, "Work mode: workers write all code.") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestWorkModeSourceText(t *testing.T) {
	cases := map[string]CharterSource{
		"global":      {Level: "global"},
		"team forge":  {Level: "team", Scope: "forge"},
		"repo shop":   {Level: "repo", Scope: "shop"},
		"agent alice": {Level: "agent", Scope: "alice"},
	}
	for want, src := range cases {
		if got := WorkModeSourceText(src); got != want {
			t.Errorf("%+v: got %q want %q", src, got, want)
		}
	}
}
