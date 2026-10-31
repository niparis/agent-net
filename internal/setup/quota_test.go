package setup

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/daemon"
	"github.com/niparis/agent-net/internal/integrations"
	"github.com/niparis/agent-net/internal/quotaaxi"
)

func fakeNode(t *testing.T, dir, version string) {
	t.Helper()
	body := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo v" + version + "; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "node"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func fakeNPM(t *testing.T, dir, logPath string) {
	t.Helper()
	body := `#!/bin/sh
if [ "$1" = "prefix" ]; then exit 0; fi
printf '%s\n' "$*" >> "$FAKE_NPM_LOG"
prefix=""
while [ $# -gt 0 ]; do
  if [ "$1" = "--prefix" ]; then shift; prefix="$1"; fi
  shift
done
/bin/mkdir -p "$prefix/node_modules/.bin"
printf '#!/bin/sh\necho 0.1.69\n' > "$prefix/node_modules/.bin/quota-axi"
/bin/chmod 755 "$prefix/node_modules/.bin/quota-axi"
`
	if err := os.WriteFile(filepath.Join(dir, "npm"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_NPM_LOG", logPath)
}

func quotaPath(t *testing.T, version string) (bin, log string) {
	t.Helper()
	bin = t.TempDir()
	t.Setenv("XDG_DATA_HOME", "")
	fakeNode(t, bin, version)
	log = filepath.Join(t.TempDir(), "npm.log")
	fakeNPM(t, bin, log)
	t.Setenv("PATH", bin)
	shell := filepath.Join(t.TempDir(), "shell")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\nprintf '%s\\0' '"+bin+"' '' '' '' '' '' '' '' ''\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	return bin, log
}

func TestSetupInstallsQuotaAXIWhenOn(t *testing.T) {
	f := newFixture(t)
	ensureQuotaAXI = quotaaxi.Ensure
	_, log := quotaPath(t, "22.19.0")
	out := runAskingAll(t, Options{Join: f.newJoin(t), All: AllYes})
	if !strings.Contains(out, "quota-axi ok (installed "+quotaaxi.Version+")") {
		t.Errorf("output:\n%s", out)
	}
	if st, err := os.Stat(quotaaxi.Binary()); err != nil || st.IsDir() {
		t.Fatalf("private quota-axi missing: %v", err)
	}
	if _, err := os.Stat(log); err != nil {
		t.Fatalf("npm was not called: %v", err)
	}
}

func TestSetupReportsNodeTooOld(t *testing.T) {
	f := newFixture(t)
	ensureQuotaAXI = quotaaxi.Ensure
	quotaPath(t, "22.18.0")
	out := runAskingAll(t, Options{Join: f.newJoin(t), All: AllYes})
	if !strings.Contains(out, quotaaxi.NeedNodeMessage) {
		t.Errorf("output lacks the Node line:\n%s", out)
	}
	if _, err := os.Stat(quotaaxi.Binary()); !os.IsNotExist(err) {
		t.Fatalf("a private copy was installed with an old Node: %v", err)
	}
}

func TestSetupTurningQuotaOffRemovesThePrivateCopy(t *testing.T) {
	f := newFixture(t)
	removeQuotaAXI = quotaaxi.Remove
	if err := os.MkdirAll(filepath.Dir(quotaaxi.Binary()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(quotaaxi.Binary(), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := runAskingAll(t, Options{Join: f.newJoin(t), All: AllNo})
	if !strings.Contains(out, "quota-axi off (removed the private copy)") {
		t.Errorf("output:\n%s", out)
	}
	if _, err := os.Stat(quotaaxi.Prefix()); !os.IsNotExist(err) {
		t.Fatalf("private folder remained: %v", err)
	}
}

func TestUninstallRemovesThePrivateCopy(t *testing.T) {
	f := newFixture(t)
	removeQuotaAXI = quotaaxi.Remove
	t.Setenv("AGENT_NET_STATE_DIR", filepath.Join(f.home, "state"))
	oldStop := stopWorkers
	stopWorkers = func(string, time.Duration) (int, error) { return 0, nil }
	t.Cleanup(func() { stopWorkers = oldStop })
	if err := os.MkdirAll(filepath.Dir(quotaaxi.Binary()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(quotaaxi.Binary(), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Uninstall(Options{Out: &out, NoService: true, Exe: "/x"}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(norm(out.String()), "quota-axi ok (removed)") {
		t.Errorf("output:\n%s", out.String())
	}
	if _, err := os.Stat(quotaaxi.Prefix()); !os.IsNotExist(err) {
		t.Fatalf("private folder remained: %v", err)
	}
}

func TestApplyIntegrationsInstallsAndRemovesInTheBackground(t *testing.T) {
	f := newFixture(t)
	var started, removed int
	ensureQuotaAXIAsync = func(func(string, ...any)) { started++ }
	removeQuotaAXI = func() (bool, error) { removed++; return false, nil }
	if err := ApplyIntegrations(integrations.AllOn(), nil); err != nil {
		t.Fatal(err)
	}
	if started != 1 {
		t.Fatalf("install starts: %d", started)
	}
	if err := ApplyIntegrations(integrations.AllOff(), nil); err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removals: %d", removed)
	}
	_ = f
}

func TestQuotaSetupRecordsOwnerCopy(t *testing.T) {
	f := newFixture(t)
	ensureQuotaAXI = quotaaxi.Ensure
	bin, log := quotaPath(t, "22.19.0")
	shell := filepath.Join(f.home, "shell")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\nprintf '%s\\0' '"+bin+"' '' '' '' '' '' '' '' ''\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	owner := filepath.Join(bin, "quota-axi")
	if err := os.WriteFile(owner, []byte("#!/bin/sh\necho 0.1.66\n"), 0755); err != nil {
		t.Fatal(err)
	}
	out := runAskingAll(t, Options{Join: f.newJoin(t), All: AllYes})
	choice, err := quotaaxi.LoadChoice(daemon.ConfigDir())
	if err != nil || choice.Path != owner || choice.Version != "0.1.66" {
		t.Fatalf("choice %+v %v; output %s", choice, err, out)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("npm ran: %v", err)
	}
}

func TestUninstallRemovesQuotaChoice(t *testing.T) {
	f := newFixture(t)
	if _, err := quotaaxi.SaveChoice(daemon.ConfigDir(), []quotaaxi.Copy{{Path: "/fake/quota-axi", Version: quotaaxi.Version, Runs: true}}); err != nil {
		t.Fatal(err)
	}
	oldStop := stopWorkers
	stopWorkers = func(string, time.Duration) (int, error) { return 0, nil }
	t.Cleanup(func() { stopWorkers = oldStop })
	var out bytes.Buffer
	if err := Uninstall(Options{Out: &out, NoService: true, Exe: "/x"}); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if _, err := quotaaxi.LoadChoice(daemon.ConfigDir()); !os.IsNotExist(err) {
		t.Fatalf("choice remains: %v", err)
	}
	_ = f
}

func TestQuotaUpgradeCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cases := []struct{ path, command string }{
		{"/opt/homebrew/bin/quota-axi", "brew upgrade quota-axi"},
		{filepath.Join(home, ".nvm/versions/node/v22.19.0/bin/quota-axi"), "npm i -g quota-axi"},
		{filepath.Join(home, ".local/bin/quota-axi"), "quota-axi update"},
	}
	for _, c := range cases {
		if got := strings.Join(quotaUpgradeCommand(c.path), " "); got != c.command {
			t.Errorf("%s: %s", c.path, got)
		}
	}
}

func TestQuotaUpgradeRequiresConsent(t *testing.T) {
	dir, _ := quotaPath(t, "22.19.0")
	script := filepath.Join(dir, "quota-axi")
	log := filepath.Join(dir, "updates")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> '"+log+"'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	copy := quotaaxi.Copy{Path: script, Version: "0.1.65", Runs: true}
	var out bytes.Buffer
	old := openTTY
	t.Cleanup(func() { openTTY = old })
	openTTY = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("n\n")), nil }
	r := &runner{Options: Options{Out: &out}}
	r.offerQuotaUpgrade(context.Background(), []quotaaxi.Copy{copy})
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("declined upgrade ran: %v", err)
	}
	openTTY = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("y\n")), nil }
	r.offerQuotaUpgrade(context.Background(), []quotaaxi.Copy{copy})
	b, err := os.ReadFile(log)
	if err != nil || strings.TrimSpace(string(b)) != "update" {
		t.Fatalf("upgrade: %q %v", b, err)
	}
}
