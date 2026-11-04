package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

// The CTO channel refuses every tool name from before the redesign with the
// one-line error that names the new tool. The stored as-owner "message" call
// stays an internal proposal tool and cannot be called through the channel.
func TestCTOChannelRefusesOldNames(t *testing.T) {
	d := &Daemon{}
	for _, name := range []string{"overview", "sessions", "session", "proposals", "feed", "messages", "graph", "machines", "projects", "workers", "tools", "settings", "message", "ask_owner", "learn", "ui"} {
		want := toolcatalog.RenamedError(name)
		if want == nil {
			t.Fatalf("%s is not an old name", name)
		}
		_, err := d.runCTOToolRaw(context.Background(), name, json.RawMessage(`{"action":"send","as_owner":true}`))
		if err == nil || err.Error() != want.Error() {
			t.Errorf("%s: %v, want %q", name, err, want)
		}
		if _, ok := ctoReadSources[name]; ok {
			t.Errorf("%s still has a read source", name)
		}
		if class, ok := ctoToolClasses[name]; ok && name != "message" {
			t.Errorf("%s is still a %s tool", name, class)
		}
	}
}

// agent_graph_read with view guide is the CTO's charter guide.
func TestCTOGraphReadGuideView(t *testing.T) {
	value, err := (&Daemon{}).readCTOTool(context.Background(), "agent_graph_read", json.RawMessage(`{"view":"guide"}`))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := value.(map[string]any)
	note, _ := out["note"].(string)
	if len(note) < len(protocol.CharterGuideText()) || note[:len(protocol.CharterGuideText())] != protocol.CharterGuideText() {
		t.Fatalf("guide: %v", value)
	}
	if nodes, ok := out["nodes"].([]any); !ok || len(nodes) != 0 {
		t.Fatalf("nodes: %v", out["nodes"])
	}
	if _, err := (&Daemon{}).readCTOTool(context.Background(), "agent_graph_read", json.RawMessage(`{"view":"other"}`)); err == nil {
		t.Fatal("unknown view accepted")
	}
}
