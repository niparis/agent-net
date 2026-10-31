package quotaaxi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeNode writes a node that prints version for --version.
func fakeNode(t *testing.T, dir, version string) string {
	t.Helper()
	path := filepath.Join(dir, "node")
	body := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo v" + version + "; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeNPM writes an npm that records its arguments and puts a quota-axi
// binary in the prefix it was given.
func fakeNPM(t *testing.T, dir, logPath string) string {
	t.Helper()
	path := filepath.Join(dir, "npm")
	body := `#!/bin/sh
if [ "$1" = "prefix" ]; then echo "$FAKE_NPM_PREFIX"; exit 0; fi
printf '%s\n' "$*" >> "$FAKE_NPM_LOG"
if [ -n "$FAKE_NPM_FAIL" ]; then echo "npm ERR! boom" >&2; exit 1; fi
prefix=""
while [ $# -gt 0 ]; do
  if [ "$1" = "--prefix" ]; then shift; prefix="$1"; fi
  shift
done
/bin/mkdir -p "$prefix/node_modules/.bin"
printf '#!/bin/sh\nexit 0\n' > "$prefix/node_modules/.bin/quota-axi"
/bin/chmod 755 "$prefix/node_modules/.bin/quota-axi"
`
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_NPM_LOG", logPath)
	return path
}

func setupPath(t *testing.T, version string) (home, log string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("AGENT_NET_DATA_DIR", "")
	bin := t.TempDir()
	fakeNode(t, bin, version)
	log = filepath.Join(t.TempDir(), "npm.log")
	fakeNPM(t, bin, log)
	t.Setenv("PATH", bin)
	return home, log
}

func fakeQuotaVersion(t *testing.T, path, version, calls string) {
	t.Helper()
	body := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf '%s\\n' \"$QUOTA_VERSION\" >> \"$QUOTA_VERSION_CALLS\"; echo \"quota-axi $QUOTA_VERSION\"; exit 0; fi\nexit 0\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUOTA_VERSION", version)
	t.Setenv("QUOTA_VERSION_CALLS", calls)
}

func TestPathSkipsOlderUserCopyForPrivateCopy(t *testing.T) {
	setupPath(t, "22.19.0")
	user := filepath.Join(t.TempDir(), quotaCommand)
	private := Binary()
	if err := os.MkdirAll(filepath.Dir(private), 0o755); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(t.TempDir(), "version-calls")
	fakeQuotaVersion(t, user, "0.1.68", calls)
	if err := os.WriteFile(private, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var logs []string
	logf := func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }
	if got, ok := Path(user, logf); !ok || got != private {
		t.Fatalf("old user copy was selected: %q %v", got, ok)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], user) || !strings.Contains(logs[0], "0.1.68") {
		t.Fatalf("skip log: %q", logs)
	}
}

func TestEnsureInstallsPinnedCopyWhenUserCopyIsTooOld(t *testing.T) {
	home, log := setupPath(t, "22.19.0")
	user := filepath.Join(home, ".local", "bin", quotaCommand)
	if err := os.MkdirAll(filepath.Dir(user), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeQuotaVersion(t, user, "0.1.68", filepath.Join(t.TempDir(), "version-calls"))
	var logs []string
	if err := Ensure(context.Background(), func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}); err != nil {
		t.Fatal(err)
	}
	if !usable(Binary()) {
		t.Fatal("Ensure did not install a private copy for the old user version")
	}
	if len(logs) != 2 || !strings.Contains(logs[0], user) || !strings.Contains(logs[0], "0.1.68") || !strings.Contains(logs[1], "installed "+Version) {
		t.Fatalf("logs: %q", logs)
	}
	if raw, err := os.ReadFile(log); err != nil || !strings.Contains(string(raw), "quota-axi@"+Version) {
		t.Fatalf("npm was not asked for the pinned copy: %q %v", raw, err)
	}
}

func TestEnsurePrivateInstallsAlongsideCompatibleUserCopy(t *testing.T) {
	home, log := setupPath(t, "22.19.0")
	user := filepath.Join(home, ".local", "bin", quotaCommand)
	if err := os.MkdirAll(filepath.Dir(user), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeQuotaVersion(t, user, Version, filepath.Join(t.TempDir(), "version-calls"))
	if err := EnsurePrivate(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if !usable(Binary()) {
		t.Fatal("EnsurePrivate did not install the private copy")
	}
	if raw, err := os.ReadFile(log); err != nil || !strings.Contains(string(raw), "quota-axi@"+Version) {
		t.Fatalf("npm was not asked for the pinned copy: %q %v", raw, err)
	}
}

func TestInstalledVersionCacheUsesMtime(t *testing.T) {
	path := filepath.Join(t.TempDir(), quotaCommand)
	calls := filepath.Join(t.TempDir(), "version-calls")
	fakeQuotaVersion(t, path, "0.1.68", calls)
	if got, err := InstalledVersion(path); err != nil || got != "0.1.68" {
		t.Fatalf("first version: %q %v", got, err)
	}
	if got, err := InstalledVersion(path); err != nil || got != "0.1.68" {
		t.Fatalf("cached version: %q %v", got, err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf '%s\\n' \"$QUOTA_VERSION\" >> \"$QUOTA_VERSION_CALLS\"; echo \"quota-axi $QUOTA_VERSION\"; exit 0; fi\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUOTA_VERSION", "0.1.69")
	changed := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, changed, changed); err != nil {
		t.Fatal(err)
	}
	if got, err := InstalledVersion(path); err != nil || got != "0.1.69" {
		t.Fatalf("version after mtime change: %q %v", got, err)
	}
	if got, err := os.ReadFile(calls); err != nil || strings.TrimSpace(string(got)) != "0.1.68\n0.1.69" {
		t.Fatalf("version command cache invalidation: %q %v", got, err)
	}
}

func TestEnsureInstallsThePinnedVersionInThePrivatePrefix(t *testing.T) {
	_, log := setupPath(t, "22.19.0")
	if err := Ensure(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if !usable(Binary()) {
		t.Fatalf("no private quota-axi at %s", Binary())
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := "install --prefix " + Prefix() + " --no-fund --no-audit quota-axi@" + Version
	if got := strings.TrimSpace(string(raw)); got != want {
		t.Fatalf("npm arguments:\n got %q\nwant %q", got, want)
	}
	if got, ok := Path(""); !ok || got != Binary() {
		t.Fatalf("private copy not found: %q %v", got, ok)
	}
}

func TestEnsureSkipsWhenTheUserHasACopy(t *testing.T) {
	home, log := setupPath(t, "22.19.0")
	local := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	user := filepath.Join(local, "quota-axi")
	if err := os.WriteFile(user, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("npm ran although the user has quota-axi: %v", err)
	}
	if usable(Binary()) {
		t.Fatal("installed a private copy next to the user's copy")
	}
}

func TestEnsureReportsNodeTooOld(t *testing.T) {
	_, log := setupPath(t, "22.18.9")
	err := Ensure(context.Background(), nil)
	if !errors.Is(err, ErrNeedNode) {
		t.Fatalf("error %v is not ErrNeedNode", err)
	}
	if err.Error() != NeedNodeMessage {
		t.Fatalf("message %q", err.Error())
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("npm ran with an old node: %v", err)
	}
	if err := NodeProblem(); !errors.Is(err, ErrNeedNode) {
		t.Fatalf("NodeProblem: %v", err)
	}
}

func TestEnsureReportsMissingNPM(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("AGENT_NET_DATA_DIR", "")
	bin := t.TempDir()
	fakeNode(t, bin, "22.20.0")
	t.Setenv("PATH", bin)
	if err := Ensure(context.Background(), nil); !errors.Is(err, ErrNeedNode) {
		t.Fatalf("error %v is not ErrNeedNode", err)
	}
	if _, err := Remove(); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureRetriesAtMostOnceADay(t *testing.T) {
	_, log := setupPath(t, "22.19.0")
	t.Setenv("FAKE_NPM_FAIL", "1")
	if err := Ensure(context.Background(), nil); err == nil {
		t.Fatal("a failing npm did not report an error")
	}
	if err := Ensure(context.Background(), nil); err != nil {
		t.Fatalf("the second attempt was not skipped: %v", err)
	}
	raw, _ := os.ReadFile(log)
	if got := strings.Count(string(raw), "quota-axi@"); got != 1 {
		t.Fatalf("npm ran %d times in one day", got)
	}
	// A new day tries again.
	if err := os.WriteFile(RecordPath(), []byte("{\"failed_day\":\"2000-01-01\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(context.Background(), nil); err == nil {
		t.Fatal("a new day did not try again")
	}
	raw, _ = os.ReadFile(log)
	if got := strings.Count(string(raw), "quota-axi@"); got != 2 {
		t.Fatalf("npm ran %d times, want 2", got)
	}
}

func TestRemoveDeletesThePrivateFolder(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	prefix := Prefix()
	if err := os.MkdirAll(filepath.Join(prefix, "node_modules", ".bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Binary(), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	removed, err := Remove()
	if err != nil || !removed {
		t.Fatalf("Remove: %v %v", removed, err)
	}
	if _, err := os.Stat(prefix); !os.IsNotExist(err) {
		t.Fatalf("private folder remained: %v", err)
	}
}

func TestUserCopyWinsOverThePrivateOne(t *testing.T) {
	setupPath(t, "22.19.0")
	if err := Ensure(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	user := filepath.Join(t.TempDir(), "quota-axi")
	if err := os.WriteFile(user, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := Path(user); !ok || got != user {
		t.Fatalf("configured copy lost: %q %v", got, ok)
	}
	t.Setenv("PATH", filepath.Dir(user))
	if got, ok := Path(""); !ok || got != user {
		t.Fatalf("PATH copy lost to the private one: %q %v", got, ok)
	}
}

func TestFindMacOSNVMAndNPMGlobalCopies(t *testing.T) {
	old := platform
	platform = "darwin"
	t.Cleanup(func() { platform = old })

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("AGENT_NET_DATA_DIR", "")
	t.Setenv("PATH", t.TempDir())

	nvm := filepath.Join(home, ".nvm", "versions", "node", "v22.20.0", "bin")
	if err := os.MkdirAll(nvm, 0o755); err != nil {
		t.Fatal(err)
	}
	nvmQuota := filepath.Join(nvm, "quota-axi")
	if err := os.WriteFile(nvmQuota, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeNode(t, nvm, "22.20.0")
	if got, ok := UserPath(""); !ok || got != nvmQuota {
		t.Fatalf("nvm copy: %q %v", got, ok)
	}
	env := ExecEnv(nvmQuota)
	path := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			path = strings.TrimPrefix(kv, "PATH=")
		}
	}
	if !strings.HasPrefix(path, nvm+string(os.PathListSeparator)) {
		t.Fatalf("node folder is not first on PATH: %q", path)
	}

	// The global npm prefix is searched too.
	global := filepath.Join(t.TempDir(), "global")
	if err := os.MkdirAll(filepath.Join(global, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	npmQuota := filepath.Join(global, "bin", "quota-axi")
	if err := os.WriteFile(npmQuota, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	t.Setenv("FAKE_NPM_PREFIX", global)
	fakeNPM(t, bin, filepath.Join(t.TempDir(), "npm.log"))
	t.Setenv("PATH", bin)
	if err := os.Remove(nvmQuota); err != nil {
		t.Fatal(err)
	}
	if got, ok := UserPath(""); !ok || got != npmQuota {
		t.Fatalf("npm global copy: %q %v", got, ok)
	}
}

func TestExecEnvFindsCompatibleNVMNode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	quotaDir := t.TempDir()
	fakeNode(t, quotaDir, "20.0.0")
	nvm := filepath.Join(home, ".nvm", "versions", "node", "v22.19.0", "bin")
	if err := os.MkdirAll(nvm, 0755); err != nil {
		t.Fatal(err)
	}
	fakeNode(t, nvm, "22.19.0")
	env := ExecEnv(filepath.Join(quotaDir, "quota-axi"))
	if !strings.Contains(strings.Join(env, "\n"), "PATH="+nvm+string(os.PathListSeparator)) {
		t.Fatal("compatible Node was not prepended to PATH")
	}
}
