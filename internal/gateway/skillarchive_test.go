package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

func archiveCall[T any](t *testing.T, g *Gateway, op string, in any) T {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	data, err := g.handle(context.Background(), &nodeConn{id: "archive-test"}, op, raw)
	if err != nil {
		t.Fatalf("%s: %v", op, err)
	}
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err = json.Unmarshal(b, &out); err != nil {
		t.Fatalf("%s answer: %v", op, err)
	}
	return out
}

func archiveList(t *testing.T, g *Gateway) []protocol.ArchivedSkill {
	t.Helper()
	return archiveCall[struct {
		Skills []protocol.ArchivedSkill `json:"skills"`
	}](t, g, protocol.FrameSkillArchiveList, map[string]string{"by": "owner"}).Skills
}

// archiveSkillFolder writes one skill folder and returns its path and content
// hash, the way an inventory scan sees it.
func archiveSkillFolder(t *testing.T, dir, body string) (string, string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := inventory.SkillHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, hash
}

func archiveGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid",
		"LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// archiveRepo makes a local repository of skills. The URL is a file URL, so
// the test never uses the network.
func archiveRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	archiveGit(t, dir, "init", "-q", "-b", "main")
	archiveSkillFolder(t, filepath.Join(dir, "grill-me"), "---\ndescription: Ask hard questions.\n---\nbody\n")
	if err := os.MkdirAll(filepath.Join(dir, "not-a-skill"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "not-a-skill", "README.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	archiveGit(t, dir, "add", "-A")
	archiveGit(t, dir, "commit", "-q", "-m", "skills")
	return "file://" + dir
}

func TestSkillArchiveAddFromFolder(t *testing.T) {
	g, n := archiveFixture(t)
	dir := t.TempDir()
	folder, hash := archiveSkillFolder(t, filepath.Join(dir, "demo"), "---\ndescription: A demo skill.\n---\nhello\n")
	addFile(t, folder, "extra.txt", "x")
	hash, err := inventory.SkillHash(folder)
	if err != nil {
		t.Fatal(err)
	}
	archiveFakeNodeSources(t, g, n, map[string][]byte{folder: archiveBundle(t, folder)}, []protocol.SkillSource{
		{Name: "demo", Hash: hash, Location: folder, Origin: "user", State: protocol.InventoryOK},
	})

	got := archiveCall[protocol.SkillArchiveAddResult](t, g, protocol.FrameSkillArchiveAdd, protocol.SkillArchiveAdd{
		Location: protocol.ToolImportLocation{NodeID: n, Location: folder}, By: "cto",
	})
	if got.State != "added" || got.Name != "demo" || got.Version != 1 || got.Change == "" {
		t.Fatalf("add: %+v", got)
	}
	if got.Description != "A demo skill." {
		t.Errorf("description %q", got.Description)
	}
	if got.SizeTokens <= 0 || got.ContextTokens <= 0 || got.ContextTokens >= got.SizeTokens {
		t.Errorf("tokens: context %d, size %d", got.ContextTokens, got.SizeTokens)
	}
	rows := archiveList(t, g)
	if len(rows) != 1 || rows[0].Name != "demo" || rows[0].SourceKind != "machine" || rows[0].SourceNode != n || rows[0].SourcePath != folder {
		t.Fatalf("list: %+v", rows)
	}
}

func addFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSkillArchiveAddGitTakesEachSkillFolderOnce(t *testing.T) {
	g := archiveGateway(t)
	url := archiveRepo(t)
	in := protocol.SkillArchiveAddGit{URL: url, By: "owner"}
	first := archiveCall[protocol.SkillArchiveMultiResult](t, g, protocol.FrameSkillArchiveAddGit, in)
	if len(first.Skills) != 1 || first.Skills[0].Name != "grill-me" || first.Skills[0].State != "added" {
		t.Fatalf("first add: %+v", first)
	}
	second := archiveCall[protocol.SkillArchiveMultiResult](t, g, protocol.FrameSkillArchiveAddGit, in)
	if len(second.Skills) != 1 || second.Skills[0].State != "unchanged" || second.Skills[0].Version != 1 {
		t.Fatalf("same content twice must be one version: %+v", second)
	}
	rows := archiveList(t, g)
	if len(rows) != 1 || rows[0].Versions != 1 || rows[0].SourceKind != "git" || !strings.HasPrefix(rows[0].SourceURL, "file://") || len(rows[0].SourceCommit) != 40 {
		t.Fatalf("list: %+v", rows)
	}
}

func TestSkillArchiveNewVersionIncrements(t *testing.T) {
	g := archiveGateway(t)
	url := archiveRepo(t)
	archiveCall[protocol.SkillArchiveMultiResult](t, g, protocol.FrameSkillArchiveAddGit, protocol.SkillArchiveAddGit{URL: url, By: "owner"})
	root := strings.TrimPrefix(url, "file://")
	if err := os.WriteFile(filepath.Join(root, "grill-me", "SKILL.md"), []byte("---\ndescription: Ask harder questions.\n---\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	archiveGit(t, root, "add", "-A")
	archiveGit(t, root, "commit", "-q", "-m", "edit")
	second := archiveCall[protocol.SkillArchiveMultiResult](t, g, protocol.FrameSkillArchiveAddGit, protocol.SkillArchiveAddGit{URL: url, By: "owner"})
	if len(second.Skills) != 1 || second.Skills[0].State != "added" || second.Skills[0].Version != 2 {
		t.Fatalf("new version: %+v", second)
	}
	rows := archiveList(t, g)
	if len(rows) != 1 || rows[0].Version != 2 || rows[0].Versions != 2 || rows[0].Description != "Ask harder questions." {
		t.Fatalf("list: %+v", rows)
	}
}

func TestSkillArchiveRemoveAndUndo(t *testing.T) {
	g := archiveGateway(t)
	url := archiveRepo(t)
	added := archiveCall[protocol.SkillArchiveMultiResult](t, g, protocol.FrameSkillArchiveAddGit, protocol.SkillArchiveAddGit{URL: url, By: "owner"})
	change := added.Skills[0].Change
	removed := archiveCall[protocol.SkillArchiveAddResult](t, g, protocol.FrameSkillArchiveRemove, protocol.SkillArchiveRemove{Name: "grill-me", By: "owner"})
	if removed.State != "removed" || removed.Change == "" {
		t.Fatalf("remove: %+v", removed)
	}
	if rows := archiveList(t, g); len(rows) != 0 {
		t.Fatalf("list after remove: %+v", rows)
	}
	restored := archiveCall[protocol.SkillArchiveAddResult](t, g, protocol.FrameSkillArchiveUndo, protocol.SkillArchiveUndo{Change: removed.Change, By: "owner"})
	if restored.State != "restored" || restored.Name != "grill-me" {
		t.Fatalf("undo remove: %+v", restored)
	}
	if rows := archiveList(t, g); len(rows) != 1 {
		t.Fatalf("list after undo remove: %+v", rows)
	}
	// Undo of the add takes that version away again.
	gone := archiveCall[protocol.SkillArchiveAddResult](t, g, protocol.FrameSkillArchiveUndo, protocol.SkillArchiveUndo{Change: change, By: "owner"})
	if gone.State != "removed" {
		t.Fatalf("undo add: %+v", gone)
	}
	if rows := archiveList(t, g); len(rows) != 0 {
		t.Fatalf("list after undo add: %+v", rows)
	}
	if _, err := g.handle(context.Background(), &nodeConn{id: "archive-test"}, protocol.FrameSkillArchiveUndo, []byte(`{"change":"`+change+`","by":"owner"}`)); err == nil {
		t.Fatal("undo was accepted twice")
	}
}

func TestSkillArchiveReadOneVersionFiles(t *testing.T) {
	g := archiveGateway(t)
	url := archiveRepo(t)
	archiveCall[protocol.SkillArchiveMultiResult](t, g, protocol.FrameSkillArchiveAddGit, protocol.SkillArchiveAddGit{URL: url, By: "owner"})
	got := archiveCall[protocol.SkillArchiveReadResult](t, g, protocol.FrameSkillArchiveRead, protocol.SkillArchiveVersion{Name: "grill-me", By: "owner"})
	if got.Version != 1 || len(got.Files) != 1 || got.Files[0].Path != "SKILL.md" || !strings.Contains(got.Files[0].Text, "Ask hard questions") {
		t.Fatalf("read: %+v", got)
	}
}

func TestSkillArchiveIntakeFromMachines(t *testing.T) {
	g, n := archiveFixture(t)
	dir := t.TempDir()
	folderA, hashA := archiveSkillFolder(t, filepath.Join(dir, "a"), "---\ndescription: First copy.\n---\nbody\n")
	folderB, hashB := archiveSkillFolder(t, filepath.Join(dir, "b"), "---\ndescription: Second copy.\n---\nother\n")
	archiveFakeNodeSources(t, g, n, map[string][]byte{folderA: archiveBundle(t, folderA), folderB: archiveBundle(t, folderB)}, []protocol.SkillSource{
		{Name: "demo", Hash: hashA, Location: folderA, Origin: "user", State: protocol.InventoryOK},
		{Name: "demo", Hash: hashB, Location: folderB, Origin: "user", State: protocol.InventoryOK},
	})
	got := archiveCall[protocol.SkillArchiveMultiResult](t, g, protocol.FrameSkillArchiveIntake, protocol.SkillArchiveIntake{By: "cto"})
	if len(got.Skills) != 2 || got.Skills[0].Name != "demo" || got.Skills[1].Version != 2 {
		t.Fatalf("intake: %+v", got)
	}
	if len(got.Shared) != 1 || got.Shared[0] != "demo" {
		t.Fatalf("shared names: %+v", got.Shared)
	}
	if rows := archiveList(t, g); len(rows) != 1 || rows[0].Versions != 2 {
		t.Fatalf("list: %+v", rows)
	}
}

func archiveBundle(t *testing.T, dir string) []byte {
	t.Helper()
	bundle, err := inventory.PackSkillLimit(dir, inventory.ArchiveLimit)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

// archiveFakeNode answers node.skill_bundle with the bundle of the asked folder,
// so one test can serve several distinct skill contents.
func archiveFakeNode(t *testing.T, g *Gateway, n string, bundles map[string][]byte) {
	t.Helper()
	archiveFakeNodeSources(t, g, n, bundles, nil)
}

// archiveFakeNodeSources is archiveFakeNode for a daemon that also answers
// node.skill_sources. A nil list is a daemon that does not know the frame.
func archiveFakeNodeSources(t *testing.T, g *Gateway, n string, bundles map[string][]byte, sources []protocol.SkillSource) {
	t.Helper()
	var answer *protocol.SkillSourcesResult
	if sources != nil {
		answer = &protocol.SkillSourcesResult{Sources: sources, UserRead: true}
	}
	archiveFakeNodeAnswer(t, g, n, bundles, answer)
}

// archiveFakeNodeAnswer is archiveFakeNodeSources with the whole answer to
// node.skill_sources, so a test can say which projects the daemon read. A nil
// answer is a daemon that does not know the frame.
func archiveFakeNodeAnswer(t *testing.T, g *Gateway, n string, bundles map[string][]byte, answer *protocol.SkillSourcesResult) *archiveFrames {
	t.Helper()
	frames := &archiveFrames{}
	accepted := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, e := websocket.Accept(w, r, nil)
		if e == nil {
			accepted <- ws
		}
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	client, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ws := <-accepted
	nc := &nodeConn{id: n, ws: ws, dead: make(chan struct{}), cancel: cancel}
	setTestConn(t, g, n, nc)
	done := make(chan struct{})
	snapshots := map[string][]byte{}
	go func() {
		defer close(done)
		for {
			_, raw, e := client.Read(ctx)
			if e != nil {
				return
			}
			var h protocol.Header
			_ = json.Unmarshal(raw, &h)
			var data any = map[string]bool{"ok": true}
			var answerErr error
			if h.Type == protocol.FrameNodeSkillSources {
				if answer == nil {
					answerErr = errors.New("unknown frame")
				} else {
					data = *answer
				}
			}
			if h.Type == "node.skill_bundle" {
				var q protocol.SkillBundleRequest
				_ = json.Unmarshal(raw, &q)
				data, answerErr = fakeBundleAnswer(q, bundles, snapshots)
			}
			if h.ID != "" {
				b, _ := protocol.EncodeResult(h.ID, data, answerErr)
				frames.note(len(b))
				var reply protocol.Header
				_ = json.Unmarshal(b, &reply)
				nc.answer(reply)
			}
		}
	}()
	t.Cleanup(func() {
		removeTestConn(g, n)
		cancel()
		client.CloseNow()
		ws.CloseNow()
		<-done
	})
	return frames
}

// archiveFrames records the size of every answer a fake node sent. A frame of
// the link must stay under the 4 MB read limit.
type archiveFrames struct {
	mu  sync.Mutex
	max int
}

func (f *archiveFrames) note(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.max = max(f.max, n)
}

func (f *archiveFrames) largest() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.max
}

// fakeBundleAnswer answers node.skill_bundle the way the daemon does
// (decision 0169): a bundle that fits one part comes whole, and a larger
// archive bundle comes as part 0 and a snapshot for the others. Past the archive
// limit it refuses with the daemon's text.
func fakeBundleAnswer(q protocol.SkillBundleRequest, bundles map[string][]byte, snapshots map[string][]byte) (any, error) {
	if q.Snapshot != "" {
		bundle, ok := snapshots[q.Snapshot]
		start := q.Part * protocol.TransferPartSize
		if !ok || q.Part < 0 || start >= len(bundle) {
			return nil, errors.New("no such part")
		}
		return protocol.SkillBundle{Data: bundle[start:min(start+protocol.TransferPartSize, len(bundle))]}, nil
	}
	bundle, ok := bundles[q.Location]
	if !ok {
		// The daemon packs the folder as it is on disk now, whatever hash the
		// gateway asked for (decision 0169).
		packed, err := inventory.PackSkillLimit(q.Location, inventory.ArchiveLimit)
		if err != nil {
			return nil, errors.New("cannot safely read skill bundle")
		}
		bundle, ok = packed, true
	}
	if !ok {
		return nil, errors.New("no such bundle")
	}
	if int64(len(bundle)) > inventory.ArchiveLimit.Bytes {
		return nil, errors.New("the skill is larger than the 64 MB archive limit")
	}
	if len(bundle) <= protocol.TransferPartSize {
		return protocol.SkillBundle{Data: bundle}, nil
	}
	id := "snap-" + q.Location
	snapshots[id] = bundle
	sum := sha256.Sum256(bundle)
	return protocol.SkillBundle{
		Data:     bundle[:protocol.TransferPartSize],
		Snapshot: id,
		Parts:    protocol.PartCount(int64(len(bundle))),
		Size:     int64(len(bundle)),
		SHA256:   hex.EncodeToString(sum[:]),
	}, nil
}

// archiveGateway is a gateway with no machines and no projects.
func archiveGateway(t *testing.T) *Gateway {
	t.Helper()
	g, err := New(Options{DataDir: seededDataDir(t), TempFolders: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	return g
}
