package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// archiveTextBytes is text that mixes two-byte and three-byte characters, so a
// page boundary can fall inside one.
func archiveTextBytes(size int) []byte {
	return bytes.Repeat([]byte("é中a\n"), size/len("é中a\n"))
}

// archiveTextSkill stores a skill named name. Its SKILL.md is short, and each
// file in files is text of the given size. It returns the content of every
// file by path.
func archiveTextSkill(t *testing.T, g *Gateway, name string, files map[string]int) map[string][]byte {
	t.Helper()
	folder := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	skillMD := []byte("---\ndescription: A large skill.\n---\nbody\n")
	if err := os.WriteFile(filepath.Join(folder, "SKILL.md"), skillMD, 0o600); err != nil {
		t.Fatal(err)
	}
	content := map[string][]byte{"SKILL.md": skillMD}
	for path, size := range files {
		full := filepath.Join(folder, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		data := archiveTextBytes(size)
		if err := os.WriteFile(full, data, 0o600); err != nil {
			t.Fatal(err)
		}
		content[path] = data
	}
	hash, err := inventory.SkillHash(folder)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.skillArchivePut(skillArchiveContent{Name: name, Hash: hash, Bundle: archiveBundleAt(t, folder), Description: "A large skill.", SizeTokens: 1, ContextTokens: 1},
		skillArchiveSource{Kind: "agent-net"}, "cto", false); err != nil {
		t.Fatal(err)
	}
	return content
}

// A skill of about 10 MB of text reads over the link. The link reads frames of
// up to 4 MB, as the daemon does. Each file is under 64 KB, so each one's text
// used to go into the answer, and 170 of them closed the link. The read answers
// with the file list and SKILL.md, and the link stays up.
func TestSkillArchiveReadOfTenMegabyteSkillStaysOnTheLink(t *testing.T) {
	g, url := newTransferGateway(t, nil)
	peer := enrollTransferPeer(t, g, url, "archive-host")
	files := map[string]int{}
	for i := 0; i < 170; i++ {
		files[fmt.Sprintf("notes/part-%03d.md", i)] = 60 << 10
	}
	archiveTextSkill(t, g, "large", files)

	raw, err := transferCall(t, peer.ws, protocol.FrameSkillArchiveRead, protocol.SkillArchiveVersion{Name: "large", By: "cto"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(raw) > 1<<20 {
		t.Fatalf("read answer is %d bytes, want at most 1 MiB", len(raw))
	}
	var read protocol.SkillArchiveReadResult
	if err := json.Unmarshal(raw, &read); err != nil {
		t.Fatal(err)
	}
	if len(read.Files) != 171 || read.Total != 171 {
		t.Fatalf("read lists %d files of %d, want 171", len(read.Files), read.Total)
	}
	for _, f := range read.Files {
		if f.Path == "SKILL.md" && f.Text == "" {
			t.Fatal("SKILL.md has no text")
		}
		if f.Path != "SKILL.md" && f.Text != "" {
			t.Fatalf("%s carries text in the overview", f.Path)
		}
	}
	if _, err := transferCall(t, peer.ws, protocol.FrameSkillArchiveList, map[string]string{"by": "cto"}); err != nil {
		t.Fatalf("list after the read: %v", err)
	}
}

// A file of 1 MB reads in pages. Each page is one answer under 1 MiB, the pages
// join to the file, and the last page has no next offset.
func TestSkillArchiveReadPagesOneFileInOrder(t *testing.T) {
	g, url := newTransferGateway(t, nil)
	peer := enrollTransferPeer(t, g, url, "archive-host")
	want := archiveTextSkill(t, g, "large", map[string]int{"notes/big.md": 1<<20 + 3})["notes/big.md"]

	var got []byte
	var offset int64
	pages := 0
	for {
		pages++
		if pages > 100 {
			t.Fatal("paging never ends")
		}
		raw, err := transferCall(t, peer.ws, protocol.FrameSkillArchiveRead, protocol.SkillArchiveVersion{Name: "large", By: "cto", File: "notes/big.md", Offset: offset})
		if err != nil {
			t.Fatalf("page at %d: %v", offset, err)
		}
		if len(raw) > 1<<20 {
			t.Fatalf("page at %d is %d bytes, want at most 1 MiB", offset, len(raw))
		}
		var read protocol.SkillArchiveReadResult
		if err := json.Unmarshal(raw, &read); err != nil {
			t.Fatal(err)
		}
		if len(read.Files) != 1 || read.Files[0].Path != "notes/big.md" || read.Files[0].Binary {
			t.Fatalf("page at %d: %+v", offset, read.Files)
		}
		f := read.Files[0]
		if f.Size != int64(len(want)) || f.Offset != offset {
			t.Fatalf("page at %d says size %d and offset %d", offset, f.Size, f.Offset)
		}
		if len(f.Text) > 64<<10 {
			t.Fatalf("page at %d has %d bytes of text, want at most 64 KB", offset, len(f.Text))
		}
		got = append(got, f.Text...)
		if f.NextOffset == 0 {
			break
		}
		offset = f.NextOffset
	}
	if pages < 2 {
		t.Fatalf("1 MB came in %d page", pages)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("joined pages are %d bytes, want %d", len(got), len(want))
	}
}

// A file that the version does not hold is refused. The name is matched
// against the stored files, so a path that climbs out of the skill is refused too.
func TestSkillArchiveReadRefusesAFileOutsideTheVersion(t *testing.T) {
	g, url := newTransferGateway(t, nil)
	peer := enrollTransferPeer(t, g, url, "archive-host")
	archiveTextSkill(t, g, "large", map[string]int{"notes/a.md": 10})
	for _, file := range []string{"notes/missing.md", "../large/SKILL.md", "/etc/passwd"} {
		if _, err := transferCall(t, peer.ws, protocol.FrameSkillArchiveRead, protocol.SkillArchiveVersion{Name: "large", By: "cto", File: file}); err == nil {
			t.Fatalf("read of %q was answered", file)
		}
	}
}

// The file list comes in pages of 200 rows. SKILL.md is first, the rest follow
// in path order, and each page names the cursor of the next.
func TestSkillArchiveReadListsFilesInPages(t *testing.T) {
	g, url := newTransferGateway(t, nil)
	peer := enrollTransferPeer(t, g, url, "archive-host")
	files := map[string]int{}
	want := []string{"SKILL.md"}
	for i := 0; i < 450; i++ {
		path := fmt.Sprintf("refs/f%03d.md", i)
		files[path] = 3
		want = append(want, path)
	}
	archiveTextSkill(t, g, "large", files)

	var got []string
	after := ""
	for page := 0; ; page++ {
		if page > 10 {
			t.Fatal("list paging never ends")
		}
		raw, err := transferCall(t, peer.ws, protocol.FrameSkillArchiveRead, protocol.SkillArchiveVersion{Name: "large", By: "cto", After: after})
		if err != nil {
			t.Fatalf("list after %q: %v", after, err)
		}
		var read protocol.SkillArchiveReadResult
		if err := json.Unmarshal(raw, &read); err != nil {
			t.Fatal(err)
		}
		if read.Total != 451 {
			t.Fatalf("total is %d, want 451", read.Total)
		}
		if page == 0 && len(read.Files) != 200 {
			t.Fatalf("first page lists %d files, want 200", len(read.Files))
		}
		for _, f := range read.Files {
			got = append(got, f.Path)
		}
		if read.NextAfter == "" {
			break
		}
		after = read.NextAfter
	}
	if len(got) != len(want) {
		t.Fatalf("pages list %d files, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d is %q, want %q", i, got[i], want[i])
		}
	}
}

// An answer over the link's frame limit would close the link on the daemon's
// side. The caller gets an error that says why instead. An answer under the
// limit is sent as it is.
func TestAnswerOverTheLinkLimitBecomesAnError(t *testing.T) {
	big := strings.Repeat("x", protocol.LinkFrameLimit)
	reply, err := answerFrame("c1", big, nil)
	if err != nil {
		t.Fatal(err)
	}
	var h protocol.Header
	if err := json.Unmarshal(reply, &h); err != nil {
		t.Fatal(err)
	}
	if h.OK || !strings.Contains(h.Error, "too large for the link") {
		t.Fatalf("oversized answer: ok %v, error %q", h.OK, h.Error)
	}
	small, err := answerFrame("c2", map[string]int{"n": 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(small, &h); err != nil {
		t.Fatal(err)
	}
	if !h.OK || h.ID != "c2" {
		t.Fatalf("small answer: %+v", h)
	}
}
