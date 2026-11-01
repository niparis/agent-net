package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTOSettingsTitle(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour})
	cli := cliClient(t, n)

	read := func() protocol.CTOSettings {
		t.Helper()
		var out protocol.CTOSettings
		if err := json.Unmarshal(cli.mustCall("cto_settings", nil), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := read(); got.Title != protocol.DefaultCTOTitle {
		t.Fatalf("fresh title = %q; want %q", got.Title, protocol.DefaultCTOTitle)
	}
	title := "  Atlas  "
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Title: &title})
	if got := read(); got.Title != "Atlas" {
		t.Fatalf("saved title = %q; want Atlas", got.Title)
	}
	// A title change is not a harness change, so the saved models stay.
	model := "fake/m1"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Model: &model})
	if got := read(); got.Title != "Atlas" || got.Model != "fake/m1" {
		t.Fatalf("model change lost the title: %+v", got)
	}
	bad := strings.Repeat("a", 25)
	if _, err := cli.call("cto_settings", protocol.CTOSettingsRequest{Title: &bad}); err == nil {
		t.Fatal("a long title was accepted")
	}
	bad = "Atlas [agent-net"
	if _, err := cli.call("cto_settings", protocol.CTOSettingsRequest{Title: &bad}); err == nil {
		t.Fatal("a note marker was accepted")
	}
	if got := read(); got.Title != "Atlas" {
		t.Fatalf("a refused title changed the saved value: %+v", got)
	}
	// The fixed word agents see is not the owner's title.
	if !strings.Contains(protocol.CTONote, "the CTO") || strings.Contains(protocol.CTONote, "Atlas") {
		t.Fatalf("agent note follows the owner title: %q", protocol.CTONote)
	}
}
