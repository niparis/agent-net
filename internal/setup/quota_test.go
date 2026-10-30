package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
printf '%s\n' "$*" >> "$FAKE_NPM_LOG"
prefix=""
while [ $# -gt 0 ]; do
  if [ "$1" = "--prefix" ]; then shift; prefix="$1"; fi
  shift
done
/bin/mkdir -p "$prefix/node_modules/.bin"
printf '#!/bin/sh\nexit 0\n' > "$prefix/node_modules/.bin/quota-axi"
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
