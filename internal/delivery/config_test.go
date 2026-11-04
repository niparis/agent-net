package delivery

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUnsafeConfigKey(t *testing.T) {
	unsafe := []struct{ key, value string }{
		{"filter.lfs.smudge", "git-lfs smudge"},
		{"filter.x.clean", "x"},
		{"filter.x.process", "x"},
		{"Filter.X.Smudge", "x"},
		{"merge.x.driver", "x %O %A %B"},
		{"core.sshcommand", "x"},
		{"core.askpass", "x"},
		{"core.gitproxy", "x"},
		{"url.https://evil/.insteadof", "https://good/"},
		{"url.https://evil/.pushinsteadof", "https://good/"},
		{"credential.helper", "x"},
		{"credential.https://good.username", "x"},
		{"diff.external", "x"},
		{"diff.x.command", "x"},
		{"diff.x.textconv", "x"},
		{"gpg.program", "x"},
		{"gpg.ssh.program", "x"},
		{"gpg.x509.program", "x"},
		{"protocol.ext.allow", "always"},
		{"protocol.allow", "always"},
		{"http.proxy", "http://evil"},
		{"http.https://good/.sslverify", "false"},
		{"remote.origin.vcs", "x"},
		{"remote.origin.uploadpack", "x"},
		{"remote.origin.receivepack", "x"},
		{"remote.origin.proxy", "http://evil"},
		{"lfs.url", "https://evil"},
		{"submodule.x.update", "!x"},
		{"core.worktree", "/tmp/decoy"},
		{"http.sslverify", "false"},
		{"http.https://good/.extraheader", "X: y"},
		{"http.https://good/.proxy", "http://evil"},
		{"http.cookiefile", "/tmp/c"},
	}
	for _, c := range unsafe {
		if why := unsafeConfigKey(c.key, c.value); why == "" {
			t.Errorf("%s was accepted", c.key)
		}
	}
	safe := []struct{ key, value string }{
		{"core.repositoryformatversion", "0"},
		{"core.filemode", "true"},
		{"core.autocrlf", "input"},
		{"remote.origin.url", "https://good/x.git"},
		{"remote.origin.pushurl", "https://good/x.git"},
		{"remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*"},
		{"branch.main.remote", "origin"},
		{"branch.main.merge", "refs/heads/main"},
		{"user.name", "A"},
		{"user.email", "a@example.com"},
		{"filter.lfs.required", "true"},
		{"merge.ff", "only"},
		{"merge.conflictstyle", "diff3"},
		{"submodule.x.update", "checkout"},
		{"submodule.x.url", "https://good/s.git"},
		{"push.default", "simple"},
		{"commit.gpgsign", "false"},
		{"http.version", "HTTP/1.1"},
		{"http.postbuffer", "524288000"},
		{"http.lowspeedlimit", "1000"},
		{"http.https://good/.version", "HTTP/1.1"},
		{"http.useragent", "x"},
	}
	for _, c := range safe {
		if why := unsafeConfigKey(c.key, c.value); why != "" {
			t.Errorf("%s was refused: %s", c.key, why)
		}
	}
}

// configFx is a merge fixture whose repository config is changed by the test.
// Every driver writes a marker file, so a test proves no driver ran.
type configFx struct {
	*mergeFx
	marker string
	head   string
}

func newConfigFx(t *testing.T) *configFx {
	t.Helper()
	f := newMergeFx(t)
	// A change that adds a tracked file with a filter attribute, so a
	// checkout or reset in the merge worktree would run the filter.
	f.write(f.repo, ".gitattributes", "*.dat filter=evil merge=evil\n")
	f.write(f.repo, "x.dat", "base\n")
	mergeGit(t, f.repo, "add", ".")
	mergeGit(t, f.repo, "commit", "-q", "-m", "attrs")
	mergeGit(t, f.repo, "push", "-q", "origin", "main")
	f.base = mergeGit(t, f.repo, "rev-parse", "HEAD")
	mergeGit(t, f.repo, "checkout", "-q", "-B", "feat", "main")
	f.write(f.repo, "x.dat", "feat\n")
	mergeGit(t, f.repo, "add", ".")
	mergeGit(t, f.repo, "commit", "-q", "-m", "change feat")
	head := mergeGit(t, f.repo, "rev-parse", "HEAD")
	mergeGit(t, f.repo, "checkout", "-q", "main")
	// Move origin on, so merge-commit mode makes a real merge commit.
	f.advanceOrigin("o.txt", "o\n")
	return &configFx{mergeFx: f, marker: filepath.Join(f.root, "driver-ran"), head: head}
}

func (f *configFx) touchCmd() string { return "touch " + f.marker }

// script writes an executable that touches the marker and fails, for keys
// whose value git runs as a program with arguments and without a shell.
func (f *configFx) script(name string) string {
	f.t.Helper()
	p := filepath.Join(f.root, name)
	f.write(f.root, name, "#!/bin/sh\n"+f.touchCmd()+"\nexit 1\n")
	if err := os.Chmod(p, 0o755); err != nil {
		f.t.Fatal(err)
	}
	return p
}

func (f *configFx) setConfig(key, value string) {
	f.t.Helper()
	mergeGit(f.t, f.repo, "config", "--local", key, value)
}

func (f *configFx) assertRefused(err error) {
	f.t.Helper()
	if !errors.Is(err, ErrUnsafeConfig) {
		f.t.Fatalf("err = %v, want ErrUnsafeConfig", err)
	}
	if _, serr := os.Stat(f.marker); serr == nil {
		f.t.Fatal("a configured program ran")
	}
}

func TestMergeRefusesRepoConfigThatRunsPrograms(t *testing.T) {
	for name, set := range map[string]func(f *configFx){
		"filter smudge": func(f *configFx) {
			f.setConfig("filter.evil.smudge", f.touchCmd())
		},
		"filter clean": func(f *configFx) {
			f.setConfig("filter.evil.clean", f.touchCmd())
		},
		"filter process": func(f *configFx) {
			f.setConfig("filter.evil.process", f.touchCmd())
		},
		"merge driver": func(f *configFx) {
			// Both sides change x.dat, so the merge needs the driver.
			f.advanceOrigin("x.dat", "origin\n")
			f.setConfig("merge.evil.driver", f.touchCmd()+" %O %A %B")
		},
		"ssh command": func(f *configFx) {
			f.setConfig("core.sshCommand", f.touchCmd())
			f.setConfig("remote.origin.url", "fakehost:/nowhere.git")
		},
		"insteadOf": func(f *configFx) {
			f.setConfig("url.ext::"+f.touchCmd()+" #.insteadOf", f.origin)
		},
		"pushInsteadOf": func(f *configFx) {
			f.setConfig("url.ext::"+f.touchCmd()+" #.pushInsteadOf", f.origin)
		},
		"credential helper": func(f *configFx) {
			f.setConfig("credential.helper", "!"+f.touchCmd())
		},
		"askpass": func(f *configFx) {
			f.setConfig("core.askPass", f.script("fake-askpass"))
		},
		"external diff": func(f *configFx) {
			f.setConfig("diff.external", f.touchCmd())
		},
		"uploadpack": func(f *configFx) {
			f.setConfig("remote.origin.uploadpack", f.touchCmd())
		},
		"gpg program": func(f *configFx) {
			f.setConfig("gpg.program", f.script("fake-gpg"))
		},
	} {
		for _, mode := range []string{MergeModeFFOnly, MergeModeCommit} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				f := newConfigFx(t)
				set(f)
				before := f.originMain()
				_, err := Merge(context.Background(), f.req("feat", f.head, mode))
				f.assertRefused(err)
				if got := f.originMain(); got != before {
					t.Fatalf("origin main moved to %s", got)
				}
			})
		}
	}
}

func TestMergeRefusesConfigFromAnInclude(t *testing.T) {
	f := newConfigFx(t)
	inc := filepath.Join(f.root, "extra.cfg")
	f.write(f.root, "extra.cfg", "[filter \"evil\"]\n\tsmudge = "+f.touchCmd()+"\n")
	f.setConfig("include.path", inc)
	_, err := Merge(context.Background(), f.req("feat", f.head, MergeModeCommit))
	f.assertRefused(err)
}

func TestMergeRefusesWorktreeConfig(t *testing.T) {
	f := newConfigFx(t)
	mergeGit(t, f.repo, "config", "extensions.worktreeConfig", "true")
	mergeGit(t, f.repo, "config", "--worktree", "filter.evil.smudge", f.touchCmd())
	_, err := Merge(context.Background(), f.req("feat", f.head, MergeModeCommit))
	f.assertRefused(err)
}

// The mirror is written only by the daemon, but if its config gets a driver
// anyway between the start and the push, the push is refused and the program
// does not run.
func TestMergeRefusesMirrorConfigChangedBeforePush(t *testing.T) {
	f := newConfigFx(t)
	r := &mergeRunner{beforePush: func() {
		mergeGit(t, f.mirror, "config", "core.sshCommand", f.touchCmd())
	}}
	before := f.originMain()
	_, err := r.merge(context.Background(), f.req("feat", f.head, MergeModeCommit))
	f.assertRefused(err)
	if got := f.originMain(); got != before {
		t.Fatalf("origin main moved to %s", got)
	}
}

// The same keys set in the owner's global config are trusted. A worker
// cannot write that file, and a push over https needs the owner's helper.
func TestMergeTrustsGlobalConfig(t *testing.T) {
	f := newConfigFx(t)
	global := filepath.Join(f.root, "global.cfg")
	f.write(f.root, "global.cfg", "[filter \"evil\"]\n\tsmudge = cat\n\tclean = cat\n")
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	if _, err := Merge(context.Background(), f.req("feat", f.head, MergeModeCommit)); err != nil {
		t.Fatal(err)
	}
}

func TestMergeAllowsBenignRepoConfig(t *testing.T) {
	f := newConfigFx(t)
	f.setConfig("core.autocrlf", "input")
	f.setConfig("user.name", "Someone")
	f.setConfig("branch.main.remote", "origin")
	f.setConfig("push.default", "simple")
	f.setConfig("filter.evil.required", "false")
	if _, err := Merge(context.Background(), f.req("feat", f.head, MergeModeCommit)); err != nil {
		t.Fatal(err)
	}
}

func TestMergeRemoteURLMustMatch(t *testing.T) {
	f := newConfigFx(t)
	r := f.req("feat", f.head, MergeModeCommit)
	r.RemoteURL = "https://example.com/not-this.git"
	before := f.originMain()
	_, err := Merge(context.Background(), r)
	if !errors.Is(err, ErrRemoteURL) {
		t.Fatalf("err = %v, want ErrRemoteURL", err)
	}
	if got := f.originMain(); got != before {
		t.Fatal("origin moved")
	}
	// A differing pushurl is ignored: merge and push use RemoteURL.
	r.RemoteURL = f.origin
	f.setConfig("remote.origin.pushurl", "https://example.com/other.git")
	if _, err := Merge(context.Background(), r); err != nil {
		t.Fatalf("differing pushurl: %v", err)
	}
	if got := f.originMain(); got == before {
		t.Fatal("origin did not move to merged commit")
	}
}

func TestCheckRepoGuardsOtherEntryPoints(t *testing.T) {
	f := newConfigFx(t)
	f.setConfig("filter.evil.clean", f.touchCmd())
	g := gitIn{dir: f.repo}
	if err := checkConfig(context.Background(), g); !errors.Is(err, ErrUnsafeConfig) {
		t.Fatalf("checkConfig: %v", err)
	}
	f.write(f.repo, "x.dat", "dirty\n")
	// RunStep runs git status, which would run the clean filter.
	_, err := RunStep(context.Background(), StepCommand{Step: "release", Dir: f.repo, Commit: f.base, Argv: []string{"true"}, Timeout: 10 * time.Second})
	if !errors.Is(err, ErrUnsafeConfig) {
		t.Fatalf("RunStep: %v", err)
	}
	_, err = NewCommits(context.Background(), WatchRequest{RepoDir: f.repo, Remote: "origin", Branch: "main"})
	if !errors.Is(err, ErrUnsafeConfig) {
		t.Fatalf("NewCommits: %v", err)
	}
	if _, serr := os.Stat(f.marker); serr == nil {
		t.Fatal("a configured program ran")
	}
	if !strings.Contains(unsafeConfigKey("filter.evil.clean", "x"), "filter") {
		t.Fatal("reason should name the kind of key")
	}
}

// Positive controls: for the keys that the other tests cannot trigger through
// a local-path remote, show with plain git that each one runs its program,
// then that checkConfig refuses the same config. Without the control, an
// absent marker could mean the vector never worked.
func TestConfigVectorsRunPlainGit(t *testing.T) {
	srv := newAuthServer(t)
	cases := map[string]struct {
		set func(f *configFx)
		run func(f *configFx) // plain git, no guard
	}{
		"credential helper": {
			set: func(f *configFx) { f.setConfig("credential.helper", "!"+f.touchCmd()+";:") },
			run: func(f *configFx) { plainGit(f.repo, "ls-remote", srv.URL+"/x.git") },
		},
		"askpass": {
			set: func(f *configFx) { f.setConfig("core.askPass", f.script("fake-askpass")) },
			run: func(f *configFx) { plainGit(f.repo, "ls-remote", srv.URL+"/x.git") },
		},
		"external diff": {
			set: func(f *configFx) { f.setConfig("diff.external", f.touchCmd()) },
			run: func(f *configFx) {
				f.write(f.repo, "README", "changed\n")
				plainGit(f.repo, "diff")
			},
		},
		"gpg program": {
			set: func(f *configFx) { f.setConfig("gpg.program", f.script("fake-gpg")) },
			run: func(f *configFx) {
				plainGit(f.repo, "-c", "user.name=T", "-c", "user.email=t@example.com",
					"commit", "-q", "--allow-empty", "-S", "-m", "signed")
			},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newConfigFx(t)
			c.set(f)
			c.run(f)
			if _, err := os.Stat(f.marker); err != nil {
				t.Fatalf("control: plain git did not run the program: %v", err)
			}
			if err := checkConfig(context.Background(), gitIn{dir: f.repo}); !errors.Is(err, ErrUnsafeConfig) {
				t.Fatalf("checkConfig = %v, want ErrUnsafeConfig", err)
			}
		})
	}
}

func plainGit(dir string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// An askpass program set in the environment would beat core.askPass.
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_ASKPASS=") && !strings.HasPrefix(kv, "SSH_ASKPASS=") {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, "GIT_TERMINAL_PROMPT=0")
	_ = cmd.Run() // the failure is expected; only the side effect counts
}

// newAuthServer answers every request with 401, so git asks for credentials.
func newAuthServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="x"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The mirror, not RepoDir, is where Merge works: RepoDir keeps its refs and
// config exactly as they were.
func TestMergeLeavesRepoDirAlone(t *testing.T) {
	f := newConfigFx(t)
	snapshot := func() string {
		return mergeGit(t, f.repo, "for-each-ref") + "\n" + mergeGit(t, f.repo, "config", "--local", "--list")
	}
	before := snapshot()
	if _, err := Merge(context.Background(), f.req("feat", f.head, MergeModeCommit)); err != nil {
		t.Fatal(err)
	}
	if after := snapshot(); after != before {
		t.Fatalf("RepoDir changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestMergeRefusesForeignMirror(t *testing.T) {
	f := newConfigFx(t)
	if err := os.MkdirAll(f.mirror, 0o755); err != nil {
		t.Fatal(err)
	}
	f.write(f.mirror, "precious.txt", "keep\n")
	_, err := Merge(context.Background(), f.req("feat", f.head, MergeModeCommit))
	if !errors.Is(err, ErrNotOwnedMirror) {
		t.Fatalf("err = %v, want ErrNotOwnedMirror", err)
	}
	if _, serr := os.Stat(filepath.Join(f.mirror, "precious.txt")); serr != nil {
		t.Fatal("the foreign directory was changed")
	}
}

func TestMergeRequiresRemoteURL(t *testing.T) {
	f := newConfigFx(t)
	r := f.req("feat", f.head, MergeModeCommit)
	r.RemoteURL = ""
	if _, err := Merge(context.Background(), r); err == nil {
		t.Fatal("want an error without RemoteURL")
	}
	r.RemoteURL = "--upload-pack=touch x"
	if _, err := Merge(context.Background(), r); err == nil {
		t.Fatal("want an error for an option as RemoteURL")
	}
}

// The push goes to the pinned URL even when the repository's own config
// names another pushurl; the differing pushurl is ignored.
func TestMergeIgnoresRedirectedPushURL(t *testing.T) {
	f := newConfigFx(t)
	alt := filepath.Join(f.root, "alt.git")
	mergeGit(t, f.root, "clone", "-q", "--bare", f.origin, alt)
	if err := os.WriteFile(filepath.Join(alt, "hooks", "pre-receive"), []byte("#!/bin/sh\ntouch "+f.marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.setConfig("remote.origin.pushurl", alt)
	res, err := Merge(context.Background(), f.req("feat", f.head, MergeModeCommit))
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !res.Pushed {
		t.Fatal("expected push")
	}
	if _, serr := os.Stat(f.marker); serr == nil {
		t.Fatal("the redirected remote's hook ran")
	}
}
