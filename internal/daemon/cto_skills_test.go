package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func callCTO[T any](t *testing.T, d *Daemon, ctx context.Context, tool, args string) T {
	t.Helper()
	result, err := d.runCTOTool(ctx, tool, json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s %s: %v", tool, args, err)
	}
	b, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err = json.Unmarshal(b, &out); err != nil {
		t.Fatalf("%s answer %s: %v", tool, b, err)
	}
	return out
}

func TestCTOSkillsSetEffectiveAndUndo(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	ctx := context.Background()
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	url := skillRepo(t)
	callCTO[protocol.SkillArchiveMultiResult](t, n.d, ctx, "skill_archive", `{"action":"add_git","url":"`+url+`"}`)
	if _, err := n.d.call(ctx, "project.add", protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Path: "/work/grill-me", Name: "grill-me"}}}); err != nil {
		t.Fatal(err)
	}

	set := callCTO[protocol.SkillSetResult](t, n.d, ctx, "skills", `{"action":"set","level":"repo","scope":"grill-me","add":["grill-me"]}`)
	if set.Change == "" || len(set.Added) != 1 || set.Added[0] != "grill-me" || len(set.Effective) != 1 || set.Effective[0].Level != "repo" {
		t.Fatalf("set: %+v", set)
	}
	nodes := callCTO[struct {
		Nodes []protocol.EffectiveSkills `json:"nodes"`
	}](t, n.d, ctx, "skills", `{"action":"effective","level":"repo","scope":"grill-me"}`)
	if len(nodes.Nodes) != 1 || len(nodes.Nodes[0].Skills) != 1 || nodes.Nodes[0].Skills[0].Name != "grill-me" {
		t.Fatalf("effective: %+v", nodes.Nodes)
	}
	if _, err := n.d.skillsOwnerUndo(ctx, set.Change); err != nil {
		t.Fatal(err)
	}
	nodes = callCTO[struct {
		Nodes []protocol.EffectiveSkills `json:"nodes"`
	}](t, n.d, ctx, "skills", `{"action":"effective","level":"repo","scope":"grill-me"}`)
	if len(nodes.Nodes[0].Skills) != 0 {
		t.Fatalf("after undo: %+v", nodes.Nodes[0].Skills)
	}
}

func TestCTOSkillsSetRefusesSameNameAddAndRemove(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	ctx := context.Background()
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	url := skillRepo(t)
	callCTO[protocol.SkillArchiveMultiResult](t, n.d, ctx, "skill_archive", `{"action":"add_git","url":"`+url+`"}`)
	if _, err := n.d.call(ctx, "project.add", protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Path: "/work/x", Name: "x"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := n.d.runCTOTool(ctx, "skills", json.RawMessage(`{"action":"set","level":"repo","scope":"x","add":["grill-me"],"remove":["grill-me"]}`)); err == nil {
		t.Fatal("the same name on both lists was accepted")
	}
}

// The suggest action is gone: the CTO picks skills from the archive list and
// sets them by name.
func TestCTOSkillsRefusesSuggestAction(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	ctx := context.Background()
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := n.d.runCTOTool(ctx, "skills", json.RawMessage(`{"action":"suggest","level":"repo","scope":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "suggest") {
		t.Fatalf("suggest action: %v", err)
	}
}
