package daemon

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// fileTestEnv is a fake home with credential folders and a project, so the
// path rules can be checked without touching the real home.
type fileTestEnv struct {
	home, project, state, config, snapshots string
}

func newFileTestEnv(t *testing.T) fileTestEnv {
	t.Helper()
	root := t.TempDir()
	e := fileTestEnv{
		home:      filepath.Join(root, "home"),
		project:   filepath.Join(root, "home", "code", "project"),
		state:     filepath.Join(root, "home", ".local", "state", "agent-net", "daemon"),
		config:    filepath.Join(root, "home", ".config", "agent-net"),
		snapshots: filepath.Join(root, "outgoing"),
	}
	for _, dir := range []string{e.project, e.state, e.config, filepath.Join(e.home, ".ssh"), filepath.Join(e.home, "Library", "Keychains"), filepath.Join(e.home, ".aws")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

// rules allows no temporary folder: the test's own folders may be under
// /tmp. The /tmp case sets Temp itself.
func (e fileTestEnv) rules() FileRules {
	return FileRules{Roots: []string{e.project}, Temp: []string{}, Home: e.home, Private: []string{e.state, e.config}}
}

func writeTestFile(t *testing.T, path string, data []byte) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// The manifest holds the name, size, SHA-256 and an ID of each file, and the
// bytes are sent from a snapshot taken while hashing, so a file that changes
// after the check cannot reach the receiver with a hash that does not match.
func TestPrepareFilesManifestHashing(t *testing.T) {
	e := newFileTestEnv(t)
	report := []byte("<html>report</html>")
	img := append([]byte{0x89, 'P', 'N', 'G', 0}, bytes.Repeat([]byte{7}, 5000)...)
	p1 := writeTestFile(t, filepath.Join(e.project, "out", "my report.html"), report)
	p2 := writeTestFile(t, filepath.Join(e.project, "chart.png"), img)
	out, err := PrepareFiles([]string{p1, p2}, e.rules(), e.snapshots)
	if err != nil {
		t.Fatalf("PrepareFiles: %v", err)
	}
	defer out.Remove()
	want := []protocol.FileEntry{
		{ID: "f1", Name: "my report.html", Size: int64(len(report)), SHA256: sha256Hex(report)},
		{ID: "f2", Name: "chart.png", Size: int64(len(img)), SHA256: sha256Hex(img)},
	}
	got := out.Manifest()
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("manifest = %+v\nwant %+v", got, want)
	}
	if err := protocol.ValidateFiles(got); err != nil {
		t.Fatalf("the manifest breaks the protocol limits: %v", err)
	}
	// Changing the source now does not change what is sent.
	writeTestFile(t, p1, []byte("changed"))
	snap, err := os.ReadFile(out.Files[0].Copy)
	if err != nil || !bytes.Equal(snap, report) {
		t.Fatalf("snapshot = %q, %v", snap, err)
	}
	if !strings.HasPrefix(out.Files[0].Copy, e.snapshots+string(filepath.Separator)) {
		t.Fatalf("snapshot %s is not under %s", out.Files[0].Copy, e.snapshots)
	}
	if err := out.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out.Dir); !os.IsNotExist(err) {
		t.Fatalf("the snapshot folder was not removed: %v", err)
	}
}

func TestPrepareFilesRejectsSwappedFile(t *testing.T) {
	for _, tc := range []struct {
		swap     string
		confined bool
	}{
		{"symlink", false}, {"replacement", false}, {"parent symlink", false},
		{"symlink", true}, {"replacement", true}, {"parent symlink", true},
		{"moved parent", false}, {"moved parent", true},
	} {
		t.Run(fmt.Sprintf("%s/confined=%t", tc.swap, tc.confined), func(t *testing.T) {
			e := newFileTestEnv(t)
			sub := filepath.Join(e.project, "sub")
			source := writeTestFile(t, filepath.Join(sub, "result.txt"), []byte("safe"))
			outsideDir := filepath.Join(e.home, "outside")
			outside := writeTestFile(t, filepath.Join(outsideDir, "result.txt"), []byte("outside contents"))
			rules := e.rules()
			rules.ConfineSymlinks = tc.confined
			out, err := prepareFiles([]string{source}, rules, e.snapshots, func() {
				switch tc.swap {
				case "symlink":
					if err := os.Rename(source, source+".old"); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(outside, source); err != nil {
						t.Fatal(err)
					}
				case "replacement":
					if err := os.Rename(outside, source); err != nil {
						t.Fatal(err)
					}
				case "parent symlink":
					if err := os.Rename(sub, sub+".old"); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(outsideDir, sub); err != nil {
						t.Fatal(err)
					}
				case "moved parent":
					moved := filepath.Join(e.home, "moved")
					if err := os.Rename(sub, moved); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(moved, sub); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(moved, "result.txt"), []byte("outside contents"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			})
			if err == nil {
				defer out.Remove()
				content, _ := os.ReadFile(out.Files[0].Copy)
				t.Fatalf("accepted swapped file with snapshot %q", content)
			}
			entries, _ := os.ReadDir(e.snapshots)
			if len(entries) != 0 {
				t.Fatalf("refusal left snapshots: %v", entries)
			}
		})
	}
}

func TestPrepareFilesRejectsConfinedHardlinks(t *testing.T) {
	e := newFileTestEnv(t)
	outside := writeTestFile(t, filepath.Join(e.home, "outside.txt"), []byte("outside"))
	linked := filepath.Join(e.project, "linked.txt")
	if err := os.Link(outside, linked); err != nil {
		t.Fatal(err)
	}
	rules := e.rules()
	rules.ConfineSymlinks = true
	if _, err := PrepareFiles([]string{linked}, rules, e.snapshots); err == nil || !strings.Contains(strings.ToLower(err.Error()), "hard link") {
		t.Fatalf("confined hard link: %v", err)
	}
	rules.ConfineSymlinks = false
	out, err := PrepareFiles([]string{linked}, rules, e.snapshots)
	if err != nil {
		t.Fatalf("unconfined hard link: %v", err)
	}
	out.Remove()

	// The link count must come from the open descriptor, after the path
	// check: another link can be created in between.
	fresh := writeTestFile(t, filepath.Join(e.project, "fresh.txt"), []byte("safe"))
	rules.ConfineSymlinks = true
	_, err = prepareFiles([]string{fresh}, rules, e.snapshots, func() {
		if err := os.Link(fresh, filepath.Join(e.home, "late-link.txt")); err != nil {
			t.Fatal(err)
		}
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "hard link") {
		t.Fatalf("link added after path check: %v", err)
	}
}

func TestPrepareFilesSymlinkedRoot(t *testing.T) {
	e := newFileTestEnv(t)
	file := writeTestFile(t, filepath.Join(e.project, "result.txt"), []byte("ok"))
	alias := filepath.Join(e.home, "alias")
	if err := os.Symlink(e.project, alias); err != nil {
		t.Fatal(err)
	}
	rules := e.rules()
	rules.Roots, rules.ConfineSymlinks = []string{alias}, true
	for _, path := range []string{file, filepath.Join(alias, "result.txt")} {
		out, err := PrepareFiles([]string{path}, rules, e.snapshots)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		out.Remove()
	}
	outside := writeTestFile(t, filepath.Join(e.home, "outside.txt"), []byte("outside"))
	link := filepath.Join(e.project, "escape.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, filepath.Join(alias, "escape.txt")} {
		if out, err := PrepareFiles([]string{path}, rules, e.snapshots); err == nil {
			out.Remove()
			t.Errorf("accepted escape %s", path)
		}
	}
}

func TestPrepareFilesSymlinkedWorkerRootInState(t *testing.T) {
	e := newFileTestEnv(t)
	worktree := filepath.Join(e.state, "worktrees", "w_1")
	file := writeTestFile(t, filepath.Join(worktree, "result.txt"), []byte("ok"))
	alias := filepath.Join(e.state, "worker-link")
	if err := os.Symlink(worktree, alias); err != nil {
		t.Fatal(err)
	}
	rules := e.rules()
	rules.Roots, rules.ConfineSymlinks = []string{alias}, true
	for _, path := range []string{file, filepath.Join(alias, "result.txt")} {
		out, err := PrepareFiles([]string{path}, rules, e.snapshots)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		out.Remove()
	}
	other := writeTestFile(t, filepath.Join(e.state, "secret"), []byte("private"))
	if out, err := PrepareFiles([]string{other}, rules, e.snapshots); err == nil {
		out.Remove()
		t.Fatal("accepted a file outside the worker root")
	}
}

func TestPrepareFilesLimits(t *testing.T) {
	e := newFileTestEnv(t)
	small := writeTestFile(t, filepath.Join(e.project, "a.txt"), []byte("a"))

	big := filepath.Join(e.project, "big.bin")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	// A sparse file: the size check comes before any byte is read.
	if err := f.Truncate(protocol.MaxFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := PrepareFiles([]string{big}, e.rules(), e.snapshots); err == nil || !strings.Contains(err.Error(), "100 MB") {
		t.Fatalf("a file over 100 MB: err=%v", err)
	}

	var eleven []string
	for i := 0; i < protocol.MaxFilesPerMessage+1; i++ {
		eleven = append(eleven, writeTestFile(t, filepath.Join(e.project, "many", string(rune('a'+i))+".txt"), []byte("x")))
	}
	if _, err := PrepareFiles(eleven, e.rules(), e.snapshots); err == nil || !strings.Contains(err.Error(), "10 files") {
		t.Fatalf("eleven files: err=%v", err)
	}
	if _, err := PrepareFiles(nil, e.rules(), e.snapshots); err == nil {
		t.Fatal("no files was accepted")
	}

	twin := writeTestFile(t, filepath.Join(e.project, "sub", "a.txt"), []byte("b"))
	if _, err := PrepareFiles([]string{small, twin}, e.rules(), e.snapshots); err == nil || !strings.Contains(err.Error(), "a.txt") {
		t.Fatalf("two files with one name: err=%v", err)
	}
	if _, err := PrepareFiles([]string{"a.txt"}, e.rules(), e.snapshots); err == nil {
		t.Fatal("a relative path was accepted")
	}
	if _, err := PrepareFiles([]string{e.project}, e.rules(), e.snapshots); err == nil {
		t.Fatal("a folder was accepted")
	}
	if _, err := PrepareFiles([]string{filepath.Join(e.project, "missing.txt")}, e.rules(), e.snapshots); err == nil {
		t.Fatal("a missing file was accepted")
	}
	entries, _ := os.ReadDir(e.snapshots)
	if len(entries) != 0 {
		t.Fatalf("refused sends left snapshots: %v", entries)
	}
}

// Only the session's project or worktree and /tmp are allowed, never a
// credential folder or agent-net's own state, and symlinks are resolved first.
func TestPrepareFilesForbiddenPaths(t *testing.T) {
	e := newFileTestEnv(t)
	key := writeTestFile(t, filepath.Join(e.home, ".ssh", "id_ed25519"), []byte("not a real key"))
	link := filepath.Join(e.project, "innocent.txt")
	if err := os.Symlink(key, link); err != nil {
		t.Fatal(err)
	}
	outside := writeTestFile(t, filepath.Join(e.home, "Documents", "notes.txt"), []byte("notes"))
	keychain := writeTestFile(t, filepath.Join(e.home, "Library", "Keychains", "login.keychain-db"), []byte("kc"))
	aws := writeTestFile(t, filepath.Join(e.home, ".aws", "credentials"), []byte("x"))
	stateFile := writeTestFile(t, filepath.Join(e.state, "state.db"), []byte("db"))
	configFile := writeTestFile(t, filepath.Join(e.config, "node.json"), []byte("{}"))
	escape := filepath.Join(e.project, "up")
	if err := os.Symlink(filepath.Join(e.home, "Documents"), escape); err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"symlink into ~/.ssh":           link,
		"outside the project":           outside,
		"symlinked folder out":          filepath.Join(escape, "notes.txt"),
		"agent-net state":               stateFile,
		"agent-net config":              configFile,
		"traversal out of the project":  filepath.Join(e.project, "..", "..", "Documents", "notes.txt"),
		"credential file in the home":   aws,
		"keychain":                      keychain,
		"ssh key with the home as root": key,
	}
	for what, path := range cases {
		rules := e.rules()
		if what == "ssh key with the home as root" {
			// A session that runs in the home folder has the home as its
			// project; the credential folders stay closed.
			rules.Roots = []string{e.home}
		}
		if _, err := PrepareFiles([]string{path}, rules, e.snapshots); err == nil {
			t.Errorf("%s: %s was accepted", what, path)
		}
	}
	homeRoot := e.rules()
	homeRoot.Roots = []string{e.home}
	for _, path := range []string{keychain, aws, stateFile, configFile} {
		if _, err := PrepareFiles([]string{path}, homeRoot, e.snapshots); err == nil {
			t.Errorf("with the home as root, %s was accepted", path)
		}
	}

	// /tmp is allowed.
	tmp, err := os.MkdirTemp("/tmp", "agent-net-files-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	inTmp := writeTestFile(t, filepath.Join(tmp, "result.json"), []byte(`{"ok":true}`))
	withTmp := e.rules()
	withTmp.Temp = nil // the default: /tmp
	out, err := PrepareFiles([]string{inTmp}, withTmp, e.snapshots)
	if err != nil {
		t.Fatalf("a file in /tmp was refused: %v", err)
	}
	out.Remove()

	// A worker's worktree lives under agent-net's state folder. It is the
	// worker's project, so its own files may go, but nothing else there.
	worktree := filepath.Join(e.state, "worktrees", "w_1")
	built := writeTestFile(t, filepath.Join(worktree, "report.html"), []byte("<p>built</p>"))
	worker := e.rules()
	worker.Roots = []string{worktree}
	out, err = PrepareFiles([]string{built}, worker, e.snapshots)
	if err != nil {
		t.Fatalf("a file in the worker's own worktree was refused: %v", err)
	}
	out.Remove()
	if _, err := PrepareFiles([]string{stateFile}, worker, e.snapshots); err == nil {
		t.Fatal("a worker sent agent-net state outside its worktree")
	}
}

// Text files go through the secret scanner; a hit stops the file unless the
// owner sends it anyway. Binary files are not scanned.
func TestPrepareFilesSecretScan(t *testing.T) {
	e := newFileTestEnv(t)
	token := "sk-" + strings.Repeat("a1", 12)
	text := writeTestFile(t, filepath.Join(e.project, "config.env"), []byte("API_URL=https://example.test\nOPENAI="+token+"\n"))
	_, err := PrepareFiles([]string{text}, e.rules(), e.snapshots)
	if err == nil || !strings.Contains(err.Error(), "secret") {
		t.Fatalf("a text file with a token: err=%v", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatal("the refusal repeats the secret")
	}
	entries, _ := os.ReadDir(e.snapshots)
	if len(entries) != 0 {
		t.Fatalf("a refused file left a snapshot: %v", entries)
	}

	owner := e.rules()
	owner.AllowSecrets = true
	out, err := PrepareFiles([]string{text}, owner, e.snapshots)
	if err != nil {
		t.Fatalf("the owner's override was refused: %v", err)
	}
	out.Remove()

	bin := writeTestFile(t, filepath.Join(e.project, "blob.bin"), append([]byte{0, 1, 2, 0}, []byte(token)...))
	out, err = PrepareFiles([]string{bin}, e.rules(), e.snapshots)
	if err != nil {
		t.Fatalf("a binary file was scanned as text: %v", err)
	}
	out.Remove()
}

// Received files are kept seven days, then the daemon's housekeeping removes
// them; leftover send snapshots go after a day.
func TestCleanFileFolders(t *testing.T) {
	state := t.TempDir()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	old := filepath.Join(state, "inbox", "s_bob", "m_old")
	fresh := filepath.Join(state, "inbox", "s_bob", "m_new")
	gone := filepath.Join(state, "inbox", "s_gone", "m_old2")
	writeTestFile(t, filepath.Join(old, "a.txt"), []byte("a"))
	writeTestFile(t, filepath.Join(fresh, "b.txt"), []byte("b"))
	writeTestFile(t, filepath.Join(gone, "c.txt"), []byte("c"))
	snapOld := filepath.Join(state, "files", "outgoing", "o_1")
	snapNew := filepath.Join(state, "files", "outgoing", "o_2")
	writeTestFile(t, filepath.Join(snapOld, "x"), []byte("x"))
	writeTestFile(t, filepath.Join(snapNew, "y"), []byte("y"))
	age := func(path string, d time.Duration) {
		if err := os.Chtimes(path, now.Add(-d), now.Add(-d)); err != nil {
			t.Fatal(err)
		}
	}
	age(old, 7*24*time.Hour+time.Minute)
	age(gone, 8*24*time.Hour)
	age(fresh, 6*24*time.Hour)
	age(snapOld, 25*time.Hour)
	age(snapNew, time.Hour)

	cleanFileFolders(state, now, nil)

	for _, p := range []string{old, gone, filepath.Join(state, "inbox", "s_gone"), snapOld} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s was kept: %v", p, err)
		}
	}
	for _, p := range []string{filepath.Join(fresh, "b.txt"), filepath.Join(snapNew, "y")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed: %v", p, err)
		}
	}
}
