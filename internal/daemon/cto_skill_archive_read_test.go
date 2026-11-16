package daemon

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The CTO reads a large skill in pages. The overview lists the files and gives
// SKILL.md, and a read with file and offset gives one page of that file. The
// arguments reach the gateway, and the next_offset of one page starts the next.
func TestCTOSkillArchiveReadsAFileInPages(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	ctx := context.Background()
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("é中a\n", 20000)
	url := skillRepoWithFiles(t, map[string]string{
		"SKILL.md":      "---\ndescription: Ask hard questions.\n---\nbody\n",
		"notes/long.md": long,
	})
	callCTOSkillArchive[protocol.SkillArchiveMultiResult](t, n.d, ctx, `{"action":"add_git","url":"`+url+`"}`)

	overview := callCTOSkillArchive[protocol.SkillArchiveReadResult](t, n.d, ctx, `{"action":"read","name":"grill-me"}`)
	if overview.Total != 2 || len(overview.Files) != 2 || overview.Files[0].Path != "SKILL.md" || overview.Files[0].Text == "" {
		t.Fatalf("overview: %+v", overview)
	}

	first := callCTOSkillArchive[protocol.SkillArchiveReadResult](t, n.d, ctx, `{"action":"read","name":"grill-me","file":"notes/long.md","offset":0}`)
	if len(first.Files) != 1 || first.Files[0].Size != int64(len(long)) || first.Files[0].NextOffset == 0 {
		t.Fatalf("first page: %+v", first.Files)
	}
	next := first.Files[0].NextOffset
	second := callCTOSkillArchive[protocol.SkillArchiveReadResult](t, n.d, ctx,
		`{"action":"read","name":"grill-me","file":"notes/long.md","offset":`+strconv.FormatInt(next, 10)+`}`)
	if len(second.Files) != 1 || second.Files[0].Offset != next || second.Files[0].Text == "" {
		t.Fatalf("second page: %+v", second.Files)
	}
	if !strings.HasPrefix(long[next:], second.Files[0].Text) {
		t.Fatal("second page is not the text at its offset")
	}
}
