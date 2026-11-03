package guidance

import (
	"encoding/json"
	"github.com/niparis/agent-net/internal/settings"
	"os"
	"strings"
	"testing"
)

func TestOriginalFixtures(t *testing.T) {
	cases := map[string]string{}
	for _, v := range []string{Claude, Other, Combined} {
		cases["block_"+v] = Block(v)
	}
	cases["apply_new"], _ = Apply("Personal instructions.\n", Other)
	cases["apply_existing"], _ = Apply("Before\n\n"+Block(Claude)+"\nAfter\n", Other)
	cases["remove"], _ = Remove("Before\n\n" + Block(Claude) + "\nAfter\n")
	cases["legacy"], _ = Apply("Before\n\n## agent-net: working with other agents\nold\n\n## Next\nAfter\n", Other)
	for name, got := range cases {
		want, err := os.ReadFile("testdata/" + name + ".golden")
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Errorf("%s differs from original", name)
		}
	}
}

func TestNetworkGuidance(t *testing.T) {
	s := settings.Snapshot{Values: []settings.Value{{Key: "guidance.intro_text", Level: "network", Scope: "network", Value: json.RawMessage(`"Custom introduction."`)}}}
	got := BlockWithSnapshot(Other, s)
	if !strings.Contains(got, "Custom introduction.") || strings.Contains(got, "Other coding agents run") {
		t.Fatal("network text not applied")
	}
}
