package delivery

import (
	"context"
	"errors"
	"os"
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
			f.setConfig("core.askPass", f.touchCmd())
		},
		"external diff": func(f *configFx) {
			f.setConfig("diff.external", f.touchCmd())
		},
		"uploadpack": func(f *configFx) {
			f.setConfig("remote.origin.uploadpack", f.touchCmd())
		},
		"gpg program": func(f *configFx) {
			f.setConfig("gpg.program", f.touchCmd())
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

// A repository whose config is clean when Merge starts but gets a driver
// before the push (a worker writing it while Merge runs) is refused at the
// push, and the program does not run.
func TestMergeRefusesConfigChangedBeforePush(t *testing.T) {
	f := newConfigFx(t)
	r := &mergeRunner{beforePush: func() {
		f.setConfig("core.sshCommand", f.touchCmd())
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
	// The push URL counts too: a pushurl that differs is refused.
	r.RemoteURL = f.origin
	f.setConfig("remote.origin.pushurl", "https://example.com/other.git")
	if _, err := Merge(context.Background(), r); !errors.Is(err, ErrRemoteURL) {
		t.Fatalf("pushurl: err = %v, want ErrRemoteURL", err)
	}
	mergeGit(t, f.repo, "config", "--unset", "remote.origin.pushurl")
	if _, err := Merge(context.Background(), r); err != nil {
		t.Fatalf("matching URL: %v", err)
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
