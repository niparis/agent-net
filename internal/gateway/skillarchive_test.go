package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	g, n, _ := toggleFixture(t)
	dir := t.TempDir()
	folder, hash := archiveSkillFolder(t, filepath.Join(dir, "demo"), "---\ndescription: A demo skill.\n---\nhello\n")
	addFile(t, folder, "extra.txt", "x")
	hash, err := inventory.SkillHash(folder)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := inventory.PackSkill(folder)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.store.SetInventory(n, protocol.InventoryReport{State: "ok", ScannedAt: time.Now(), Items: []protocol.InventoryItem{{Kind: "skill", Name: "demo", Hash: hash, State: "ok", Scope: "user", Location: "/skills/demo"}}}); err != nil {
		t.Fatal(err)
	}
	toggleFakeNode(t, g, n, bundle, nil)

	got := archiveCall[protocol.SkillArchiveAddResult](t, g, protocol.FrameSkillArchiveAdd, protocol.SkillArchiveAdd{
		Location: protocol.ToolImportLocation{NodeID: n, Location: "/skills/demo"}, By: "cto",
	})
	if got.State != "added" || got.Name != "demo" || got.Version != 1 || got.Change == "" {
		t.Fatalf("add: %+v", got)
	}
	if got.Description != "A demo skill." {
		t.Errorf("description %q", got.Description)
	}
	if got.Tokens <= 0 {
		t.Errorf("tokens %d", got.Tokens)
	}
	rows := archiveList(t, g)
	if len(rows) != 1 || rows[0].Name != "demo" || rows[0].SourceKind != "machine" || rows[0].SourceNode != n || rows[0].SourcePath != "/skills/demo" {
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

func TestSkillArchiveIntakeFromGitStoreCopiesEveryVersionOnce(t *testing.T) {
	g := archiveGateway(t)
	repo, err := openSkillStore(g.opts.DataDir, g.store)
	if err != nil {
		t.Fatal(err)
	}
	for i, body := range []string{"---\ndescription: one.\n---\na\n", "---\ndescription: two.\n---\nb\n"} {
		dir := t.TempDir()
		if err = os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		hash, e := inventory.SkillHash(dir)
		if e != nil {
			t.Fatal(e)
		}
		bundle, e := inventory.PackSkill(dir)
		if e != nil {
			t.Fatal(e)
		}
		item := seedIdentity(t, g.store, "skill", "alpha", hash)
		if _, e = repo.put(item, hash, bundle, "n", "/skills/alpha"); e != nil {
			t.Fatalf("put %d: %v", i, e)
		}
	}
	first, err := g.intakeSkillGit()
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Shared) != 1 || first.Shared[0] != "alpha" {
		t.Fatalf("two contents under one name must be listed: %+v", first)
	}
	rows := archiveList(t, g)
	if len(rows) != 1 || rows[0].Versions != 2 {
		t.Fatalf("versions after intake: %+v", rows)
	}
	second, err := g.intakeSkillGit()
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Shared) != 0 {
		t.Fatalf("repeat intake reported new shared names: %+v", second)
	}
	if rows = archiveList(t, g); rows[0].Versions != 2 {
		t.Fatalf("repeat intake made a version: %+v", rows)
	}
}

func TestSkillArchiveIntakeFromMachines(t *testing.T) {
	g, n, _ := toggleFixture(t)
	dir := t.TempDir()
	folderA, hashA := archiveSkillFolder(t, filepath.Join(dir, "a"), "---\ndescription: First copy.\n---\nbody\n")
	folderB, hashB := archiveSkillFolder(t, filepath.Join(dir, "b"), "---\ndescription: Second copy.\n---\nother\n")
	if err := g.store.SetInventory(n, protocol.InventoryReport{State: "ok", ScannedAt: time.Now(), Items: []protocol.InventoryItem{
		{Kind: "skill", Name: "demo", Hash: hashA, State: "ok", Scope: "user", Location: folderA},
		{Kind: "skill", Name: "demo", Hash: hashB, State: "ok", Scope: "project", Location: folderB},
		{Kind: "skill", Name: "only-plugin", Hash: "plugin", State: "ok", Scope: "plugin", Location: "/plugin/x"},
	}}); err != nil {
		t.Fatal(err)
	}
	archiveFakeNode(t, g, n, map[string][]byte{hashA: archiveBundle(t, folderA), hashB: archiveBundle(t, folderB)})
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
	bundle, err := inventory.PackSkill(dir)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

// archiveFakeNode answers node.skill_bundle with the bundle of the asked hash,
// so one test can serve several distinct skill contents.
func archiveFakeNode(t *testing.T, g *Gateway, n string, bundles map[string][]byte) {
	t.Helper()
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
	g.mu.Lock()
	g.conns[n] = nc
	g.mu.Unlock()
	done := make(chan struct{})
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
			if h.Type == "node.skill_bundle" {
				var q protocol.SkillBundleRequest
				_ = json.Unmarshal(raw, &q)
				if bundle, ok := bundles[q.Hash]; ok {
					data = protocol.SkillBundle{Data: bundle}
				} else {
					answerErr = errors.New("no such bundle")
				}
			}
			if h.ID != "" {
				b, _ := protocol.EncodeResult(h.ID, data, answerErr)
				var reply protocol.Header
				_ = json.Unmarshal(b, &reply)
				nc.answer(reply)
			}
		}
	}()
	t.Cleanup(func() {
		g.mu.Lock()
		delete(g.conns, n)
		g.mu.Unlock()
		cancel()
		client.CloseNow()
		ws.CloseNow()
		<-done
	})
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
