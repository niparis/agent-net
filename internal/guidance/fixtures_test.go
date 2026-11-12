package guidance

import (
	"encoding/json"
	"flag"
	"github.com/niparis/agent-net/internal/settings"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite guidance fixtures")

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
		if *update {
			if err := os.WriteFile("testdata/"+name+".golden", []byte(got), 0644); err != nil {
				t.Fatal(err)
			}
		}
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

// Owner questions are in the chat and on the route (decision 0150): an agent
// also sends them to the chief with agent_owner_ask. No guidance file that
// agent-net writes, here or in internal/agentfiles, tells an agent to ask its
// owner without the route.
func TestGoldensHaveNoAskYourOwnerLine(t *testing.T) {
	paths, err := filepath.Glob("testdata/*.golden")
	if err != nil {
		t.Fatal(err)
	}
	more, err := filepath.Glob("../agentfiles/testdata/*.golden")
	if err != nil {
		t.Fatal(err)
	}
	paths = append(paths, more...)
	if len(paths) < 2 {
		t.Fatalf("found %d golden files", len(paths))
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			low := strings.ToLower(line)
			if strings.Contains(low, "ask your owner") || strings.Contains(low, "wait for my decision") {
				t.Errorf("%s:%d tells an agent to ask its owner: %s", path, i+1, line)
			}
		}
	}
}
