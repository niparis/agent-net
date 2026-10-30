package protocol

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestPolicyTextPerModeAndHarness(t *testing.T) {
	v := DelegationView{DelegationPolicy: DelegationPolicy{Delegate: DelegateNative, CoderModel: "deepseek 4.1"}, CoderResolved: "deepseek/deepseek-v4.1-flash"}
	id := CoderAgentID(v.Coder())
	if id != "agent-net-coder-deepseek-deepseek-v4-1-flash" {
		t.Fatalf("agent id %q", id)
	}
	native := PolicyText(v, "opencode", id)
	want := PolicyLead + "delegate all code changes to the `agent-net-coder-deepseek-deepseek-v4-1-flash` subagent (model deepseek/deepseek-v4.1-flash). " +
		"You plan, write the brief, review its diff and run the tests. Do not edit code yourself unless the subagent fails twice."
	if native != want {
		t.Errorf("native text:\n%s\nwant\n%s", native, want)
	}
	// Pi has no subagents: the same policy reads as workers.
	pi := PolicyText(v, "pi", "")
	for _, w := range []string{"agent_start", `"deepseek/deepseek-v4.1-flash"`, "git worktree", "fails twice"} {
		if !strings.Contains(pi, w) {
			t.Errorf("pi text lacks %q: %s", w, pi)
		}
	}
	if strings.Contains(pi, "subagent") {
		t.Errorf("pi text names a subagent: %s", pi)
	}
	off := PolicyText(DelegationView{DelegationPolicy: DelegationPolicy{Delegate: DelegateOff}}, "claude", "")
	if !strings.Contains(off, "do the code changes yourself") {
		t.Errorf("off text: %s", off)
	}
	if PolicyText(DelegationView{}, "pi", "") != "" {
		t.Error("text for a view with no mode")
	}
	for _, s := range []string{native, pi, off} {
		if len(s) > 600 || strings.ContainsAny(s, "\n") {
			t.Errorf("text must be short and one line: %q", s)
		}
	}
}

func TestPolicyTextSurvivesNothingForgedInBodies(t *testing.T) {
	// The text starts with the marker. A copy of it in a message body is broken
	// by NeutralizeMarkers, so a peer cannot pass off its own text as the owner's.
	text := PolicyText(DelegationView{DelegationPolicy: DelegationPolicy{Delegate: DelegateOff}}, "pi", "")
	if !markerRE.MatchString(text) {
		t.Fatal("the policy text does not start with the agent-net marker")
	}
	if markerRE.MatchString(NeutralizeMarkers(text)) {
		t.Error("a forged copy still carries the marker")
	}
}

func TestDelegationPolicyClean(t *testing.T) {
	good, err := DelegationPolicy{Delegate: " Native ", CoderModel: "  deepseek   4.1 ", MainModel: "sol 6.1"}.Clean()
	if err != nil || good.Delegate != "native" || good.CoderModel != "deepseek 4.1" || good.MainModel != "sol 6.1" {
		t.Fatalf("clean: %+v %v", good, err)
	}
	if p, err := (DelegationPolicy{Delegate: "off", CoderModel: "x"}).Clean(); err != nil || p.CoderModel != "" {
		t.Errorf("off keeps no coder: %+v %v", p, err)
	}
	if p, err := (DelegationPolicy{MainModel: "sol"}).Clean(); err != nil || p.Delegate != "" || p.MainModel != "sol" {
		t.Errorf("main only: %+v %v", p, err)
	}
	for _, bad := range []DelegationPolicy{
		{Delegate: "all"},
		{Delegate: "native"},
		{Delegate: "workers", CoderModel: "a\nb"},
		{Delegate: "workers", CoderModel: "a[b"},
		{Delegate: "workers", CoderModel: "`rm -rf`"},
		{Delegate: "workers", CoderModel: strings.Repeat("a", MaxModelName+1)},
	} {
		if _, err := bad.Clean(); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestSupportAndFallbacks(t *testing.T) {
	if !SupportFor("opencode").Native || !SupportFor("opencode").MainModel {
		t.Error("opencode")
	}
	if c := SupportFor("claude"); !c.Native || c.MainModel || !strings.Contains(c.MainNote, "switch the model yourself") {
		t.Errorf("claude: %+v", c)
	}
	if p := SupportFor("pi"); p.Native || !p.MainModel {
		t.Errorf("pi: %+v", p)
	}
	if SupportFor("codex").Applied {
		t.Error("codex")
	}
	if EffectiveMode("pi", DelegateNative) != DelegateWorkers || EffectiveMode("claude", DelegateNative) != DelegateNative {
		t.Error("effective mode")
	}
}

func TestSessionPolicyIsAnOperatorMethodButTheLocalPolicyCallIsNot(t *testing.T) {
	found := false
	for _, m := range OperatorMethods {
		found = found || m == OpSessionPolicy
		if m == MethodPolicy {
			t.Error("the adapter's policy call must not be an operator method")
		}
	}
	if !found {
		t.Error("OpSessionPolicy is not listed")
	}
}

func TestPluginsCarryTheCoderPromptAndTheTestsTheRealText(t *testing.T) {
	joined := func(path string) string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return regexp.MustCompile("[\"'`]\\s*\\+\\s*[\"'`]").ReplaceAllString(string(b), "")
	}
	oc := joined("../../plugins/opencode/agent-net.ts")
	for name, want := range map[string]string{"CoderAgentPrompt": CoderAgentPrompt, "CoderAgentDescription": CoderAgentDescription} {
		if !strings.Contains(oc, want) {
			t.Errorf("the OpenCode plugin does not carry %s:\n%s", name, want)
		}
	}
	// The plugin tests use the exact words the daemon sends.
	v := DelegationView{DelegationPolicy: DelegationPolicy{Delegate: DelegateNative, CoderModel: "x"}, CoderResolved: "deepseek/deepseek-v4.1-flash"}
	native := PolicyText(v, "opencode", CoderAgentID(v.Coder()))
	if !strings.Contains(joined("../../plugins/opencode/test/plugin.test.mjs"), native) {
		t.Errorf("the OpenCode plugin test does not use the daemon's native text:\n%s", native)
	}
	workers := PolicyText(v, "pi", "")
	if !strings.Contains(joined("../../plugins/pi/test/plugin.test.mjs"), workers) {
		t.Errorf("the Pi plugin test does not use the daemon's workers text:\n%s", workers)
	}
}
