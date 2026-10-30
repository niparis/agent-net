package setup

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/guidance"
	"github.com/niparis/agent-net/plugins/opencode"
	"github.com/niparis/agent-net/plugins/pi"
)

func TestSystemdUnit(t *testing.T) {
	d := SystemdUnit(Service{Name: "daemon", Args: []string{"daemon"}}, "/home/me/.local/bin/agent-net")
	for _, want := range []string{"ExecStart=/home/me/.local/bin/agent-net daemon\n", "Restart=always", "After=agent-net-gateway.service", "WantedBy=default.target"} {
		if !strings.Contains(d, want) {
			t.Errorf("daemon unit lacks %q:\n%s", want, d)
		}
	}
	if !strings.Contains(d, "KillMode=process") {
		t.Errorf("daemon unit must not kill the workers when it stops:\n%s", d)
	}
	g := SystemdUnit(Service{Name: "gateway", Args: []string{"gateway", "--listen", "0.0.0.0:7420", "--dist", "/a b/dist"}}, "/x/agent-net")
	if !strings.Contains(g, `ExecStart=/x/agent-net gateway --listen 0.0.0.0:7420 --dist "/a b/dist"`+"\n") || !strings.Contains(g, "Restart=on-failure") {
		t.Errorf("gateway unit:\n%s", g)
	}
	if (Service{Name: "daemon"}).UnitName() != "agent-net-daemon.service" {
		t.Error("unit name")
	}
}

func TestLaunchdPlist(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3")
	}
	s := Service{Name: "daemon", Args: []string{"daemon"}}
	plist := LaunchdPlist(s, "/Users/me & co/.local/bin/agent-net", "/Users/me & co")
	script := `import plistlib, sys
d = plistlib.loads(sys.stdin.buffer.read())
assert d["Label"] == "com.agent-net.daemon", d
assert d["ProgramArguments"] == ["/Users/me & co/.local/bin/agent-net", "daemon"], d
assert d["KeepAlive"] is True and d["RunAtLoad"] is True
assert d["StandardErrorPath"] == "/Users/me & co/Library/Logs/agent-net-daemon.log", d
assert d["StandardOutPath"] == d["StandardErrorPath"]
assert d["AbandonProcessGroup"] is True, d
`
	cmd := exec.Command("python3", "-I", "-c", script)
	cmd.Stdin = strings.NewReader(plist)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("plist invalid: %v\n%s\n%s", err, out, plist)
	}
	if s.PlistName() != "com.agent-net.daemon.plist" {
		t.Error("plist name")
	}
}

// fakeClaude puts a `claude` script on PATH that logs its arguments. It
// reports a registered server only after `mcp add` ran.
func fakeClaude(t *testing.T, dir string) (logPath string) {
	t.Helper()
	bin := t.TempDir()
	logPath = filepath.Join(dir, "claude.log")
	script := "#!/bin/sh\necho \"$*\" >> '" + logPath + "'\n" +
		"case \"$1 $2\" in\n" +
		"  'mcp get') [ -f '" + logPath + ".added' ] ;;\n" +
		"  'mcp add') touch '" + logPath + ".added' ;;\n" +
		"  'mcp remove') rm -f '" + logPath + ".added' ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

type fixture struct {
	home, join, data string
	gw               *gateway.Gateway
	srv              *httptest.Server
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("AGENT_NET_CONFIG_DIR", filepath.Join(home, "cfg"))
	t.Setenv("AGENT_NET_NO_SERVICE", "1")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	oldLook := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { lookPath = oldLook })
	noTTY(t)
	data := t.TempDir()
	gw, err := gateway.New(gateway.Options{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(gw.Handler())
	srv.TLS = gw.TLSConfig()
	srv.StartTLS()
	t.Cleanup(func() { srv.Close(); gw.Close() })
	return &fixture{home: home, data: data, gw: gw, srv: srv}
}

func (f *fixture) newJoin(t *testing.T) string {
	code, err := f.gw.Store().CreateEnrollCode(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return f.srv.URL + "#" + code + "#" + f.gw.Fingerprint()
}

func run(t *testing.T, o Options) string {
	t.Helper()
	// Most tests are about one step: answer the one integrations question
	// with none, without a terminal. A per-item flag still wins over this.
	if o.All == "" {
		o.All = AllNo
	}
	return runAskingAll(t, o)
}

// runAsking leaves the one integrations question to the terminal.
func runAsking(t *testing.T, o Options) string {
	t.Helper()
	return runAskingAll(t, o)
}

// runAskingAll asks every question that is not answered by an option.
func runAskingAll(t *testing.T, o Options) string {
	t.Helper()
	var out bytes.Buffer
	o.Out = &out
	o.Exe = "/opt/test/agent-net"
	if err := Setup(context.Background(), o); err != nil {
		t.Fatalf("setup: %v\n%s", err, out.String())
	}
	return norm(out.String())
}

func TestSetupFreshAndRerun(t *testing.T) {
	f := newFixture(t)
	claudeLog := fakeClaude(t, f.home)
	os.MkdirAll(filepath.Join(f.home, ".pi"), 0o755)
	os.MkdirAll(filepath.Join(f.home, ".claude"), 0o755)
	os.WriteFile(filepath.Join(f.home, ".claude", "CLAUDE.md"), []byte("# mine\n"), 0o644)

	out := run(t, Options{Join: f.newJoin(t)})
	for _, want := range []string{"join", "ok (node n_", "daemon service skipped (no service)", "claude code ok", "pi extension ok", "claude guidance updated", "agent guidance ok"} {
		if !strings.Contains(out, want) {
			t.Errorf("first run lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(f.home, "cfg", "node.json")); err != nil {
		t.Fatal("node.json missing")
	}
	ext, _ := os.ReadFile(filepath.Join(f.home, ".pi", "agent", "extensions", "agent-net.ts"))
	if !bytes.Equal(ext, pi.Extension) {
		t.Error("pi extension content")
	}
	cm, _ := os.ReadFile(filepath.Join(f.home, ".claude", "CLAUDE.md"))
	if !strings.HasPrefix(string(cm), "# mine\n\n"+guidance.Begin) {
		t.Errorf("CLAUDE.md:\n%s", cm)
	}
	am, _ := os.ReadFile(filepath.Join(f.home, ".pi", "agent", "AGENTS.md"))
	if string(am) != guidance.Block(guidance.Other) {
		t.Errorf("AGENTS.md:\n%s", am)
	}
	log, _ := os.ReadFile(claudeLog)
	if !strings.Contains(string(log), "mcp add -s user agent-net -- /opt/test/agent-net mcp") {
		t.Errorf("claude log:\n%s", log)
	}

	// Rerun with a fresh code: nothing is joined again, nothing changes.
	nodeBefore, _ := os.ReadFile(filepath.Join(f.home, "cfg", "node.json"))
	out = run(t, Options{Join: f.newJoin(t)})
	for _, want := range []string{"join skipped (already joined)", "claude code skipped (already registered)", "pi extension ok (unchanged)", "claude guidance ok (unchanged)", "agent guidance ok (unchanged)"} {
		if !strings.Contains(out, want) {
			t.Errorf("rerun lacks %q:\n%s", want, out)
		}
	}
	nodeAfter, _ := os.ReadFile(filepath.Join(f.home, "cfg", "node.json"))
	if !bytes.Equal(nodeBefore, nodeAfter) {
		t.Error("node.json changed on rerun")
	}

	// An old extension is updated.
	os.WriteFile(filepath.Join(f.home, ".pi", "agent", "extensions", "agent-net.ts"), []byte("old"), 0o644)
	if out = run(t, Options{Join: f.newJoin(t)}); !strings.Contains(out, "pi extension updated") {
		t.Errorf("expected updated:\n%s", out)
	}
}

func TestSetupSkipsWhatIsMissing(t *testing.T) {
	f := newFixture(t)
	t.Setenv("PATH", t.TempDir())
	out := run(t, Options{Join: f.newJoin(t)})
	for _, want := range []string{"claude code skipped (claude not found)", "pi extension skipped (~/.pi not found)", "skipped (~/.claude not found)", "skipped (~/.pi not found)"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(f.home, ".claude")); err == nil {
		t.Error("created ~/.claude")
	}
}

func TestSetupLeavesPiSymlink(t *testing.T) {
	f := newFixture(t)
	t.Setenv("PATH", t.TempDir())
	extDir := filepath.Join(f.home, ".pi", "agent", "extensions")
	os.MkdirAll(extDir, 0o755)
	target := filepath.Join(f.home, "repo-agent-net.ts")
	os.WriteFile(target, []byte("repo copy"), 0o644)
	os.Symlink(target, filepath.Join(extDir, "agent-net.ts"))
	out := run(t, Options{Join: f.newJoin(t)})
	if !strings.Contains(out, "pi extension skipped (symlink, left as is)") {
		t.Errorf("output:\n%s", out)
	}
	if b, _ := os.ReadFile(target); string(b) != "repo copy" {
		t.Error("symlink target was written")
	}
	// Uninstall leaves it too.
	var buf bytes.Buffer
	if err := Uninstall(Options{Out: &buf, Exe: "/x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(extDir, "agent-net.ts")); err != nil {
		t.Error("uninstall removed the symlink")
	}
}

func TestSetupGatewayJoinsItself(t *testing.T) {
	f := newFixture(t)
	t.Setenv("PATH", t.TempDir())
	dist := filepath.Join(f.home, "dist")
	exe := filepath.Join(f.home, "agent-net-fake")
	os.WriteFile(exe, []byte("fake binary"), 0o755)
	port := f.srv.Listener.Addr().String()
	port = port[strings.LastIndex(port, ":")+1:]
	opts := Options{Gateway: true, Data: f.data, Dist: dist, Port: port}
	var out bytes.Buffer
	opts.Out, opts.Exe = &out, exe
	if err := Setup(context.Background(), opts); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	got := norm(out.String())
	for _, want := range []string{"gateway service skipped (no service)", "gateway binaries ok\n", "join ok (node n_"} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dist, "agent-net-"+runtime.GOOS+"-"+runtime.GOARCH)); string(b) != "fake binary" {
		t.Error("dist binary not copied")
	}
	// Rerun: joined already, dist unchanged.
	out.Reset()
	if err := Setup(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	got = norm(out.String())
	for _, want := range []string{"gateway binaries ok (unchanged)", "join skipped (already joined)"} {
		if !strings.Contains(got, want) {
			t.Errorf("rerun lacks %q:\n%s", want, got)
		}
	}
}

func TestSetupWithoutJoinNeedsCredentials(t *testing.T) {
	newFixture(t)
	t.Setenv("PATH", t.TempDir())
	var out bytes.Buffer
	if err := Setup(context.Background(), Options{Out: &out, Exe: "/x"}); err == nil || !strings.Contains(out.String(), "not joined yet") {
		t.Fatalf("expected a not-joined error, got %v\n%s", err, out.String())
	}
}

func TestUninstall(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	os.MkdirAll(filepath.Join(f.home, ".pi"), 0o755)
	os.MkdirAll(filepath.Join(f.home, ".claude"), 0o755)
	orig := "# mine\n\n- rule\n"
	os.WriteFile(filepath.Join(f.home, ".claude", "CLAUDE.md"), []byte(orig), 0o644)
	run(t, Options{Join: f.newJoin(t)})

	var out bytes.Buffer
	if err := Uninstall(Options{Out: &out, Exe: "/x", Purge: true}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if b, _ := os.ReadFile(filepath.Join(f.home, ".claude", "CLAUDE.md")); string(b) != orig {
		t.Errorf("CLAUDE.md not restored:\n%q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(f.home, ".pi", "agent", "AGENTS.md")); len(b) != 0 {
		t.Errorf("AGENTS.md not empty: %q", b)
	}
	if _, err := os.Stat(filepath.Join(f.home, ".pi", "agent", "extensions", "agent-net.ts")); err == nil {
		t.Error("extension still there")
	}
	if _, err := os.Stat(filepath.Join(f.home, "cfg")); err == nil {
		t.Error("--purge kept the config dir")
	}
	if !strings.Contains(norm(out.String()), "claude code ok (removed)") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestOpenCodeInstallAndUninstall(t *testing.T) {
	f := newFixture(t)
	t.Setenv("PATH", t.TempDir())
	ocDir := filepath.Join(f.home, ".config", "opencode")
	os.MkdirAll(ocDir, 0o755)
	orig := "my own instructions\n"
	os.WriteFile(filepath.Join(ocDir, "AGENTS.md"), []byte(orig), 0o644)

	out := run(t, Options{Join: f.newJoin(t)})
	for _, want := range []string{"opencode plugin ok", "opencode guidance updated", "restart its service"} {
		if !strings.Contains(out, want) {
			t.Errorf("first run lacks %q:\n%s", want, out)
		}
	}
	plugin, _ := os.ReadFile(filepath.Join(ocDir, "plugins", "agent-net.ts"))
	if !bytes.Equal(plugin, opencode.Plugin) {
		t.Error("plugin content")
	}
	ag, _ := os.ReadFile(filepath.Join(ocDir, "AGENTS.md"))
	if !strings.HasPrefix(string(ag), orig+"\n"+guidance.Begin) {
		t.Errorf("AGENTS.md:\n%s", ag)
	}

	// A rerun changes nothing and does not repeat the restart hint.
	out = run(t, Options{Join: f.newJoin(t)})
	for _, want := range []string{"opencode plugin ok (unchanged)", "opencode guidance ok (unchanged)"} {
		if !strings.Contains(out, want) {
			t.Errorf("rerun lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "restart its service") {
		t.Errorf("hint repeated:\n%s", out)
	}

	var buf bytes.Buffer
	if err := Uninstall(Options{Out: &buf, Exe: "/x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ocDir, "plugins", "agent-net.ts")); err == nil {
		t.Error("plugin still there")
	}
	if b, _ := os.ReadFile(filepath.Join(ocDir, "AGENTS.md")); string(b) != orig {
		t.Errorf("AGENTS.md not restored:\n%q", b)
	}
}

// norm collapses the column padding of the step lines.
func norm(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.Join(strings.Fields(l), " ")
	}
	return strings.Join(lines, "\n")
}

func TestInviteLine(t *testing.T) {
	got := InviteLine("10.0.0.5", "7420", "ABCD2345", "sha256//xyz=")
	want := "curl -fsSL -k --pinnedpubkey 'sha256//xyz=' 'https://10.0.0.5:7420/install/ABCD2345' | sh"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if !strings.Contains(InviteLine("::1", "7420", "A", "p"), "https://[::1]:7420/") {
		t.Error("IPv6 host not bracketed")
	}
	if isTailscale("192.168.1.2") || !isTailscale("100.64.0.10") || isTailscale("100.128.0.1") {
		t.Error("tailscale range check")
	}
}

func TestEnsureWorkersSurviveUnitFixesAnOldUnit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-net-daemon.service")
	old := "[Unit]\nDescription=agent-net daemon\n\n[Service]\nExecStart=/x/agent-net daemon\nRestart=always\n\n[Install]\nWantedBy=default.target\n"
	os.WriteFile(path, []byte(old), 0o644)
	changed, err := EnsureWorkersSurviveUnit(path)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "[Service]\nKillMode=process\nExecStart=/x/agent-net daemon\n") {
		t.Errorf("unit:\n%s", b)
	}
	if changed, _ := EnsureWorkersSurviveUnit(path); changed {
		t.Error("second run changed the file again")
	}
	// The unit that setup writes now is already right.
	os.WriteFile(path, []byte(SystemdUnit(Service{Name: "daemon", Args: []string{"daemon"}}, "/x/agent-net")), 0o644)
	if changed, _ := EnsureWorkersSurviveUnit(path); changed {
		t.Error("changed a unit that already has KillMode")
	}
	if changed, err := EnsureWorkersSurviveUnit(path + ".none"); changed || err != nil {
		t.Errorf("missing file: changed=%v err=%v", changed, err)
	}
}

func TestUninstallStopsTheWorkersThatKeepRunning(t *testing.T) {
	f := newFixture(t)
	state := filepath.Join(f.home, "state")
	t.Setenv("AGENT_NET_STATE_DIR", state)
	var gotDir string
	old := stopWorkers
	stopWorkers = func(dir string, _ time.Duration) (int, error) { gotDir = dir; return 2, nil }
	t.Cleanup(func() { stopWorkers = old })
	var out bytes.Buffer
	if err := Uninstall(Options{Out: &out, Exe: "/x"}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if gotDir != state {
		t.Errorf("workers stopped in %q, want %q", gotDir, state)
	}
	if !strings.Contains(norm(out.String()), "workers ok (stopped 2)") {
		t.Errorf("output:\n%s", out.String())
	}
}
