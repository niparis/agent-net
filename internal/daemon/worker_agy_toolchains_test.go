package daemon

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/harnesses"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestAgyWorkerToolchainDirs(t *testing.T) {
	for _, tc := range []string{"go", "standard-location", "home", "parent-of-home", "symlink-home", "missing", "no-go"} {
		t.Run(tc, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			old := standardGoPaths
			standardGoPaths = func(home string) []string { return []string{filepath.Join(home, ".local", "go", "bin", "go")} }
			t.Cleanup(func() { standardGoPaths = old })
			w, records, repo := startAgyNode(t, shortSet)
			base := t.TempDir()
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			root, cache := filepath.Join(base, "go root"), filepath.Join(base, "modules")
			for _, dir := range []string{root, cache} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			var want []string
			switch tc {
			case "go", "standard-location":
				want = []string{root, cache}
			case "home":
				root, cache = home, home
			case "parent-of-home":
				root, cache = filepath.Dir(home), string(filepath.Separator)
			case "symlink-home":
				root = filepath.Join(base, "home-link")
				if err := os.Symlink(home, root); err != nil {
					t.Fatal(err)
				}
				cache = root
			case "missing":
				root, cache = filepath.Join(base, "missing"), filepath.Join(base, "absent")
			}
			t.Setenv("FAKE_GO_ROOT", root)
			t.Setenv("FAKE_GO_CACHE", cache)
			if tc != "no-go" {
				script := "#!/bin/sh\ncase \"$*\" in\n'--version') echo 'go version fake';;\n'env GOROOT GOMODCACHE') printf '%s\\n%s\\n' \"$FAKE_GO_ROOT\" \"$FAKE_GO_CACHE\";;\n*) exit 1;;\nesac\n"
				dir := base
				if tc == "standard-location" {
					// Not on the search path, only where the Go tarball is often unpacked.
					dir = filepath.Join(home, ".local", "go", "bin")
					if err := os.MkdirAll(dir, 0700); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(dir, "go"), []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
			}
			w.d.cfg.HarnessSearch = &harnesses.Options{PATH: base, Dirs: []string{}, Home: home,
				LoginShellPATH: func(_ context.Context) string { return "" }}
			res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:handback"})
			n := w.waitNotice(t, 1)[0]
			r := agyRecords(records)[0]
			var got []string
			for i, arg := range r.Args {
				if arg == "--add-dir" && i+1 < len(r.Args) {
					got = append(got, r.Args[i+1])
				}
			}
			toolchains := want
			gitDir := res.Worktree
			if gitDir == "" {
				gitDir = repo
			}
			want = append(append([]string(nil), toolchains...), workerGitRoots(context.Background(), gitDir)...)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("--add-dir: got %v, want %v; args: %v", got, want, r.Args)
			}
			if r.Args[len(r.Args)-1] != "-p=" {
				t.Fatalf("prompt must stay last: %v", r.Args)
			}
			if len(toolchains) > 0 {
				detail := "plus toolchains: " + strings.Join(toolchains, ", ")
				if !strings.Contains(r.Turns[0], detail) || !strings.Contains(n.Body, detail) {
					t.Errorf("toolchain permissions missing: brief=%s notice=%s", r.Turns[0], n.Body)
				}
				logs := w.logs.lines("worker.started")
				if len(logs) != 1 || !strings.Contains(logs[0], root) || !strings.Contains(logs[0], cache) {
					t.Errorf("toolchain start log: %v", logs)
				}
			}
		})
	}
}
