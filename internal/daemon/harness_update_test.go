package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/harnesses"
	"github.com/niparis/agent-net/internal/integrations"
	"github.com/niparis/agent-net/internal/protocol"
)

func updateFixture(t *testing.T, scripts map[string]string) (*Daemon, string) {
	t.Helper()
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0755); err != nil {
			t.Fatal(err)
		}
	}
	opts := &harnesses.Options{PATH: dir + ":/bin:/usr/bin", Dirs: []string{}, LoginShellPATH: func(context.Context) string { return "" }}
	log := log.New(&logBuf{}, "", 0)
	d := &Daemon{cfg: Config{HarnessSearch: opts, HarnessUpdateInitialDelay: 10 * time.Millisecond, HarnessUpdateInterval: time.Hour, HarnessUpdateRetryDelay: 30 * time.Millisecond, HarnessUpdateTimeout: 200 * time.Millisecond, DetectHarnesses: func(ctx context.Context) []protocol.HarnessInfo { return harnesses.Detect(ctx, *opts) }}, log: log, sessions: map[string]*localSession{}, workers: map[string]*worker{}}
	d.refreshHarnessInventory(context.Background())
	return d, dir
}

func startHarnessUpdateTest(t *testing.T, d *Daemon) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); d.harnessUpdateLoop(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
}

func updateResult(d *Daemon, name string) *protocol.HarnessUpdate {
	hs, _ := d.inventory()
	for _, h := range hs {
		if h.Name == name {
			return h.Update
		}
	}
	return nil
}

func TestHarnessUpdateRunsAndReportsVersionAndStdin(t *testing.T) {
	d, _ := updateFixture(t, map[string]string{"claude": `case "$1" in
 --version) if [ -f "$(dirname "$0")/new" ]; then echo 2; else echo 1; fi ;;
 update) if read x; then echo stdin-was-open >&2; exit 1; fi; touch "$(dirname "$0")/new" ;;
 esac
`})
	startHarnessUpdateTest(t, d)
	eventually(t, "updated version", func() bool { u := updateResult(d, "claude"); return u != nil && u.Result == "updated" })
	u := updateResult(d, "claude")
	if u.Before != "1" || u.After != "2" || u.CheckedAt.IsZero() {
		t.Fatalf("update: %+v", u)
	}
	hs, _ := d.inventory()
	if hs[0].Version != "2" {
		t.Fatalf("inventory: %+v", hs)
	}
}

func TestHarnessUpdateBusyRetry(t *testing.T) {
	d, _ := updateFixture(t, map[string]string{"pi": `case "$1" in --version) echo 1 ;; update) echo ran > "$(dirname "$0")/ran" ;; esac`})
	d.mu.Lock()
	d.sessions["pi/s"] = &localSession{info: protocol.SessionInfo{Harness: "pi", Status: "busy"}}
	d.mu.Unlock()
	startHarnessUpdateTest(t, d)
	eventually(t, "busy result", func() bool { u := updateResult(d, "pi"); return u != nil && u.Result == "skipped busy" })
	d.mu.Lock()
	d.sessions["pi/s"].info.Status = "idle"
	d.mu.Unlock()
	eventually(t, "retry", func() bool { u := updateResult(d, "pi"); return u != nil && u.Result == "already current" })
}

func TestHarnessUpdateFailureDoesNotStopOthers(t *testing.T) {
	d, _ := updateFixture(t, map[string]string{
		"claude": `case "$1" in --version) echo 1 ;; update) echo first; echo 'last failure' >&2; exit 1 ;; esac`,
		"pi":     `case "$1" in --version) echo 1 ;; update) exit 0 ;; esac`,
	})
	startHarnessUpdateTest(t, d)
	eventually(t, "other update", func() bool { u := updateResult(d, "pi"); return u != nil && u.Result == "already current" })
	u := updateResult(d, "claude")
	if u == nil || u.Result != "failed" || u.Detail != "last failure" {
		t.Fatalf("failure: %+v", u)
	}
}

func TestHarnessUpdateTimeout(t *testing.T) {
	d, _ := updateFixture(t, map[string]string{"codex": `case "$1" in --version) echo 1 ;; update) exec sleep 1 ;; esac`})
	d.cfg.HarnessUpdateTimeout = 40 * time.Millisecond
	startHarnessUpdateTest(t, d)
	eventually(t, "timeout", func() bool { u := updateResult(d, "codex"); return u != nil && u.Result == "failed" })
	if u := updateResult(d, "codex"); !strings.Contains(u.Detail, "timed out") {
		t.Fatalf("timeout: %+v", u)
	}
	if _, err := os.Stat("fake-codex.json"); !os.IsNotExist(err) {
		t.Fatalf("test left fake-codex.json in package folder: %v", err)
	}
}

func TestHarnessUpdateSettingOff(t *testing.T) {
	d, dir := updateFixture(t, map[string]string{"claude": `case "$1" in --version) echo 1 ;; update) touch "$(dirname "$0")/ran" ;; esac`})
	if err := integrations.SaveApplied(ConfigDir(), protocol.Integrations{HarnessUpdatesOff: true}); err != nil {
		t.Fatal(err)
	}
	startHarnessUpdateTest(t, d)
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(dir, "ran")); !os.IsNotExist(err) {
		t.Fatalf("update ran with setting off: %v", err)
	}
	if u := updateResult(d, "claude"); u != nil {
		t.Fatalf("unexpected result: %+v", u)
	}
}

func TestHarnessUpdateAgyIsOneNormalEntryAndWorkerBusy(t *testing.T) {
	d, _ := updateFixture(t, map[string]string{"agy": `case "$1" in
--version) echo 1 ;;
models) printf 'Fetching available models...\ngemini-3.8-flash-low\tGemini 3.8 Flash (low)\n' ;;
update) exit 0 ;;
esac`})
	hs, _ := d.inventory()
	if len(hs) != 1 || hs[0].Name != "agy" || hs[0].Version != "1" || len(hs[0].Models) != 1 || hs[0].SignedIn != "yes" {
		t.Fatalf("agy is one normal harness entry: %+v", hs)
	}
	startHarnessUpdateTest(t, d)
	eventually(t, "agy update", func() bool { u := updateResult(d, "agy"); return u != nil && u.Result == "already current" })
	if hs, _ := d.inventory(); len(hs) != 1 || hs[0].Name != "agy" {
		t.Fatalf("the update loop added a second agy entry: %+v", hs)
	}
	d.wmu.Lock()
	d.workers["w"] = &worker{harness: "codex", turnActive: true}
	d.wmu.Unlock()
	if !d.harnessBusy("codex") {
		t.Fatal("active worker missed")
	}
	d.wmu.Lock()
	d.workers["w"].turnActive = false
	d.workers["w"].turnHandbackIdle = true
	d.wmu.Unlock()
	if d.harnessBusy("codex") {
		t.Fatal("idle worker considered busy")
	}
}

func TestHarnessUpdateCommandsAndSearchPATH(t *testing.T) {
	script := `case "$1" in --version) echo 1 ;; update|upgrade) update-helper "$@" > "$(dirname "$0")/args-$(basename "$0")" ;; esac`
	d, dir := updateFixture(t, map[string]string{"claude": script, "codex": script, "opencode": script, "pi": script, "agy": script, "update-helper": "printf '%s' \"$*\""})
	startHarnessUpdateTest(t, d)
	for name, want := range map[string]string{"claude": "update", "codex": "update", "opencode": "upgrade", "pi": "update self", "agy": "update"} {
		name, want := name, want
		eventually(t, "command for "+name, func() bool {
			b, err := os.ReadFile(filepath.Join(dir, "args-"+name))
			return err == nil && string(b) == want
		})
	}
	if _, err := os.Stat("fake-codex.json"); !os.IsNotExist(err) {
		t.Fatalf("test left fake-codex.json in package folder: %v", err)
	}
}

func TestHarnessUpdateBrewManualWhenUnsupported(t *testing.T) {
	d, dir := updateFixture(t, map[string]string{"opencode": `case "$1" in --version) echo 1 ;; upgrade) echo 'installation method not found'; exit 1 ;; esac`})
	cellar := filepath.Join(dir, "Cellar", "opencode", "1", "bin")
	if err := os.MkdirAll(cellar, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "opencode"), filepath.Join(cellar, "opencode")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(cellar, "opencode"), filepath.Join(dir, "opencode")); err != nil {
		t.Fatal(err)
	}
	startHarnessUpdateTest(t, d)
	eventually(t, "manual brew", func() bool { u := updateResult(d, "opencode"); return u != nil && u.Result == "update by hand" })
	if u := updateResult(d, "opencode"); u.Detail != "brew upgrade opencode" {
		t.Fatalf("manual update: %+v", u)
	}
}

func TestHarnessUpdateNetworkSettingsPropagation(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	if err := g.gw.Store().SetIntegrations(protocol.Integrations{}); err != nil {
		t.Fatal(err)
	}
	n := startNode(t, g, nodeOpts{detect: func(context.Context) []protocol.HarnessInfo { return nil }, cfg: func(c *Config) {
		c.IntegrationsInterval = 20 * time.Millisecond
		// Even an unrelated integration failing must not prevent disabling updates.
		c.ApplyIntegrations = func(protocol.Integrations, func(string, ...any)) error { return fmt.Errorf("fixture apply failure") }
	}})
	eventually(t, "initial network update setting", func() bool {
		n.d.intgMu.Lock()
		defer n.d.intgMu.Unlock()
		return n.d.updateSettingKnown && !n.d.updateSettingOff
	})
	cli := cliClient(t, n)
	cli.mustCall(protocol.OpIntegrationsSet, protocol.Integrations{HarnessUpdatesOff: true})
	eventually(t, "network update setting off", func() bool { return !n.d.harnessUpdatesAllowed() })
	stored, ok, err := g.gw.Store().Integrations()
	if err != nil || !ok || !stored.HarnessUpdatesOff {
		t.Fatalf("network setting: %+v %v %v", stored, ok, err)
	}
}

func TestHarnessUpdateRefreshesModelsAndSignIn(t *testing.T) {
	d, _ := updateFixture(t, map[string]string{
		"claude": `case "$1" in
 --version) echo 1 ;;
 auth) if [ -f "$(dirname "$0")/claude-new" ]; then echo '{"loggedIn":true,"authMethod":"oauth"}'; else echo '{"loggedIn":false}'; fi ;;
 update) touch "$(dirname "$0")/claude-new" ;;
 esac`,
		"opencode": `case "$1" in
 --version) echo 1 ;;
 models) if [ -f "$(dirname "$0")/opencode-new" ]; then echo example/new; else echo example/old; fi ;;
 upgrade) touch "$(dirname "$0")/opencode-new" ;;
 esac`,
	})
	startHarnessUpdateTest(t, d)
	eventually(t, "refreshed models and sign-in", func() bool {
		hs, _ := d.inventory()
		signed, models := false, false
		for _, h := range hs {
			if h.Name == "claude" {
				signed = h.SignedIn == "yes" && h.SignIn == "oauth"
			}
			if h.Name == "opencode" {
				models = len(h.Models) == 1 && h.Models[0] == "example/new"
			}
		}
		return signed && models
	})
}

func TestHarnessUpdateInventoryStillRetriesFailedModelProbe(t *testing.T) {
	d, _ := updateFixture(t, map[string]string{"pi": `case "$1" in --version) echo 1 ;; --list-models) echo 'provider/model';; esac`})
	d.hmu.Lock()
	d.installed[0].Models = []string{"provider/model"}
	d.hmu.Unlock()
	d.cfg.DetectHarnesses = func(context.Context) []protocol.HarnessInfo {
		return []protocol.HarnessInfo{{Name: "pi", Path: d.installed[0].Path, Version: "1"}}
	}
	hs, incomplete := d.refreshHarnessInventory(context.Background())
	if !incomplete || len(hs[0].Models) != 1 {
		t.Fatalf("must retain models and retry: incomplete=%v inventory=%+v", incomplete, hs)
	}
}

func TestHarnessUpdateAgyFoundEvenWhenVersionProbeFails(t *testing.T) {
	d, dir := updateFixture(t, map[string]string{"agy": `case "$1" in --version) exit 1 ;; update) touch "$(dirname "$0")/ran" ;; esac`})
	opts := *d.cfg.HarnessSearch
	if got := harnesses.Detect(context.Background(), opts); len(got) != 1 || got[0].Name != "agy" {
		t.Fatalf("executable must be detected even when its version probe fails: %+v", got)
	}
	startHarnessUpdateTest(t, d)
	eventually(t, "agy update despite failed version", func() bool { _, err := os.Stat(filepath.Join(dir, "ran")); return err == nil })
}
