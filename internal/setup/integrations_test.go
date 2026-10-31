package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/integrations"
)

// integrationAnswers stores the owner's answers on the gateway, as a first
// setup run with a terminal does.
func setNetworkIntegrations(t *testing.T, f *fixture, on bool) {
	t.Helper()
	var in integrations.Answers
	if on {
		in = integrations.AllOn()
	}
	if err := f.gw.Store().SetIntegrations(in); err != nil {
		t.Fatal(err)
	}
}

func TestSetupOneQuestionAllOn(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	answer(t, "y\n")
	out := runAskingAll(t, Options{Join: f.newJoin(t)})
	for _, want := range []string{
		"Connect your agents fully? Recommended:",
		"Claude: accept messages from your other agents",
		"Codex: call agent-net tools without asking",
		"Quota: read your subscription quotas with quota-axi",
		"Details: docs/setup.md#integrations",
		"Turn all on? [Y/n/c]",
		"integrations: 7 of 7 on",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	got, ok, err := f.gw.Store().Integrations()
	if err != nil || !ok || got.CountOn() != 7 {
		t.Fatalf("network answers: %+v ok=%v err=%v", got, ok, err)
	}
	// The one question replaces the old questions.
	if strings.Contains(out, "Add the hook?") || strings.Contains(out, "Accept agent-net messages") {
		t.Errorf("an old question is still asked:\n%s", out)
	}
}

func TestQuotaAXIIsOnByDefaultWhenInstalled(t *testing.T) {
	f := newFixture(t)
	quotaPath(t, "22.19.0")
	local := filepath.Join(f.home, ".local", "bin")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "quota-axi"), []byte("#!/bin/sh\necho 0.1.66\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	in := (&runner{Options: Options{}}).currentIntegrations()
	if !in.QuotaAXI {
		t.Fatal("an installed quota-axi was not on by default")
	}
	if err := os.Remove(filepath.Join(local, "quota-axi")); err != nil {
		t.Fatal(err)
	}
	in = (&runner{Options: Options{}}).currentIntegrations()
	if in.QuotaAXI {
		t.Fatal("quota-axi was on without being installed")
	}
}

func TestSetupOneQuestionNone(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	answer(t, "n\n")
	out := runAskingAll(t, Options{Join: f.newJoin(t)})
	if !strings.Contains(out, "integrations: 0 of 7 on") {
		t.Errorf("output:\n%s", out)
	}
	got, ok, err := f.gw.Store().Integrations()
	if err != nil || !ok || got.CountOn() != 0 {
		t.Fatalf("network answers: %+v ok=%v err=%v", got, ok, err)
	}
}

func TestSetupOneQuestionCustom(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	// c, then yes, no, yes, no, yes, no.
	answer(t, "c\ny\nn\ny\nn\ny\nn\ny\n")
	out := runAskingAll(t, Options{Join: f.newJoin(t)})
	if !strings.Contains(out, "Claude: show your workers above the prompt? [Y/n]") {
		t.Errorf("custom question lines missing:\n%s", out)
	}
	got, ok, err := f.gw.Store().Integrations()
	if err != nil || !ok {
		t.Fatalf("network answers: %+v ok=%v err=%v", got, ok, err)
	}
	for i, spec := range integrations.Specs() {
		want := i%2 == 0
		if got.Get(spec.ID) != want {
			t.Errorf("%s = %v, want %v", spec.ID, got.Get(spec.ID), want)
		}
	}
}

func TestSetupIntegrationFlagsSkipTheQuestion(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	answer(t, "")
	out := runAskingAll(t, Options{Join: f.newJoin(t), All: AllNo})
	if strings.Contains(out, "Turn all on?") {
		t.Errorf("--none still asked:\n%s", out)
	}
	if !strings.Contains(out, "integrations: 0 of 7 on") {
		t.Errorf("output:\n%s", out)
	}
	if _, ok, _ := f.gw.Store().Integrations(); !ok {
		t.Error("--none did not store the answers")
	}
}

func TestSetupUsesNetworkAnswersWithoutAsking(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	setNetworkIntegrations(t, f, true)
	out := runAskingAll(t, Options{Join: f.newJoin(t)})
	if strings.Contains(out, "Turn all on?") {
		t.Errorf("asked although the network has answers:\n%s", out)
	}
	if !strings.Contains(out, "integrations: 7 of 7 on (your network settings)") {
		t.Errorf("output:\n%s", out)
	}
	if !strings.Contains(out, "claude hint hook on") {
		t.Errorf("network answer not applied:\n%s", out)
	}
}

func TestSetupWithoutAnswersKeepsWhatIsInstalled(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	runAskingAll(t, Options{Join: f.newJoin(t), HintHook: HintHookYes})
	before := readSettingsFile(t, f)
	if !strings.Contains(before, " hint") {
		t.Fatalf("setup did not install the hook:\n%s", before)
	}
	// An upgrade: no terminal, no network answers, no flags.
	out := runAskingAll(t, Options{})
	if strings.Contains(out, "Turn all on?") {
		t.Errorf("an upgrade asked:\n%s", out)
	}
	if got := readSettingsFile(t, f); got != before {
		t.Errorf("an upgrade without an answer changed the integrations:\n%s", got)
	}
}

func TestApplyIntegrationsKeepsTheLocalOptOut(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	var local integrations.Local
	local.Set(integrations.HintHook, false)
	if err := integrations.SaveLocal(filepath.Join(f.home, "cfg"), local); err != nil {
		t.Fatal(err)
	}
	if err := ApplyIntegrations(integrations.AllOn(), nil); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := readSettingsFile(t, f)
	if strings.Contains(got, " hint\"") || !strings.Contains(got, "needs-hook\"") {
		t.Errorf("settings after apply:\n%s", got)
	}
	applied, ok := integrations.LoadApplied(filepath.Join(f.home, "cfg"))
	if !ok || applied.HintHook || !applied.NeedsHook {
		t.Errorf("applied record: %+v ok=%v", applied, ok)
	}
	// The same answers again change nothing, even when the file was edited.
	before := readSettingsFile(t, f)
	if err := ApplyIntegrations(integrations.AllOn(), nil); err != nil {
		t.Fatal(err)
	}
	if readSettingsFile(t, f) != before {
		t.Error("a second apply rewrote the settings")
	}
}

func TestSetupLocalOptOutWinsOverNetwork(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	setNetworkIntegrations(t, f, true)
	out := runAskingAll(t, Options{Join: f.newJoin(t), HintHook: HintHookNo})
	if !strings.Contains(out, "claude hint hook off") {
		t.Errorf("opt out not applied:\n%s", out)
	}
	local := integrations.LoadLocal(filepath.Join(f.home, "cfg"))
	if on, ok := local.Get(integrations.HintHook); !ok || on {
		t.Fatalf("local override: %+v", local)
	}
	// A later run without the flag keeps the local opt-out.
	out = runAskingAll(t, Options{Join: f.newJoin(t)})
	if !strings.Contains(out, "claude hint hook off") {
		t.Errorf("local opt out lost:\n%s", out)
	}
	if b, _ := os.ReadFile(filepath.Join(f.home, ".claude", "settings.json")); strings.Contains(string(b), "agent-net hint") {
		t.Errorf("hint hook installed despite the local opt out:\n%s", b)
	}
}
