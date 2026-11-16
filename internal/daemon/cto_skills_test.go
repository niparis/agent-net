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

func TestCTOSkillsSuggestSetEffectiveAndUndo(t *testing.T) {
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

	suggestion := callCTO[protocol.SkillSuggestion](t, n.d, ctx, "skills", `{"action":"suggest","level":"repo","scope":"grill-me"}`)
	if suggestion.ID == "" || len(suggestion.Skills) != 1 || suggestion.Skills[0].Name != "grill-me" || suggestion.Skills[0].Reason == "" {
		t.Fatalf("suggestion: %+v", suggestion)
	}
	set := callCTO[protocol.SkillSetResult](t, n.d, ctx, "skills", `{"action":"set","suggestion":"`+suggestion.ID+`"}`)
	if set.Change == "" || len(set.Added) != 1 || set.Added[0] != "grill-me" || len(set.Effective) != 1 || set.Effective[0].Level != "repo" {
		t.Fatalf("set: %+v", set)
	}
	log, err := g.gw.Store().DeskLog(10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range log {
		if strings.Contains(entry.Text, "set skills for repo grill-me") && len(entry.Options) == 2 && entry.Options[0] == protocol.OpSkillsUndo {
			found = true
		}
	}
	if !found {
		t.Fatalf("no undo line: %+v", log)
	}
	nodes := callCTO[struct {
		Nodes []protocol.EffectiveSkills `json:"nodes"`
	}](t, n.d, ctx, "skills", `{"action":"effective","level":"repo","scope":"grill-me"}`)
	if len(nodes.Nodes) != 1 || len(nodes.Nodes[0].Skills) != 1 || nodes.Nodes[0].Skills[0].Name != "grill-me" {
		t.Fatalf("effective: %+v", nodes.Nodes)
	}
	if _, err = n.d.skillsOwnerUndo(ctx, set.Change); err != nil {
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
