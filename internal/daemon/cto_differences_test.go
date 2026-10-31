package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTOToolsDifferencesFindsTwoCopiesAndHonoursAccepted(t *testing.T) {
	home := t.TempDir()
	for dir, text := range map[string]string{".claude/skills/demo": "one", ".agents/skills/demo": "two"} {
		p := filepath.Join(home, dir)
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "SKILL.md"), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, cfg: func(c *Config) { c.InventoryHome = home }})
	cli := cliClient(t, n)
	type reply struct {
		Differences []struct {
			Key      string `json:"key"`
			Kind     string `json:"kind"`
			Title    string `json:"title"`
			Fix      string `json:"fix"`
			Accepted bool   `json:"accepted"`
		} `json:"differences"`
		ToLookAt int `json:"to_look_at"`
	}
	read := func() (r reply, size int) {
		v, err := n.d.runCTOToolRaw(context.Background(), "tools_differences", json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(v)
		if err = json.Unmarshal(b, &r); err != nil {
			t.Fatal(err)
		}
		return r, len(b)
	}
	var r reply
	eventually(t, "two different copies are found", func() bool {
		r, _ = read()
		return len(r.Differences) > 0
	})
	d := r.Differences[0]
	if d.Kind != "two-copies" || !strings.Contains(d.Title, "demo") || d.Fix == "" || d.Accepted || r.ToLookAt != 1 {
		t.Fatalf("%+v", r)
	}
	if _, err := cli.call("tools_accept", protocol.ToolAccept{Key: d.Key, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	r, size := read()
	if !r.Differences[0].Accepted || r.ToLookAt != 0 {
		t.Fatalf("accepted difference still counts: %+v", r)
	}
	if size > 20*1024 {
		t.Fatalf("answer is %d bytes", size)
	}
}

// A large network gives a bounded answer that says how many differences it
// left out, and the tool reads every page of items to find them.
func TestCTOToolsDifferencesCapsTheAnswerAndReadsEveryPage(t *testing.T) {
	home := t.TempDir()
	write := func(dir, text string) {
		p := filepath.Join(home, dir)
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "SKILL.md"), []byte(text+strings.Repeat(" padding", 200)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	const pairs = maxCTODifferences + 10
	for i := 0; i < pairs; i++ {
		name := fmt.Sprintf("pair-%03d", i)
		write(".claude/skills/"+name, "one "+name)
		write(".agents/skills/"+name, "two "+name)
	}
	g := startGatewayWithToolsPageSize(t, t.TempDir(), 16)
	n := startNode(t, g, nodeOpts{detect: fakeInventory, cfg: func(c *Config) { c.InventoryHome = home }})
	cli := cliClient(t, n)
	var r struct {
		Differences []struct{ Kind string } `json:"differences"`
		ToLookAt    int                     `json:"to_look_at"`
		NotListed   int                     `json:"not_listed"`
		Next        int                     `json:"next"`
	}
	eventually(t, "the pairs are found", func() bool {
		v, err := n.d.runCTOToolRaw(context.Background(), "tools_differences", json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(v)
		t.Logf("default difference page: %d bytes", len(b))
		r.Differences, r.ToLookAt, r.NotListed = nil, 0, 0
		if err = json.Unmarshal(b, &r); err != nil {
			t.Fatal(err)
		}
		return r.ToLookAt >= pairs
	})
	if len(r.Differences) != 20 || r.Next != 20 || r.NotListed != r.ToLookAt-20 {
		t.Fatalf("listed %d, not listed %d, to look at %d", len(r.Differences), r.NotListed, r.ToLookAt)
	}
	v, err := n.d.runCTOToolRaw(context.Background(), "tools_differences", json.RawMessage(`{"offset":20,"limit":60}`))
	if err != nil {
		t.Fatal(err)
	}
	all, err := n.d.runCTOToolRaw(context.Background(), "tools_differences", json.RawMessage(`{"limit":60}`))
	if err != nil {
		t.Fatal(err)
	}
	allJSON, _ := json.Marshal(all)
	t.Logf("60 difference page: %d bytes", len(allJSON))
	var rest struct {
		Differences []json.RawMessage `json:"differences"`
		Next        int               `json:"next"`
	}
	b, _ := json.Marshal(v)
	if err := json.Unmarshal(b, &rest); err != nil || len(rest.Differences) != pairs-20 || rest.Next != 0 {
		t.Fatalf("second page: %+v %v", rest, err)
	}
	var page protocol.ToolsPage
	if err := json.Unmarshal(cli.mustCall("tools_items", protocol.ToolsFilter{}), &page); err != nil {
		t.Fatal(err)
	}
	if page.Next == 0 {
		t.Fatalf("the fixture fits one page of %d items; the paging is not exercised", len(page.Items))
	}
}
