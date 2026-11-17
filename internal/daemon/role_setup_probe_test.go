package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

func setupRoleProbeFixture(t *testing.T) (*workerNode, *testGateway) {
	t.Helper()
	g := startGateway(t, t.TempDir(), "")
	bin := filepath.Join(t.TempDir(), "codex")
	script := `#!/bin/sh
while [ "$#" -gt 0 ]; do if [ "$1" = --output-last-message ]; then shift; printf OK > "$1"; fi; shift; done
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	detect := func(context.Context) []protocol.HarnessInfo {
		return []protocol.HarnessInfo{{Name: "codex", Path: bin, Models: []string{"fake-model"}}}
	}
	n := startWorkerNodeWithDetect(t, g, shortSet, detect, func(c *Config) {
		c.HarnessPaths = map[string]string{"codex": bin}
		c.ListModels = func(context.Context, string) ([]string, error) { return []string{"fake-model"}, nil }
	})
	eventually(t, "codex inventory", func() bool { list, _ := n.d.inventory(); return len(list) > 0 })
	return n, g
}

func TestRoleSetupWritesOnlyUnsetLists(t *testing.T) {
	n, g := setupRoleProbeFixture(t)
	setChiefModels(t, g, "claude · anthropic · owner-model")
	result, err := n.d.setupRoleModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Models) != 1 || result.Models[0].Harness != "codex" {
		t.Fatalf("setup result = %+v", result)
	}
	snap, err := g.gw.Store().SettingsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, value := range snap.Values {
		if value.Level == "network" && value.Scope == "network" {
			var text string
			if json.Unmarshal(value.Value, &text) == nil {
				values[value.Key] = text
			}
		}
	}
	if values["chief.model"] != "claude · anthropic · owner-model" || !strings.Contains(values["cto.models"], "codex") || !strings.Contains(values["merger.models"], "codex") {
		t.Fatalf("settings = %v", values)
	}
}

func TestRoleSetupIsOwnerOnly(t *testing.T) {
	n, _ := setupRoleProbeFixture(t)
	peer, _ := helloPi(t, n.node, "role-peer", t.TempDir(), true)
	if _, err := peer.call(protocol.OpRoleSetup, nil); err == nil {
		t.Fatal("an agent called the owner-only role setup")
	}
	var result protocol.RoleSetupResult
	mustJSON(t, n.cli.mustCall(protocol.OpRoleSetup, nil), &result)
	if len(result.Models) != 1 || result.Models[0].Harness != "codex" {
		t.Fatalf("owner role setup = %+v", result)
	}
}

func TestRoleSetupStartsCTOAndChief(t *testing.T) {
	t.Setenv(envFakeCodex, "1")
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	detect := func(context.Context) []protocol.HarnessInfo {
		return []protocol.HarnessInfo{{Name: "codex", Path: os.Args[0], Models: []string{"gpt-fake"}, SignedIn: "yes"}}
	}
	n := startWorkerNodeWithDetect(t, g, shortSet, detect, func(c *Config) {
		c.HarnessPaths = map[string]string{"codex": os.Args[0]}
		c.CodexHome = f.home
		c.CodexInterval = 20 * time.Millisecond
		c.ListModels = func(context.Context, string) ([]string, error) { return []string{"gpt-fake"}, nil }
	})
	t.Setenv("HOME", filepath.Dir(n.d.cfg.StateDir))
	models := []protocol.RoleModel{{Harness: "codex", Provider: "openai", Model: "gpt-fake"}}
	if _, err := n.d.ctoSettings(context.Background(), protocol.CTOSettingsRequest{Models: &models}); err != nil {
		t.Fatal(err)
	}
	setChiefModels(t, g, protocol.FormatRoleModels(models))
	if err := n.d.startSetupRoles(context.Background(), []string{"cto", "chief"}); err != nil {
		t.Fatal(err)
	}
	cto := readCodex(t, codexCTODir(n.node), func(r fakeCodexRecord) bool { return len(r.Turns) > 0 })
	if len(cto.Turns) == 0 {
		t.Fatal("CTO got no first turn")
	}
	chief, err := g.gw.Store().ChiefStatus()
	if err != nil || chief.SessionID == "" {
		t.Fatalf("chief = %+v, %v", chief, err)
	}
	eventually(t, "chief first message queued", func() bool {
		for _, q := range f.queue() {
			if strings.Contains(q.Text, chiefFirstMessage) {
				return true
			}
		}
		return false
	})
}

func TestRoleSetupKeepsExistingLocalCTOChoice(t *testing.T) {
	n, _ := setupRoleProbeFixture(t)
	if _, err := n.d.st.db.Exec(`UPDATE cto SET active_harness='claude', active_model='owner-model', models='claude · anthropic · owner-model', models_migrated=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := n.d.setupRoleModels(context.Background()); err != nil {
		t.Fatal(err)
	}
	var harness, model, models string
	if err := n.d.st.db.QueryRow(`SELECT active_harness, active_model, models FROM cto WHERE id=1`).Scan(&harness, &model, &models); err != nil {
		t.Fatal(err)
	}
	if harness != "claude" || model != "owner-model" || !strings.Contains(models, "owner-model") {
		t.Fatalf("local CTO = %q %q %q, want the existing choice", harness, model, models)
	}
}

func roleProbeHarness(t *testing.T, name, script string) *Daemon {
	t.Helper()
	bin := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Daemon{cfg: Config{HarnessPaths: map[string]string{name: bin}}}
}

func TestRoleSetupOnlyWritesUnsetLists(t *testing.T) {
	snapshot := settings.Snapshot{Values: []settings.Value{{Key: "chief.model", Level: "network", Scope: "network", Value: []byte(`"owner choice"`)}}}
	models := []protocol.RoleModel{{Harness: "codex", Provider: "openai", Model: "fake"}}
	changes := setupRoleChanges(snapshot, models)
	if len(changes) != 2 || changes[0].Key != "cto.models" || changes[1].Key != "merger.models" {
		t.Fatalf("changes = %+v, want only unset lists", changes)
	}
	for _, change := range changes {
		if change.Expect != nil || change.Level != "network" || change.Scope != "network" || !strings.Contains(string(change.Value), "codex") {
			t.Fatalf("change = %+v", change)
		}
	}
	if got := setupRoleChanges(snapshot, nil); len(got) != 0 {
		t.Fatalf("no usable models produced changes: %+v", got)
	}
}

func TestRoleSetupProbeRequiresModelAnswer(t *testing.T) {
	for _, harness := range []string{"codex", "opencode", "claude"} {
		t.Run(harness, func(t *testing.T) {
			d := roleProbeHarness(t, harness, "echo 'a diagnostic, not an answer'\nexit 0")
			got := d.probeSetupRoleModel(context.Background(), protocol.RoleModel{Harness: harness, Model: "fake/model"})
			if got.OK {
				t.Fatalf("diagnostic accepted as an answer: %+v", got)
			}
		})
	}
}

func TestRoleSetupProbeAcceptsModelAnswers(t *testing.T) {
	for _, tc := range []struct{ harness, script string }{
		{"codex", `while [ "$#" -gt 0 ]; do if [ "$1" = --output-last-message ]; then shift; printf 'OK' > "$1"; fi; shift; done`},
		{"opencode", `printf '%s\n' '{"type":"text","part":{"type":"text","text":"OK"}}'`},
		{"claude", `printf '%s\n' '{"type":"result","is_error":false,"result":"OK"}'`},
	} {
		t.Run(tc.harness, func(t *testing.T) {
			d := roleProbeHarness(t, tc.harness, tc.script)
			got := d.probeSetupRoleModel(context.Background(), protocol.RoleModel{Harness: tc.harness, Model: "fake/model"})
			if !got.OK {
				t.Fatalf("answer not accepted: %+v", got)
			}
		})
	}
}

func TestRoleSetupProbePreservesFailureReason(t *testing.T) {
	d := roleProbeHarness(t, "codex", `echo '429 quota exhausted' >&2; exit 1`)
	got := d.probeSetupRoleModel(context.Background(), protocol.RoleModel{Harness: "codex", Model: "fake"})
	if got.OK || !strings.Contains(got.Output, "429 quota exhausted") {
		t.Fatalf("probe = %+v, want the failure reason", got)
	}
}

func TestRoleSetupProbeFallsThroughInOrder(t *testing.T) {
	installed := []protocol.HarnessInfo{{Name: "pi"}, {Name: "claude"}, {Name: "opencode"}, {Name: "codex"}}
	var tested []string
	models := func(_ context.Context, name string) ([]string, error) {
		return map[string][]string{"codex": {"codex-model"}, "opencode": {"fake/model"}, "claude": {"claude-model"}}[name], nil
	}
	probe := func(_ context.Context, model protocol.RoleModel) protocol.ModelTestResult {
		tested = append(tested, model.Harness)
		if model.Harness == "codex" {
			return protocol.ModelTestResult{Output: "429 quota exhausted"}
		}
		return protocol.ModelTestResult{OK: true, Output: "OK"}
	}
	got, failures := chooseSetupRoleModels(context.Background(), installed, models, probe)
	if strings.Join(tested, ",") != "codex,opencode,claude" {
		t.Fatalf("probe order = %v", tested)
	}
	if len(got) != 2 || got[0].Harness != "opencode" || got[1].Harness != "claude" {
		t.Fatalf("usable models = %+v", got)
	}
	if len(failures) != 1 || !strings.Contains(failures[0], "429 quota exhausted") {
		t.Fatalf("failure reasons = %v", failures)
	}
}
